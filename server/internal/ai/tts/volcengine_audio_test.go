package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"voicebook/internal/audio"
)

// fakeVolcAudio speaks the Volcengine audio generation protocol on 127.0.0.1; nothing reaches the real service.
type fakeVolcAudio struct {
	mu     sync.Mutex
	bodies []map[string]any
	header http.Header
	reply  func(n int, body map[string]any) (int, any)
}

func (f *fakeVolcAudio) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/tts/create" || r.Method != http.MethodPost {
			w.WriteHeader(404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.header = r.Header.Clone()
		n := len(f.bodies)
		f.mu.Unlock()
		status, out := 200, any(nil)
		if f.reply != nil {
			status, out = f.reply(n, body)
		}
		if out == nil {
			out = map[string]any{"audio": base64.StdEncoding.EncodeToString(wavOf(1.2)), "duration": 1.2, "original_duration": 1.2}
		}
		w.Header().Set("X-Tt-Logid", "log-1")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wavOf(seconds float64) []byte {
	p := &audio.PCM{Rate: 44100, Channels: 1, Data: make([]float32, int(44100*seconds))}
	for i := range p.Data {
		p.Data[i] = float32(i%100) / 400
	}
	var b bytes.Buffer
	audio.EncodeWAV16(&b, p)
	return b.Bytes()
}

type memRefs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memRefs) PutRef(_ context.Context, name string, b []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	id := "voice-refs/" + name + ".wav"
	m.data[id] = b
	return id, nil
}
func (m *memRefs) GetRef(_ context.Context, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return b, nil
}
func (m *memRefs) DeleteRef(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}

func TestVolcCatalog(t *testing.T) {
	if len(VolcCatalog()) < 400 {
		t.Fatalf("catalog too small: %d", len(VolcCatalog()))
	}
	v := VolcLookup("zh_female_vv_uranus_bigtts")
	if v == nil || v.Name != "Vivi" || v.Gender != "female" || v.Language != "zh-CN" || !v.Recommended {
		t.Fatalf("Vivi: %+v", v)
	}
	if v := VolcLookup("en_male_tim_uranus_bigtts"); v == nil || v.Gender != "male" || v.Language != "en-US" {
		t.Fatalf("Tim: %+v", v)
	}
	if VolcLookup("en_male_brad_pitt_p1_uranus_bigtts") != nil {
		t.Fatal("voices imitating a real person are left out")
	}
	seen := map[string]bool{}
	for _, x := range VolcCatalog() {
		if seen[x.ID] || x.Name == "" || (x.Gender != "male" && x.Gender != "female" && x.Gender != "unknown") {
			t.Fatalf("bad entry %+v", x)
		}
		seen[x.ID] = true
	}
}

func TestBuildVolcPrompt(t *testing.T) {
	r := SynthesizeRequest{Text: "你终于来了，“快进来”。", Emotion: "happy", Intensity: 3, Instruction: "压低声音，像在说悄悄话。", Language: "zh-CN"}
	got := BuildVolcPrompt(r, "", true)
	want := "纯人声，无背景音乐和音效。（开心，情绪饱满强烈，压低声音，像在说悄悄话）说道：“你终于来了，「快进来」。”"
	if got != want {
		t.Fatalf("prompt:\n got %s\nwant %s", got, want)
	}
	if got := BuildVolcPrompt(SynthesizeRequest{Text: "天黑了。"}, "@音频1", false); got != "@音频1说道：“天黑了。”" {
		t.Fatalf("reference voice prompt: %s", got)
	}
	en := BuildVolcPrompt(SynthesizeRequest{Text: `He said "run".`, Emotion: "fearful", Language: "en-US"}, "", true)
	if en != `Voice only, no background music or sound effects. (frightened) says: "He said 'run'."` {
		t.Fatalf("english prompt: %s", en)
	}
	// a style tag at the start of the line becomes direction
	tagged := BuildVolcPrompt(SynthesizeRequest{Text: "别出声", Cues: []Cue{{At: 0, Tag: "whispers"}}}, "", true)
	if !strings.Contains(tagged, "（压低声音耳语）") {
		t.Fatalf("style cue: %s", tagged)
	}
	// tags inside the line: sounds become actions between quoted stretches, a style tag changes what follows
	line := "你真傻。我早就知道了。"
	inline := BuildVolcPrompt(SynthesizeRequest{Text: line, Emotion: "sad", Cues: []Cue{{At: 0, Tag: "sighing"}, {At: 4, Tag: "giggles"}, {At: 4, Tag: "crying"}, {At: 11, Tag: "cough"}}}, "@音频1", false)
	if want := "@音频1（悲伤）叹了口气，说道：“你真傻。”轻笑了一声，接着（带着哭腔）说：“我早就知道了。”说完咳嗽了一声。"; inline != want {
		t.Fatalf("inline tags:\n got %s\nwant %s", inline, want)
	}
	// older whole-line tags count as tags at the start
	if got := BuildVolcPrompt(SynthesizeRequest{Text: "好吧。", Tags: []string{"叹气"}}, "", false); got != "叹了口气，说道：“好吧。”" {
		t.Fatalf("legacy tag: %s", got)
	}
	// a style tag with nothing after it changes nothing; straight quotes inside the line are paired
	if got := BuildVolcPrompt(SynthesizeRequest{Text: `他说"走"。`, Cues: []Cue{{At: 6, Tag: "crying"}}}, "", false); got != "说道：“他说「走」。”" {
		t.Fatalf("trailing style / quotes: %s", got)
	}
	enInline := BuildVolcPrompt(SynthesizeRequest{Text: "Fine. I knew it.", Language: "en-US", Cues: []Cue{{At: 5, Tag: "sighing"}, {At: 16, Tag: "giggles"}}}, "", false)
	if enInline != `says: "Fine." Then sighs, and continues: "I knew it." Then giggles.` {
		t.Fatalf("english inline: %s", enInline)
	}
}

func TestVolcPiecesKeepTagsInPlace(t *testing.T) {
	text := "  " + strings.Repeat("一二三四五六七八九。", 3) // 30 characters after two spaces
	pieces := volcPieces(SynthesizeRequest{Text: text, Cues: []Cue{{At: 2, Tag: "whispers"}, {At: 15, Tag: "sighing"}, {At: 22, Tag: "crying"}, {At: 32, Tag: "giggles"}}}, 10)
	if len(pieces) != 3 {
		t.Fatalf("pieces: %d", len(pieces))
	}
	if !reflect.DeepEqual(pieces[0].Cues, []Cue{{At: 0, Tag: "whispers"}}) {
		t.Fatalf("piece 1: %v", pieces[0].Cues)
	}
	// the whisper set at the start still holds in piece 2; the sigh lands where it was, 3 characters in
	if !reflect.DeepEqual(pieces[1].Cues, []Cue{{At: 0, Tag: "whispers"}, {At: 3, Tag: "sighing"}}) {
		t.Fatalf("piece 2: %v", pieces[1].Cues)
	}
	// crying takes over at the start of piece 3 (instead of the whisper); the laugh is at its end
	if !reflect.DeepEqual(pieces[2].Cues, []Cue{{At: 0, Tag: "crying"}, {At: 10, Tag: "giggles"}}) {
		t.Fatalf("piece 3: %v", pieces[2].Cues)
	}
	if p := BuildVolcPrompt(pieces[2], "", false); p != "（带着哭腔）说道：“一二三四五六七八九。”说完轻笑了一声。" {
		t.Fatalf("piece 3 prompt: %s", p)
	}
}

func TestSplitForVolc(t *testing.T) {
	long := strings.Repeat("今天的风很大，街上几乎没有人。", 30)
	parts := splitForVolc(long, 100)
	if len(parts) < 4 {
		t.Fatalf("parts: %d", len(parts))
	}
	joined := strings.Join(parts, "")
	if joined != long {
		t.Fatal("splitting must keep every character")
	}
	for _, p := range parts {
		if n := len([]rune(p)); n > 100 {
			t.Fatalf("piece too long: %d", n)
		}
	}
	if got := splitForVolc("短句。", 100); len(got) != 1 {
		t.Fatalf("short: %v", got)
	}
}

func TestVolcAudioSynthesizePresetVoice(t *testing.T) {
	f := &fakeVolcAudio{}
	srv := f.serve(t)
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL + "/", APIKey: " vk ", Config: map[string]any{}})
	out, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "你好。", ProviderVoiceID: "zh_male_m191_uranus_bigtts", Speed: 1.5, Pitch: -2, Volume: 6, Emotion: "sad", Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Format != "wav" || len(out.Data) < 1000 {
		t.Fatalf("audio: %s %d", out.Format, len(out.Data))
	}
	b := f.bodies[0]
	if b["model"] != "seed-audio-1.0" || f.header.Get("X-Api-Key") != "vk" || f.header.Get("X-Api-Request-Id") == "" {
		t.Fatalf("request: %v %v", b["model"], f.header)
	}
	refs := b["references"].([]any)
	if refs[0].(map[string]any)["speaker"] != "zh_male_m191_uranus_bigtts" {
		t.Fatalf("speaker: %v", refs)
	}
	cfg := b["audio_config"].(map[string]any)
	if cfg["format"] != "wav" || cfg["sample_rate"] != 44100.0 || cfg["speech_rate"] != 50.0 || cfg["pitch_rate"] != -2.0 || cfg["loudness_rate"] != 100.0 {
		t.Fatalf("audio_config: %v", cfg)
	}
	if !strings.Contains(b["text_prompt"].(string), "（悲伤）说道：“你好。”") {
		t.Fatalf("prompt: %v", b["text_prompt"])
	}
	if !BillsByDuration(p) {
		t.Fatal("audio generation is billed by duration")
	}
}

func TestVolcAudioOldConsoleAuth(t *testing.T) {
	f := &fakeVolcAudio{}
	srv := f.serve(t)
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "token", Config: map[string]any{"appId": "12345"}})
	if _, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "测试"}); err != nil {
		t.Fatal(err)
	}
	if f.header.Get("X-Api-App-Id") != "12345" || f.header.Get("X-Api-Access-Key") != "token" || f.header.Get("X-Api-Key") != "" {
		t.Fatalf("old console headers: %v", f.header)
	}
}

func TestVolcAudioLongTextIsSplitAndJoined(t *testing.T) {
	f := &fakeVolcAudio{}
	srv := f.serve(t)
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "k", Config: map[string]any{"maxChars": 60.0}})
	text := strings.Repeat("风从山谷里吹过来，带着潮湿的泥土味。", 8)
	out, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: text, Cues: []Cue{{At: 0, Tag: "whispers"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) < 3 {
		t.Fatalf("requests: %d", len(f.bodies))
	}
	for i, b := range f.bodies {
		if !strings.Contains(b["text_prompt"].(string), "耳语") {
			t.Fatalf("a style tag holds until the next one, across pieces: piece %d: %s", i+1, b["text_prompt"])
		}
	}
	if len(out.Prompts) != len(f.bodies) || out.Prompts[1] != f.bodies[1]["text_prompt"] {
		t.Fatalf("every prompt sent is reported back: %d of %d", len(out.Prompts), len(f.bodies))
	}
	pcm, err := audio.DecodeWAV(bytes.NewReader(out.Data))
	if err != nil {
		t.Fatal(err)
	}
	if ms := pcm.DurationMs(); ms < int64(len(f.bodies))*1200 {
		t.Fatalf("joined audio too short: %d ms for %d pieces", ms, len(f.bodies))
	}
}

func TestVolcAudioErrors(t *testing.T) {
	cases := []struct {
		status int
		body   any
		want   string
		calls  int
	}{
		{401, map[string]any{"code": 45000010, "message": "invalid api key"}, "密钥无效", 1},
		{400, map[string]any{"code": 45001115, "message": "speaker S_x not found in speaker_map"}, "找不到这个音色", 1},
		{400, map[string]any{"code": 45001125, "message": "demo text audit failed"}, "安全审核", 1},
		{400, map[string]any{"code": 40000020, "message": "InvalidPayload:DurationOutOfRange"}, "超过 2 分钟", 1},
		{500, map[string]any{"code": 55001309, "message": "audio generate internal error"}, "繁忙或暂时出错", 3},
		{200, map[string]any{"code": 45001116, "message": "text_prompt length 3961 exceeds maximum of 3000"}, "3000", 1},
	}
	for _, c := range cases {
		f := &fakeVolcAudio{reply: func(int, map[string]any) (int, any) { return c.status, c.body }}
		srv := f.serve(t)
		p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "k"})
		p.(*volcAudio).retryWait = time.Millisecond
		_, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "测试"})
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "log-1") {
			t.Errorf("%d %v: %v", c.status, c.body, err)
		}
		if len(f.bodies) != c.calls {
			t.Errorf("%v: %d calls, want %d", c.body, len(f.bodies), c.calls)
		}
	}
}

func TestVolcAudioCheckDoesNotGenerate(t *testing.T) {
	f := &fakeVolcAudio{reply: func(_ int, body map[string]any) (int, any) {
		if body["text_prompt"] == "" {
			return 400, map[string]any{"code": 45001001, "message": "text_prompt is required"}
		}
		return 200, nil
	}}
	srv := f.serve(t)
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "k"})
	msg, err := p.(Checker).Check(context.Background())
	if err != nil || !strings.Contains(msg, "不产生费用") {
		t.Fatalf("check: %q %v", msg, err)
	}
	bad := &fakeVolcAudio{reply: func(int, map[string]any) (int, any) {
		return 401, map[string]any{"code": 45000010, "message": "invalid api key"}
	}}
	srv2 := bad.serve(t)
	p2, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv2.URL, APIKey: "bad"})
	if _, err := p2.(Checker).Check(context.Background()); err == nil {
		t.Fatal("a wrong key must fail the check")
	}
}

func TestVolcAudioReferenceVoices(t *testing.T) {
	f := &fakeVolcAudio{}
	srv := f.serve(t)
	refs := &memRefs{}
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "k", Refs: refs})
	// cloning keeps at most 30 s of the sample on this site
	dir := t.TempDir()
	sample := filepath.Join(dir, "merged.wav")
	os.WriteFile(sample, wavOf(40), 0o644)
	res, err := p.(VoiceCloner).CloneVoice(context.Background(), CloneRequest{MergedPath: sample, Seed: "v1"})
	if err != nil || res.Status != "ready" || !strings.HasPrefix(res.ProviderVoiceID, "ref:voice-refs/") {
		t.Fatalf("clone: %+v %v", res, err)
	}
	stored, _ := refs.GetRef(context.Background(), strings.TrimPrefix(res.ProviderVoiceID, "ref:"))
	pcm, err := audio.DecodeWAV(bytes.NewReader(stored))
	if err != nil || pcm.DurationMs() > 30000 || pcm.DurationMs() < 29000 {
		t.Fatalf("reference clip: %v ms, %v", pcm.DurationMs(), err)
	}
	if len(f.bodies) != 0 {
		t.Fatal("cloning must not call the platform")
	}
	// synthesis sends the recording and names it @音频1
	if _, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "早上好。", ProviderVoiceID: res.ProviderVoiceID}); err != nil {
		t.Fatal(err)
	}
	ref := f.bodies[0]["references"].([]any)[0].(map[string]any)
	if ref["speaker"] != nil || ref["audio_data"] != base64.StdEncoding.EncodeToString(stored) {
		t.Fatal("reference recording not sent")
	}
	if !strings.HasPrefix(strings.TrimPrefix(f.bodies[0]["text_prompt"].(string), "纯人声，无背景音乐和音效。"), "@音频1") {
		t.Fatalf("prompt: %v", f.bodies[0]["text_prompt"])
	}
	// deleting the voice removes the recording
	p.(VoiceCloner).DeleteVoice(context.Background(), res.ProviderVoiceID)
	if _, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "x", ProviderVoiceID: res.ProviderVoiceID}); err == nil {
		t.Fatal("a deleted reference cannot be used")
	}
}

func TestVolcAudioDesignVoice(t *testing.T) {
	f := &fakeVolcAudio{}
	srv := f.serve(t)
	refs := &memRefs{}
	p, _ := New(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "k", Refs: refs})
	cands, err := p.(VoiceDesigner).DesignVoice(context.Background(), DesignRequest{Description: "中年男性，嗓音低沉沙哑，台湾口音。", PreviewText: "你好", Count: 3, Seed: "d1"})
	if err != nil || len(cands) != 3 {
		t.Fatalf("design: %d %v", len(cands), err)
	}
	for _, b := range f.bodies {
		if b["references"] != nil || !strings.Contains(b["text_prompt"].(string), "（中年男性，嗓音低沉沙哑，台湾口音）说道：“你好”") {
			t.Fatalf("design request: %v", b)
		}
	}
	for _, c := range cands {
		if !strings.HasPrefix(c.ProviderVoiceID, "ref:") || c.Preview == nil {
			t.Fatalf("candidate: %+v", c)
		}
	}
	if len(refs.data) != 3 {
		t.Fatalf("each candidate keeps its recording: %d", len(refs.data))
	}
}
