package tts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"voicebook/internal/audio"
)

func init() {
	miniMaxRetryDelays = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	miniMaxPollDelay = 10 * time.Millisecond
	miniMaxThrottle = 50 * time.Millisecond
}

func tinyWAV(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := audio.EncodeWAV16(&buf, &audio.PCM{Rate: 44100, Channels: 1, Data: make([]float32, 4410)}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMiniMaxRequestText(t *testing.T) {
	r := SynthesizeRequest{
		Text:  "他笑了(真的)\x01，行吧。",
		Prons: []Pron{{Offset: 9, Char: "行", Pinyin: "xíng"}},
		Cues:  []Cue{{At: 0, Tag: "sad"}, {At: 2, Tag: "giggles"}, {At: 4, Tag: "sarcastic"}},
		Speed: 1.1, Pitch: 2.4, Volume: 6,
	}
	q, err := BuildMiniMaxRequest("speech-2.8-hd", r, MiniMaxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// "(真的)" -> fullwidth so it is not read as markup; control char dropped; reading replaces 行; giggle inserted at 2
	if want := "他笑(chuckle)了（真的），(xing2)吧。"; q.Text != want {
		t.Fatalf("text = %q, want like %q", q.Text, want)
	}
	if strings.ContainsRune(q.Text, '\x01') || strings.Contains(q.Text, "(真的)") {
		t.Fatalf("text not sanitized: %q", q.Text)
	}
	if q.Emotion != "sad" { // style cue at the start becomes the line emotion
		t.Fatalf("emotion = %q", q.Emotion)
	}
	if q.Pitch != 2 || q.Speed != 1.1 || q.Vol < 1.99 || q.Vol > 2.0 {
		t.Fatalf("prosody: pitch=%d speed=%v vol=%v", q.Pitch, q.Speed, q.Vol)
	}
	if q.LanguageBoost != "" {
		t.Fatalf("chinese book should not boost, got %q", q.LanguageBoost)
	}

	// older models: no interjection tags, emotion explicit wins over cue
	r.Emotion = "happy"
	q, _ = BuildMiniMaxRequest("speech-02-hd", r, MiniMaxOptions{})
	if strings.Contains(q.Text, "(chuckle)") || q.Emotion != "happy" {
		t.Fatalf("02 model: text=%q emotion=%q", q.Text, q.Emotion)
	}
}

func TestMiniMaxEmotionAndLanguage(t *testing.T) {
	cases := []struct {
		model, emotion, want string
	}{
		{"speech-2.8-hd", "neutral", ""}, {"speech-2.8-hd", "whisper", ""}, {"speech-2.6-hd", "whisper", "whisper"},
		{"speech-2.8-turbo", "fearful", "fearful"}, {"speech-2.8-hd", "gentle", ""},
	}
	for _, c := range cases {
		q, _ := BuildMiniMaxRequest(c.model, SynthesizeRequest{Text: "好", Emotion: c.emotion}, MiniMaxOptions{})
		if q.Emotion != c.want {
			t.Errorf("%s/%s: got %q want %q", c.model, c.emotion, q.Emotion, c.want)
		}
	}
	q, _ := BuildMiniMaxRequest("speech-2.8-hd", SynthesizeRequest{Text: "早晨", ProviderVoiceID: "Cantonese_GentleLady"}, MiniMaxOptions{})
	if q.LanguageBoost != "Chinese,Yue" {
		t.Fatalf("cantonese boost = %q", q.LanguageBoost)
	}
	q, _ = BuildMiniMaxRequest("speech-2.8-hd", SynthesizeRequest{Text: "Hi", Language: "en-US"}, MiniMaxOptions{LanguageBoost: "auto"})
	if q.LanguageBoost != "English" {
		t.Fatalf("english boost = %q", q.LanguageBoost)
	}
	q, _ = BuildMiniMaxRequest("speech-2.8-hd", SynthesizeRequest{Text: "你好"}, MiniMaxOptions{LanguageBoost: "auto"})
	if q.LanguageBoost != "auto" {
		t.Fatalf("fallback boost = %q", q.LanguageBoost)
	}
	if _, err := BuildMiniMaxRequest("speech-2.8-hd", SynthesizeRequest{Text: "  "}, MiniMaxOptions{}); err == nil {
		t.Fatal("empty text accepted")
	}
}

func TestMiniMaxCapabilities(t *testing.T) {
	c := MiniMaxCapabilities("speech-2.8-hd")
	if !c.InlineTags || len(c.TagKeys) != len(miniMaxSoundTags) || !c.Phoneme || c.Instruction {
		t.Fatalf("2.8 caps = %+v", c)
	}
	if c := MiniMaxCapabilities("speech-2.6-turbo"); c.InlineTags || c.Emotions[len(c.Emotions)-1] != "whisper" {
		t.Fatalf("2.6 caps = %+v", c)
	}
	for _, v := range MiniMaxCatalog() {
		if v.ID == "" || v.Name == "" || v.Language == "" {
			t.Fatalf("incomplete catalog entry %+v", v)
		}
	}
	if len(MiniMaxCatalog()) < 300 || MiniMaxLookup("Cantonese_ProfessionalHost（F)") == nil {
		t.Fatal("catalog incomplete")
	}
	rv := MiniMaxLookup("Chinese (Mandarin)_Radio_Host").Remote("speech-2.8-hd")
	if rv.PriceTier != "premium" || !rv.Recommended || rv.CrowdPool {
		t.Fatalf("remote = %+v", rv)
	}
	if ModelLabel("speech-2.8-turbo") != "MiniMax 2.8 Turbo" || ModelLabel("cosyvoice-v2") != "CosyVoice v2" {
		t.Fatal("model labels")
	}
}

type mmServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	body  map[string]any
}

func newMMServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body map[string]any) bool) *mmServer {
	s := &mmServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mm-key" {
			w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`))
			return
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw)) // handlers may parse it again (multipart uploads)
		json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.paths = append(s.paths, r.URL.Path+"?"+r.URL.RawQuery)
		if body != nil {
			s.body = body
		}
		s.mu.Unlock()
		if !h(w, r, body) {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestMiniMaxSyncWithRetry(t *testing.T) {
	wav := tinyWAV(t)
	var calls int32
	srv := newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		if r.URL.Path != "/v1/t2a_v2" {
			return false
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Write([]byte(`{"base_resp":{"status_code":1002,"status_msg":"rate limit"}}`))
			return true
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"audio": hex.EncodeToString(wav), "status": 2}, "extra_info": map[string]any{"audio_sample_rate": 44100, "audio_format": "wav"}, "base_resp": map[string]any{"status_code": 0, "status_msg": "success"}})
		return true
	})
	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", Model: "speech-2.8-turbo", RPM: 6000, Config: map[string]any{"groupId": "g1", "textNormalization": "quality"}})
	a, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "你好(laughs)", ProviderVoiceID: "female-chengshu", Emotion: "happy"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || a.Format != "wav" || !bytes.HasPrefix(a.Data, []byte("RIFF")) {
		t.Fatalf("calls=%d format=%s", calls, a.Format)
	}
	if !strings.Contains(srv.paths[0], "GroupId=g1") {
		t.Fatalf("group id missing: %v", srv.paths)
	}
	vs := srv.body["voice_setting"].(map[string]any)
	as := srv.body["audio_setting"].(map[string]any)
	if srv.body["model"] != "speech-2.8-turbo" || vs["voice_id"] != "female-chengshu" || vs["emotion"] != "happy" || as["format"] != "wav" ||
		srv.body["text_normalization_mode"] != "quality" || srv.body["text"] != "你好（laughs）" {
		t.Fatalf("body = %v", srv.body)
	}
}

func TestMiniMaxErrors(t *testing.T) {
	srv := newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		w.Write([]byte(`{"base_resp":{"status_code":2054,"status_msg":"voice id not exist"}}`))
		return true
	})
	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", RPM: 6000})
	_, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "你好", ProviderVoiceID: "nope"})
	if err == nil || !strings.Contains(err.Error(), "「nope」在 MiniMax 上不存在") {
		t.Fatalf("err = %v", err)
	}
	p, _ = New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "wrong", RPM: 6000})
	if _, err = p.Synthesize(context.Background(), SynthesizeRequest{Text: "你好"}); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("auth err = %v", err)
	}
	p, _ = New(Account{Provider: "minimax", BaseURL: srv.URL})
	if _, err = p.Synthesize(context.Background(), SynthesizeRequest{Text: "你好"}); err == nil || !strings.Contains(err.Error(), "缺少 API Key") {
		t.Fatalf("missing key err = %v", err)
	}
}

func tarOf(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		tw.Write(data)
	}
	tw.Close()
	return buf.Bytes()
}

func TestMiniMaxAsync(t *testing.T) {
	wav := tinyWAV(t)
	var polls int32
	srv := newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		switch r.URL.Path {
		case "/v1/t2a_async_v2":
			w.Write([]byte(`{"task_id":95157322514444,"file_id":95157322514445,"usage_characters":10,"base_resp":{"status_code":0,"status_msg":"success"}}`))
		case "/v1/query/t2a_async_query_v2":
			if r.URL.Query().Get("task_id") != "95157322514444" {
				t.Errorf("task id = %q", r.URL.Query().Get("task_id"))
			}
			st := "Processing"
			if atomic.AddInt32(&polls, 1) >= 3 {
				st = "Success"
			}
			w.Write([]byte(`{"task_id":95157322514444,"status":"` + st + `","file_id":95157322514445,"base_resp":{"status_code":0,"status_msg":"success"}}`))
		case "/v1/files/retrieve_content":
			w.Header().Set("Content-Type", "application/x-tar")
			w.Write(tarOf(t, map[string][]byte{"95157322514445/content.wav": wav, "95157322514445/content.json": []byte(`[]`), "95157322514445/content_extra.json": []byte(`{}`)}))
		default:
			return false
		}
		return true
	})
	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", RPM: 6000, Config: map[string]any{"synthMode": "async"}})
	a, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "很长的一段文字", ProviderVoiceID: "male-qn-jingying", Model: "speech-2.6-hd"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Data, wav) || polls < 3 {
		t.Fatalf("polls=%d len=%d", polls, len(a.Data))
	}
	as := srv.body["audio_setting"]
	if as == nil {
		// last body recorded is the create call (GETs have no body)
		t.Fatalf("no async body recorded")
	}
	if as.(map[string]any)["audio_sample_rate"] == nil || srv.body["stream"] != nil {
		t.Fatalf("async body = %v", srv.body)
	}
}

func TestExtractMiniMaxAudio(t *testing.T) {
	wav := tinyWAV(t)
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("out/title.json")
	f.Write([]byte("{}"))
	f, _ = zw.Create("out/content.mp3")
	f.Write([]byte("ID3fake"))
	f, _ = zw.Create("out/content.wav")
	f.Write(wav)
	zw.Close()
	data, name, err := extractMiniMaxAudio(zb.Bytes())
	if err != nil || name != "content.wav" || !bytes.Equal(data, wav) {
		t.Fatalf("zip: name=%s err=%v", name, err)
	}
	if data, _, err := extractMiniMaxAudio(wav); err != nil || !bytes.Equal(data, wav) {
		t.Fatalf("bare: %v", err)
	}
	if _, _, err := extractMiniMaxAudio(tarOf(t, map[string][]byte{"a.json": []byte("{}")})); err == nil {
		t.Fatal("tar without audio accepted")
	}
}

func TestRateGateSpacing(t *testing.T) {
	g := &rateGate{interval: 20 * time.Millisecond}
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := g.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 55*time.Millisecond {
		t.Fatalf("4 requests took %v, want >= 60ms", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.Pause(time.Hour)
	cancel()
	if err := g.Wait(ctx); err == nil {
		t.Fatal("wait ignored cancellation")
	}
}

func TestMiniMaxListVoices(t *testing.T) {
	srv := newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		if r.URL.Path != "/v1/get_voice" || body["voice_type"] != "system" {
			return false
		}
		w.Write([]byte(`{"system_voice":[{"voice_id":"female-chengshu","voice_name":"成熟女性音色","description":[]},{"voice_id":"moss_audio_new","voice_name":"新上线女声","description":["温柔"]}],"base_resp":{"status_code":0,"status_msg":"success"}}`))
		return true
	})
	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", Model: "speech-2.6-hd", RPM: 6000})
	list, err := p.(VoiceLister).ListVoices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 6 { // 2 voices x (2 catalog models + account model)
		t.Fatalf("got %d voices", len(list))
	}
	var fresh *RemoteVoice
	for i := range list {
		if list[i].ProviderVoiceID == "moss_audio_new" {
			fresh = &list[i]
		}
	}
	if fresh == nil || fresh.Gender != "female" || fresh.Language != "zh-CN" {
		t.Fatalf("guessed voice = %+v", fresh)
	}
}

func TestMiniMaxCloneVoiceID(t *testing.T) {
	for _, seed := range []string{"01a1afa6-bc9c-789c-bae3-d8082e3420f6", "x", "我的声音", strings.Repeat("ab", 100)} {
		for attempt := 0; attempt < 3; attempt++ {
			id := MiniMaxCloneVoiceID(seed, attempt)
			if len(id) < 8 || len(id) > 256 || !(id[0] >= 'a' && id[0] <= 'z') || strings.ContainsAny(id[len(id)-1:], "-_") {
				t.Fatalf("bad id %q for seed %q", id, seed)
			}
			for _, r := range id {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
					t.Fatalf("bad char in %q", id)
				}
			}
		}
	}
	if MiniMaxCloneVoiceID("s1", 0) == MiniMaxCloneVoiceID("s1", 1) {
		t.Fatal("attempts must differ")
	}
}

func wavFile(t *testing.T, seconds float64) string {
	t.Helper()
	p := t.TempDir() + "/sample.wav"
	if err := audio.WriteWAVFile(p, &audio.PCM{Rate: 32000, Channels: 1, Data: make([]float32, int(32000*seconds))}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMiniMaxClone(t *testing.T) {
	demo := tinyWAV(t)
	var mu sync.Mutex
	taken := map[string]bool{"vbvoice0001": true} // first id already exists on the platform
	var uploadPurpose, uploadName string
	var cloneBody map[string]any
	deleted := ""
	var srv *mmServer
	srv = newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/files/upload":
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			uploadPurpose = r.FormValue("purpose")
			_, fh, _ := r.FormFile("file")
			uploadName = fh.Filename
			w.Write([]byte(`{"file":{"file_id":312345678901,"bytes":10,"filename":"sample.wav","purpose":"voice_clone"},"base_resp":{"status_code":0,"status_msg":"success"}}`))
		case "/v1/voice_clone":
			cloneBody = body
			if taken[body["voice_id"].(string)] {
				w.Write([]byte(`{"base_resp":{"status_code":2039,"status_msg":"voice clone voice id duplicate"}}`))
				return true
			}
			w.Write([]byte(`{"input_sensitive":{"type":0},"demo_audio":"` + srv.URL + `/demo.wav","base_resp":{"status_code":0,"status_msg":"success"}}`))
		case "/demo.wav":
			w.Write(demo)
		case "/v1/delete_voice":
			deleted = body["voice_id"].(string)
			w.Write([]byte(`{"voice_id":"x","base_resp":{"status_code":0,"status_msg":"success"}}`))
		default:
			return false
		}
		return true
	})
	// the demo link is public: the test server accepts it without auth
	srv.Config.Handler = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/demo.wav" {
				w.Write(demo)
				return
			}
			next.ServeHTTP(w, r)
		})
	}(srv.Config.Handler)

	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", Model: "speech-2.8-hd", RPM: 6000})
	if !p.Capabilities().QuickClone || !p.Capabilities().CloneExpiresUnused || p.Capabilities().CloneExpiryDays != 7 {
		t.Fatalf("caps = %+v", p.Capabilities())
	}
	cl := p.(VoiceCloner)
	res, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: wavFile(t, 12), Seed: "voice-0001", Denoise: true, PreviewText: "你好，听起来像我吗？", Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ProviderVoiceID != "vbvoice0001r1" || res.Status != "ready" || res.ExpiresInDays != 7 || res.Preview == nil || !bytes.Equal(res.Preview.Data, demo) {
		t.Fatalf("res = %+v", res)
	}
	if uploadPurpose != "voice_clone" || uploadName != "sample.wav" {
		t.Fatalf("upload purpose=%q name=%q", uploadPurpose, uploadName)
	}
	if cloneBody["file_id"].(float64) != 312345678901 || cloneBody["model"] != "speech-2.8-hd" || cloneBody["text"] != "你好，听起来像我吗？" ||
		cloneBody["need_noise_reduction"] != true || cloneBody["need_volume_normalization"] != true {
		t.Fatalf("clone body = %v", cloneBody)
	}
	if err := cl.DeleteVoice(context.Background(), res.ProviderVoiceID); err != nil || deleted != res.ProviderVoiceID {
		t.Fatalf("delete: %v %q", err, deleted)
	}
	if st, _ := cl.CloneStatus(context.Background(), "abc"); st.Status != "ready" {
		t.Fatal("status")
	}
	// sample rules are enforced before anything is uploaded
	if _, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: wavFile(t, 4), Seed: "s"}); err == nil || !strings.Contains(err.Error(), "至少 10 秒") {
		t.Fatalf("short sample err = %v", err)
	}
	if _, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: wavFile(t, 12), Seed: "s", Tier: "pro"}); err == nil {
		t.Fatal("pro clone accepted")
	}
}

func TestMiniMaxCloneErrors(t *testing.T) {
	srv := newMMServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
		switch r.URL.Path {
		case "/v1/files/upload":
			w.Write([]byte(`{"file":{"file_id":1},"base_resp":{"status_code":0}}`))
		case "/v1/voice_clone":
			w.Write([]byte(`{"base_resp":{"status_code":2038,"status_msg":"no clone permission"}}`))
		case "/v1/delete_voice":
			w.Write([]byte(`{"base_resp":{"status_code":2054,"status_msg":"voice id not exist"}}`))
		default:
			return false
		}
		return true
	})
	p, _ := New(Account{Provider: "minimax", BaseURL: srv.URL, APIKey: "mm-key", RPM: 6000})
	_, err := p.(VoiceCloner).CloneVoice(context.Background(), CloneRequest{MergedPath: wavFile(t, 11), Seed: "abcdefgh"})
	if err == nil || !strings.Contains(err.Error(), "实名认证") {
		t.Fatalf("err = %v", err)
	}
	if err := p.(VoiceCloner).DeleteVoice(context.Background(), "gone-voice"); err != nil {
		t.Fatalf("deleting a voice that no longer exists should succeed: %v", err)
	}
}
