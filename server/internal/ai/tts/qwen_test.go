package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"voicebook/internal/audio"
)

func TestNumericPinyin(t *testing.T) {
	cases := map[string]string{"xíng": "xing2", "háng": "hang2", "lǜ": "lv4", "de": "de5", "dian3 dang4": "dian3 dang4", "zhōng guó": "zhong1 guo2", "nǚ": "nv3"}
	for in, want := range cases {
		if got := NumericPinyin(in); got != want {
			t.Errorf("NumericPinyin(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCatalogBindsVoicesToModels(t *testing.T) {
	if len(QwenCatalog()) < 300 {
		t.Fatalf("catalog too small: %d", len(QwenCatalog()))
	}
	if QwenLookup("cosyvoice-v3-flash", "longanyang") == nil || QwenLookup("cosyvoice-v2", "longanyang") != nil {
		t.Fatal("voices must be looked up per model")
	}
	for _, m := range QwenModels {
		if QwenLookup(m, qwenDefaultVoice[m]) == nil {
			t.Errorf("default voice %s missing for %s", qwenDefaultVoice[m], m)
		}
	}
}

func TestBuildQwenAudioInput(t *testing.T) {
	in, err := BuildQwenInput("qwen-audio-3.1-tts-flash", SynthesizeRequest{
		Text: "银行在前面行驶。", ProviderVoiceID: "longanhuan_v3.1", Emotion: "sad", Intensity: 3, Speed: 1.2, Pitch: 12, Tags: []string{"叹气"},
		Instruction: "慢慢说", Prons: []Pron{{Offset: 1, Char: "行", Pinyin: "háng"}}, Language: "zh-CN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(in.Text, "[sad][sighing]银行") || in.EnableSSML {
		t.Errorf("expected inline tags without SSML, got %q ssml=%v", in.Text, in.EnableSSML)
	}
	if in.Rate != 1.2 || in.Pitch != 2 || in.LanguageHints != nil {
		t.Errorf("prosody/hints wrong: %+v", in)
	}
	if !strings.Contains(in.Instruction, "慢慢说") || !strings.Contains(in.Instruction, "强烈") {
		t.Errorf("instruction %q", in.Instruction)
	}
	pr := in.HotFix["pronunciation"].([]map[string]string)
	if len(pr) != 1 || pr[0]["行"] != "hang2" {
		t.Errorf("hot_fix %v", in.HotFix)
	}
}

func TestConflictingReadingsUseSSML(t *testing.T) {
	in, err := BuildQwenInput("qwen-audio-3.1-tts-flash", SynthesizeRequest{
		Text: "银行在行驶", ProviderVoiceID: "longanhuan_v3.1", Emotion: "angry",
		Prons: []Pron{{Offset: 1, Char: "行", Pinyin: "háng"}, {Offset: 3, Char: "行", Pinyin: "xíng"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !in.EnableSSML || in.HotFix != nil {
		t.Fatalf("expected SSML: %+v", in)
	}
	want := `<speak>银<phoneme alphabet="py" ph="hang2">行</phoneme>在<phoneme alphabet="py" ph="xing2">行</phoneme>驶</speak>`
	if in.Text != want {
		t.Errorf("ssml %q", in.Text)
	}
	if !strings.Contains(in.Instruction, "愤怒") {
		t.Errorf("emotion should move into instruction under SSML: %q", in.Instruction)
	}
}

func TestCosyVoiceSystemVoiceDropsInstruction(t *testing.T) {
	in, err := BuildQwenInput("cosyvoice-v2", SynthesizeRequest{Text: "你好<世界>", ProviderVoiceID: "longxiaochun_v2", Emotion: "happy", Instruction: "开心点",
		Prons: []Pron{{Offset: 0, Char: "你", Pinyin: "nǐ"}}, Language: "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Instruction != "" || strings.Contains(in.Text, "[") {
		t.Errorf("system CosyVoice voices take no instruction/tags: %+v", in)
	}
	if !in.EnableSSML || !strings.Contains(in.Text, "&lt;世界&gt;") {
		t.Errorf("cosyvoice-v2 has no hot_fix; expected escaped SSML, got %q", in.Text)
	}
	if len(in.LanguageHints) != 1 || in.LanguageHints[0] != "en" {
		t.Errorf("hints %v", in.LanguageHints)
	}
}

func TestQwenSynthesizeRoundTrip(t *testing.T) {
	var got map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services/audio/tts/SpeechSynthesizer":
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				w.WriteHeader(401)
				w.Write([]byte(`{"code":"InvalidApiKey","message":"bad key"}`))
				return
			}
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"request_id":"r1","output":{"finish_reason":"stop","audio":{"url":"` + srv.URL + `/audio.wav"}}}`))
		case "/audio.wav":
			// streaming-style header: placeholder sizes that the client must correct
			w.Write([]byte("RIFF\xbf\xff\xff\x7fWAVEdata\x9b\xff\xff\x7f\x01\x02\x03\x04"))
		}
	}))
	defer srv.Close()
	p, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "sk-test", Model: "cosyvoice-v3-flash"})
	a, err := p.Synthesize(context.Background(), SynthesizeRequest{Text: "测试", Speed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Data) != "RIFF\x10\x00\x00\x00WAVEdata\x04\x00\x00\x00\x01\x02\x03\x04" || a.Format != "wav" {
		t.Fatalf("audio %+v", a)
	}
	in := got["input"].(map[string]any)
	if got["model"] != "cosyvoice-v3-flash" || in["voice"] != "longanyang" || in["format"] != "wav" {
		t.Errorf("request %v", got)
	}
	bad, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "wrong"})
	if _, err := bad.Synthesize(context.Background(), SynthesizeRequest{Text: "测试"}); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Errorf("expected friendly key error, got %v", err)
	}
}

func TestPositionalCues(t *testing.T) {
	// "你这个人啊，真是拿你没办法。" — laugh after "啊", sarcastic from the start
	in, err := BuildQwenInput("qwen-audio-3.1-tts-flash", SynthesizeRequest{
		Text: "你这个人啊，真是拿你没办法。", ProviderVoiceID: "longanhuan_v3.1", Emotion: "happy",
		Cues: []Cue{{At: 5, Tag: "laughing"}, {At: 0, Tag: "讽刺"}, {At: 99, Tag: "sighing"}, {At: 2, Tag: "not-a-tag"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "[sarcastic]你这个人啊[laughing]，真是拿你没办法。[sighing]"
	if in.Text != want {
		t.Errorf("got %q want %q (style cue at start replaces the emotion tag, unknown tags dropped, offsets clamped)", in.Text, want)
	}
	// CosyVoice system voices ignore cues entirely
	cv, _ := BuildQwenInput("cosyvoice-v3-flash", SynthesizeRequest{Text: "你好啊", ProviderVoiceID: "longxiu_v3", Cues: []Cue{{At: 2, Tag: "laughing"}}})
	if cv.Text != "你好啊" || cv.Instruction != "" {
		t.Errorf("cosyvoice should not receive cues: %+v", cv)
	}
	// under SSML (conflicting readings) style cues become direction, sounds are dropped
	ss, _ := BuildQwenInput("qwen-audio-3.1-tts-flash", SynthesizeRequest{Text: "银行在行驶", ProviderVoiceID: "longanhuan_v3.1",
		Prons: []Pron{{Offset: 1, Char: "行", Pinyin: "háng"}, {Offset: 3, Char: "行", Pinyin: "xíng"}}, Cues: []Cue{{At: 0, Tag: "very slowly"}, {At: 2, Tag: "gasp"}}})
	if !ss.EnableSSML || strings.Contains(ss.Text, "[") || !strings.Contains(ss.Instruction, "放慢语速") {
		t.Errorf("ssml fallback: %+v", ss)
	}
}

func TestInsertCuesKeepsOrderAtSamePosition(t *testing.T) {
	got := InsertCues("好", []Cue{{At: 0, Tag: "sighing"}, {At: 0, Tag: "tired"}, {At: 1, Tag: "cough"}})
	if got != "[sighing][tired]好[cough]" {
		t.Errorf("got %q", got)
	}
}

func TestQwenClone(t *testing.T) {
	qwenClonePoll = 5 * time.Millisecond
	var (
		mu      sync.Mutex
		created map[string]any
		polls   int
		deleted []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/v1/services/audio/tts/customization" || r.Header.Get("Authorization") != "Bearer k" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model string         `json:"model"`
			Input map[string]any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "voice-enrollment" {
			t.Errorf("model = %q", body.Model)
		}
		switch body.Input["action"] {
		case "create_voice":
			created = body.Input
			fmt.Fprintf(w, `{"output":{"voice_id":"%s-%s-abc"},"usage":{"count":1},"request_id":"r1"}`, body.Input["target_model"], body.Input["prefix"])
		case "query_voice":
			polls++
			st := "DEPLOYING"
			if polls >= 3 {
				st = "OK"
			}
			fmt.Fprintf(w, `{"output":{"status":"%s","target_model":"x"},"request_id":"r2"}`, st)
		case "delete_voice":
			deleted = append(deleted, body.Input["voice_id"].(string))
			if body.Input["voice_id"] == "gone" {
				w.WriteHeader(400)
				w.Write([]byte(`{"code":"BadRequest.ResourceNotExist","message":"The Required resource not exist.","request_id":"r3"}`))
				return
			}
			w.Write([]byte(`{"output":{},"request_id":"r3"}`))
		}
	}))
	defer srv.Close()
	sample := t.TempDir() + "/s.wav"
	if err := audio.WriteWAVFile(sample, &audio.PCM{Rate: 32000, Channels: 1, Data: make([]float32, 32000*14)}); err != nil {
		t.Fatal(err)
	}
	p, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "k"})
	caps := p.Capabilities()
	if !caps.QuickClone || caps.CloneMaxSeconds != 60 || !caps.ServerDenoise || caps.CloneExpiresUnused {
		t.Fatalf("caps = %+v", caps)
	}
	cl := p.(VoiceCloner)
	res, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: sample, Seed: "01a1afca-7752-77bb", Denoise: true, Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ready" || res.Model != QwenDefaultCloneModel || res.PriceTier != "premium" || res.ProviderVoiceID != "qwen-audio-3.1-tts-plus-vb01a1afca-abc" || polls != 3 {
		t.Fatalf("res = %+v polls=%d", res, polls)
	}
	if a, _ := created["audio"].(string); !strings.HasPrefix(a, "data:audio/wav;base64,UklGR") { // "RIFF"
		t.Fatalf("audio = %.40s", a)
	}
	if created["target_model"] != QwenDefaultCloneModel || created["prefix"] != "vb01a1afca" || created["enable_preprocess"] != true ||
		created["enable_volume_normalization"] != "true" || created["max_prompt_audio_length"].(float64) != 14 || fmt.Sprint(created["language_hints"]) != "[zh]" {
		t.Fatalf("create input = %v", created)
	}
	// configured clone model; CosyVoice v2 takes none of the optional parameters
	p2, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "k", Config: map[string]any{"cloneModel": "cosyvoice-v2"}})
	polls = 0
	res, err = p2.(VoiceCloner).CloneVoice(context.Background(), CloneRequest{MergedPath: sample, Seed: "x"})
	if err != nil || res.Model != "cosyvoice-v2" || res.PriceTier != "economy" || created["enable_preprocess"] != nil || created["language_hints"] != nil {
		t.Fatalf("v2: %v %+v %v", err, res, created)
	}
	if p2.Capabilities().ServerDenoise {
		t.Fatal("cosyvoice-v2 has no server-side preprocessing")
	}
	if err := cl.DeleteVoice(context.Background(), "v1"); err != nil {
		t.Fatal(err)
	}
	if err := cl.DeleteVoice(context.Background(), "gone"); err != nil {
		t.Fatalf("deleting a missing voice should succeed: %v", err)
	}
	// local checks before anything is sent
	short := t.TempDir() + "/short.wav"
	audio.WriteWAVFile(short, &audio.PCM{Rate: 32000, Channels: 1, Data: make([]float32, 32000*3)})
	if _, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: short, Seed: "x"}); err == nil || !strings.Contains(err.Error(), "至少 5 秒") {
		t.Fatalf("short err = %v", err)
	}
	if _, err := cl.CloneVoice(context.Background(), CloneRequest{MergedPath: t.TempDir() + "/a.webm", Seed: "x"}); err == nil || !strings.Contains(err.Error(), "wav、mp3、m4a") {
		t.Fatalf("format err = %v", err)
	}
	if got := QwenClonePrefix("我的-声音_ABCdef123456"); got != "vbabcdef12" {
		t.Fatalf("prefix = %q", got)
	}
}

func TestQwenCloneReviewAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		switch body.Input["action"] {
		case "create_voice":
			w.WriteHeader(400)
			w.Write([]byte(`{"code":"Audio.AudioShortError","message":"valid audio too short!","request_id":"r"}`))
		case "query_voice":
			w.Write([]byte(`{"output":{"status":"UNDEPLOYED"},"request_id":"r"}`))
		}
	}))
	defer srv.Close()
	sample := t.TempDir() + "/s.wav"
	audio.WriteWAVFile(sample, &audio.PCM{Rate: 24000, Channels: 1, Data: make([]float32, 24000*12)})
	p, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "k"})
	_, err := p.(VoiceCloner).CloneVoice(context.Background(), CloneRequest{MergedPath: sample, Seed: "abc"})
	if err == nil || !strings.Contains(err.Error(), "有效人声太短") {
		t.Fatalf("err = %v", err)
	}
	st, err := p.(VoiceCloner).CloneStatus(context.Background(), "v")
	if err != nil || st.Status != "failed" || !strings.Contains(st.Message, "审核未通过") {
		t.Fatalf("status = %+v %v", st, err)
	}
}

func TestQwenDesignTextRules(t *testing.T) {
	long := strings.Repeat("沉稳的中年男声，低沉浑厚，", 60)
	if p := QwenDesignPrompt(long); len([]rune(p)) > 500 || !strings.HasSuffix(p, "，") {
		t.Fatalf("prompt not cut at a boundary: %d runes, %q", len([]rune(p)), p[len(p)-6:])
	}
	cases := []struct{ in, want, lang string }{
		{"", qwenDesignPreviewDefault, "zh"},
		{"太短了", qwenDesignPreviewDefault, "zh"},
		{"こんにちは、今日はいい天気ですね。散歩に行きましょう。", qwenDesignPreviewDefault, "zh"},
		{"各位听众朋友们，大家好，欢迎收听本期节目。", "各位听众朋友们，大家好，欢迎收听本期节目。", "zh"},
		{"Good evening, and welcome to tonight's story.", "Good evening, and welcome to tonight's story.", "en"},
	}
	for _, c := range cases {
		if got, lang := QwenDesignPreview(c.in); got != c.want || lang != c.lang {
			t.Errorf("preview(%q) = %q,%s", c.in, got, lang)
		}
	}
	if m := QwenDesignModelFor(map[string]any{"cloneModel": "cosyvoice-v2"}); m != QwenDefaultCloneModel {
		t.Fatalf("v2 clone model must not be used for design, got %s", m)
	}
	if m := QwenDesignModelFor(map[string]any{"cloneModel": "cosyvoice-v3.5-plus"}); m != "cosyvoice-v3.5-plus" {
		t.Fatalf("design follows the clone model, got %s", m)
	}
	if m := QwenDesignModelFor(map[string]any{"cloneModel": "cosyvoice-v3.5-plus", "designModel": "qwen-audio-3.0-tts-flash"}); m != "qwen-audio-3.0-tts-flash" {
		t.Fatalf("designModel wins, got %s", m)
	}
}

func TestQwenDesignVoice(t *testing.T) {
	qwenClonePoll = 5 * time.Millisecond
	var buf bytes.Buffer
	audio.EncodeWAV16(&buf, &audio.PCM{Rate: 48000, Channels: 1, Data: make([]float32, 4800)})
	preview := base64.StdEncoding.EncodeToString(buf.Bytes())
	var (
		mu      sync.Mutex
		creates []map[string]any
		params  map[string]any
		deleted []string
	)
	reject := "" // voice id that fails review
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input      map[string]any `json:"input"`
			Parameters map[string]any `json:"parameters"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		switch body.Input["action"] {
		case "create_voice":
			creates = append(creates, body.Input)
			params = body.Parameters
			fmt.Fprintf(w, `{"output":{"voice_id":"%s-vd-%s-%d","target_model":"%s","preview_audio":{"data":"%s","sample_rate":48000,"response_format":"wav"}},"usage":{"count":1},"request_id":"r"}`,
				body.Input["target_model"], body.Input["prefix"], len(creates), body.Input["target_model"], preview)
		case "query_voice":
			st := "OK"
			if reject != "" && strings.Contains(body.Input["voice_id"].(string), reject) {
				st = "UNDEPLOYED"
			}
			fmt.Fprintf(w, `{"output":{"status":"%s"},"request_id":"r"}`, st)
		case "delete_voice":
			deleted = append(deleted, body.Input["voice_id"].(string))
			w.Write([]byte(`{"output":{},"request_id":"r"}`))
		}
	}))
	defer srv.Close()
	p, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "k", Config: map[string]any{"designModel": "cosyvoice-v3.5-plus"}})
	if !p.Capabilities().VoiceDesign || p.Capabilities().DesignPromptMax != 500 {
		t.Fatalf("caps = %+v", p.Capabilities())
	}
	d := p.(VoiceDesigner)
	cands, err := d.DesignVoice(context.Background(), DesignRequest{Description: "温柔知性的女性，30 岁左右，语调平和，适合有声书朗读", PreviewText: "Hi", Count: 3, Seed: "design01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 || len(creates) != 3 {
		t.Fatalf("got %d candidates from %d creates", len(cands), len(creates))
	}
	for _, c := range cands {
		if c.Model != "cosyvoice-v3.5-plus" || c.PriceTier != "standard" || c.Preview == nil || !bytes.HasPrefix(c.Preview.Data, []byte("RIFF")) {
			t.Fatalf("candidate = %+v", c)
		}
	}
	in := creates[0]
	if in["voice_prompt"] != "温柔知性的女性，30 岁左右，语调平和，适合有声书朗读" || in["preview_text"] != qwenDesignPreviewDefault || fmt.Sprint(in["language_hints"]) != "[zh]" ||
		in["target_model"] != "cosyvoice-v3.5-plus" || len(in["prefix"].(string)) != 10 || !strings.HasPrefix(in["prefix"].(string), "vbdesign0") || in["audio"] != nil {
		t.Fatalf("create input = %v", in)
	}
	if params["sample_rate"].(float64) != 48000 || params["response_format"] != "wav" {
		t.Fatalf("parameters = %v", params)
	}
	// one candidate fails review: it is deleted and dropped, the others survive
	creates = nil
	reject = "-vd-vbdesign01-" // the second candidate (prefix "vbdesign0" + index 1)
	cands, err = d.DesignVoice(context.Background(), DesignRequest{Description: "活泼的少年，声音清亮", Count: 3, Seed: "design02"})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotDeleted := append([]string{}, deleted...)
	mu.Unlock()
	if len(cands) != 2 || len(gotDeleted) != 1 || !strings.Contains(gotDeleted[0], reject) {
		ids := []string{}
		for _, c := range cands {
			ids = append(ids, c.ProviderVoiceID)
		}
		t.Fatalf("candidates=%v deleted=%v", ids, gotDeleted)
	}
}

func TestQwenDesignAllRejected(t *testing.T) {
	qwenClonePoll = 5 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":"DataInspectionFailed","message":"Input data may contain inappropriate content.","request_id":"r"}`))
	}))
	defer srv.Close()
	p, _ := New(Account{Provider: "qwen", BaseURL: srv.URL, APIKey: "k"})
	if _, err := p.(VoiceDesigner).DesignVoice(context.Background(), DesignRequest{Description: "某种声音描述", Count: 3, Seed: "x"}); err == nil {
		t.Fatal("expected an error when every candidate fails")
	}
}
