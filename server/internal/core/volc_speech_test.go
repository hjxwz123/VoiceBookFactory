package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"voicebook/internal/ai/gen"
	"voicebook/internal/ai/tts"
	"voicebook/internal/audio"
	"voicebook/internal/model"
	"voicebook/internal/storage"
)

// Every service here is a fake on 127.0.0.1; no real Qianwen or Volcengine account is used.

func speechTestApp(t *testing.T) (*App, *model.User) {
	t.Helper()
	a, u := testApp(t)
	a.Cfg.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	a.Cfg.PublicURL = "http://localhost:18080"
	a.FF = &audio.FF{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	local, err := storage.NewLocal(t.TempDir(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	a.Store = storage.NewStore(local)
	return a, u
}

func toneWAV(seconds float64) []byte {
	p := &audio.PCM{Rate: 44100, Channels: 1, Data: make([]float32, int(44100*seconds))}
	for i := range p.Data {
		p.Data[i] = float32((i*7)%200)/800 - 0.1
	}
	var b bytes.Buffer
	audio.EncodeWAV16(&b, p)
	return b.Bytes()
}

// fakeVolcCreate answers /api/v3/tts/create with a short WAV (or MP3 bytes for sound effects).
func fakeVolcCreate(t *testing.T) (*httptest.Server, func() []map[string]any) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/tts/create" || r.Header.Get("X-Api-Key") != "vk" {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":45000010,"message":"invalid api key"}`))
			return
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if body["text_prompt"] == "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"code":45001001,"message":"text_prompt is required"}`))
			return
		}
		data := toneWAV(1.5)
		json.NewEncoder(w).Encode(map[string]any{"audio": base64.StdEncoding.EncodeToString(data), "duration": 1.5, "original_duration": 1.5})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any{}, bodies...)
	}
}

func TestPublishTempAndReferenceStore(t *testing.T) {
	a, _ := speechTestApp(t)
	src := filepath.Join(t.TempDir(), "clip.mp3")
	os.WriteFile(src, []byte("mp3-bytes"), 0o644)
	link, cleanup, err := a.publishTemp(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	if !strings.HasPrefix(link, "http://localhost:18080/files/tmp/share/") || !strings.HasSuffix(u.Path, ".mp3") {
		t.Fatalf("link: %s", link)
	}
	key := strings.TrimPrefix(u.Path, "/files/")
	q := u.Query()
	if !a.Store.Verify(key, q.Get("e"), q.Get("dl"), q.Get("s")) {
		t.Fatal("the link must carry a valid signature")
	}
	if b, _ := a.Store.ReadAll(key); string(b) != "mp3-bytes" {
		t.Fatal("published file missing")
	}
	cleanup()
	if a.Store.Local.Exists(key) {
		t.Fatal("cleanup must remove the published file")
	}

	refs := a.refStore()
	id, err := refs.PutRef(context.Background(), "clone-abc/../x", []byte("RIFF"))
	if err != nil || !strings.HasPrefix(id, "voice-refs/clone-abc----x-") {
		t.Fatalf("put: %s %v", id, err)
	}
	if b, err := refs.GetRef(context.Background(), id); err != nil || string(b) != "RIFF" {
		t.Fatalf("get: %v", err)
	}
	if _, err := refs.GetRef(context.Background(), "books/secret.wav"); err == nil {
		t.Fatal("only reference recordings can be read")
	}
	refs.DeleteRef(context.Background(), id)
	if _, err := refs.GetRef(context.Background(), id); err == nil {
		t.Fatal("deleted reference still readable")
	}
}

// The Volcengine voices are seeded into the library; a cloned voice keeps its sample on this site and every
// synthesis sends it as the reference recording; the account test generates nothing.
func TestVolcengineVoicesCloneAndCheck(t *testing.T) {
	a, _ := speechTestApp(t)
	a.seedVolcCatalog()
	var n int64
	a.DB.Model(&model.Voice{}).Where("provider = ? AND scope = ?", "volcengine_audio", model.ScopeSystem).Count(&n)
	if n < 400 {
		t.Fatalf("seeded %d voices", n)
	}
	a.seedVolcCatalog()
	var again int64
	a.DB.Model(&model.Voice{}).Where("provider = ?", "volcengine_audio").Count(&again)
	if again != n {
		t.Fatal("seeding twice must not duplicate voices")
	}
	srv, bodies := fakeVolcCreate(t)
	acc := model.ProviderAccount{Kind: model.KindTTS, Provider: "volcengine_audio", Scope: "platform", Name: "火山 本机模拟", BaseURL: srv.URL, Model: "seed-audio-1.0", CredentialsEnc: a.Encrypt("vk"), Enabled: true, Priority: 1000, Config: model.EmptyMap()}
	a.DB.Create(&acc)
	msg, err := a.TestAccount(context.Background(), &acc)
	if err != nil || !strings.Contains(msg, "不产生费用") {
		t.Fatalf("test account: %q %v", msg, err)
	}
	if b := bodies(); len(b) != 1 || b[0]["text_prompt"] != "" {
		t.Fatalf("the account test sends one empty prompt only: %v", b)
	}
	p, err := tts.New(a.ttsAccount(&acc))
	if err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join(t.TempDir(), "merged.wav")
	os.WriteFile(sample, toneWAV(12), 0o644)
	res, err := p.(tts.VoiceCloner).CloneVoice(context.Background(), tts.CloneRequest{MergedPath: sample, Seed: "voice1"})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(res.ProviderVoiceID, "ref:")
	if !a.Store.Exists(key) {
		t.Fatalf("reference recording not stored: %s", key)
	}
	if _, err := p.Synthesize(context.Background(), tts.SynthesizeRequest{Text: "你好", ProviderVoiceID: res.ProviderVoiceID}); err != nil {
		t.Fatal(err)
	}
	last := bodies()[len(bodies())-1]
	stored, _ := a.Store.ReadAll(key)
	if last["references"].([]any)[0].(map[string]any)["audio_data"] != base64.StdEncoding.EncodeToString(stored) {
		t.Fatal("the stored sample must be sent as the reference")
	}
	if !tts.BillsByDuration(p) {
		t.Fatal("the cost of audio generation follows the audio length")
	}
}

// A sound effect can use library audio as "提示音": the clips go to the service as reference recordings.
func TestVolcengineSoundEffectWithReferences(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	a, u := speechTestApp(t)
	srv, bodies := fakeVolcCreate(t)
	acc := model.ProviderAccount{Kind: model.KindSFX, Provider: "volcengine_audio", Scope: "platform", Name: "火山音效 本机模拟", BaseURL: srv.URL, CredentialsEnc: a.Encrypt("vk"), Enabled: true, Priority: 1000, CostPerUnit: 0.6, Config: model.EmptyMap()}
	a.DB.Create(&acc)
	a.Grant(u.PersonalStudio, 100000, "grant", nil, "测试", "")
	startQueue(t, a)
	x, _ := a.StudioAccess(u, u.PersonalStudio)
	opts := a.GenOptions(u.PersonalStudio)["sfx"].(map[string]any)
	if opts["references"] != 3 || opts["maxSeconds"] != gen.VolcSFXMaxSeconds {
		t.Fatalf("options: %v", opts)
	}
	ref, err := a.SaveAssetBytes(context.Background(), AssetInput{StudioID: u.PersonalStudio, Kind: model.AssetSFX, Name: "雨声", Ext: "wav", Origin: "upload", CreatedBy: u.ID}, toneWAV(40))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := a.SaveAssetBytes(context.Background(), AssetInput{StudioID: "another-studio", Kind: model.AssetSFX, Name: "别人的", Ext: "wav", Origin: "upload"}, toneWAV(1))
	if _, err := a.EnqueueGenerate(x, GenRequest{Kind: model.KindSFX, Prompt: "雷声", References: []string{other.ID}}); err == nil {
		t.Fatal("another studio's asset cannot be a reference")
	}
	j, err := a.EnqueueGenerate(x, GenRequest{Kind: model.KindSFX, Prompt: "在 @音频1 的雨声里加入远处的雷声", Count: 1, DurationMs: 20000, References: []string{ref.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, a, j.ID); done.Status != model.JobSucceeded {
		t.Fatalf("job %s: %s", done.Status, done.Error)
	}
	b := bodies()
	if len(b) != 1 {
		t.Fatalf("requests: %d", len(b))
	}
	prompt := b[0]["text_prompt"].(string)
	if !strings.HasPrefix(prompt, "在 @音频1 的雨声里加入远处的雷声。") || !strings.Contains(prompt, "总时长约 20 秒") {
		t.Fatalf("prompt: %s", prompt)
	}
	clip, _ := base64.StdEncoding.DecodeString(b[0]["references"].([]any)[0].(map[string]any)["audio_data"].(string))
	pcm, err := audio.DecodeWAV(bytes.NewReader(clip))
	if err != nil || pcm.DurationMs() > 30000 || pcm.DurationMs() < 25000 {
		t.Fatalf("the reference is cut to 30 s of WAV: %v ms %v", pcm.DurationMs(), err)
	}
	var assets []model.Asset
	a.DB.Where("studio_id = ? AND kind = ? AND origin = ?", u.PersonalStudio, model.AssetSFX, "ai").Find(&assets)
	if len(assets) != 1 || assets[0].License != "provider_terms" {
		t.Fatalf("assets: %+v", assets)
	}
}

// Qianwen recognition takes 5 minutes per request: a long chapter is cut into pieces and the word times are
// shifted back onto the chapter's timeline.
func TestLongAudioIsCutForQwenRecognition(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	a, u := speechTestApp(t)
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		fmt.Fprintf(w, `{"output":{"text":"第%d段","sentence":{"begin_time":0,"end_time":1500,"text":"第%d段","words":[{"text":"第%d段","begin_time":1000,"end_time":1500,"punctuation":""}]}},"request_id":"r"}`, n, n, n)
	}))
	t.Cleanup(srv.Close)
	acc := model.ProviderAccount{Kind: model.KindASR, Provider: "qwen", Scope: "platform", Name: "千问识别 本机模拟", BaseURL: srv.URL, Model: "qwen-audio-3.1-asr-flash", CredentialsEnc: a.Encrypt("sk"), Enabled: true, Priority: 1000, Config: model.EmptyMap()}
	a.DB.Create(&acc)
	long := filepath.Join(t.TempDir(), "chapter.wav")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "600", long).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	prov, _, err := a.ASRFor(u.PersonalStudio)
	if err != nil {
		t.Fatal(err)
	}
	words, text, err := a.transcribeLong(context.Background(), prov, long, "zh-CN", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(words) != 3 || text != "第1段第2段第3段" {
		t.Fatalf("10 minutes in 4.5-minute pieces: %d calls, %d words, %q", calls, len(words), text)
	}
	if words[1].StartMs != 270_000+1000 || words[2].StartMs != 540_000+1000 {
		t.Fatalf("offsets: %+v", words)
	}
	// a 2'10" generated line as 44.1 kHz WAV is over Qianwen's 10 MB: it is re-encoded instead of skipped
	line := filepath.Join(t.TempDir(), "take.wav")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "130", "-c:a", "pcm_s16le", line).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	if st, _ := os.Stat(line); st.Size() <= 10<<20 {
		t.Fatalf("test clip should be over 10 MB: %d", st.Size())
	}
	calls = 0
	res, err := a.transcribeClip(context.Background(), prov, line, "zh-CN", 130_000)
	if err != nil || calls != 1 || res.Text != "第1段" {
		t.Fatalf("large line: %+v %v (%d calls)", res, err, calls)
	}
	// a short line goes as it is
	short := filepath.Join(t.TempDir(), "short.wav")
	os.WriteFile(short, toneWAV(2), 0o644)
	calls = 0
	if res, err := a.transcribeClip(context.Background(), prov, short, "zh-CN", 2000); err != nil || calls != 1 || res.Text != "第1段" {
		t.Fatalf("short line: %+v %v", res, err)
	}
}

// A line voiced by Volcengine keeps the prompt that was sent on its take, also when the audio is reused.
func TestVolcenginePromptSavedOnTake(t *testing.T) {
	a, u := speechTestApp(t)
	a.seedVolcCatalog()
	srv, bodies := fakeVolcCreate(t)
	acc := model.ProviderAccount{Kind: model.KindTTS, Provider: "volcengine_audio", Scope: "platform", Name: "火山 本机模拟", BaseURL: srv.URL, Model: "seed-audio-1.0", CredentialsEnc: a.Encrypt("vk"), Enabled: true, Priority: 1000, Config: model.EmptyMap()}
	a.DB.Create(&acc)
	var voice model.Voice
	if err := a.DB.First(&voice, "provider = ? AND provider_voice_id = ?", "volcengine_audio", "zh_female_vv_uranus_bigtts").Error; err != nil {
		t.Fatal(err)
	}
	book := model.Book{StudioID: u.PersonalStudio, Title: "风", Language: "zh-CN"}
	a.DB.Create(&book)
	ch := model.Chapter{BookID: book.ID, Title: "第一章"}
	a.DB.Create(&ch)
	char := model.Character{BookID: book.ID, Name: "林黛玉"}
	a.DB.Create(&char)
	seg := model.Segment{ChapterID: ch.ID, BookID: book.ID, CharacterID: char.ID, VoiceID: voice.ID, Type: "dialogue", Text: "你真傻。我早就知道了。", Emotion: "sad",
		Cues: model.JSON[[]model.Cue]{V: []model.Cue{{At: 4, Tag: "giggles"}, {At: 4, Tag: "crying"}}}}
	a.DB.Create(&seg)
	chars := map[string]*model.Character{char.ID: &char}
	out, err := a.synthesizeSegment(context.Background(), &book, &seg, chars, nil, u.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "纯人声，无背景音乐和音效。（悲伤）说道：“你真傻。”轻笑了一声，接着（带着哭腔）说：“我早就知道了。”"
	if b := bodies(); len(b) != 1 || b[0]["text_prompt"] != want {
		t.Fatalf("sent: %v", b)
	}
	var take model.Take
	a.DB.First(&take, "id = ?", out.Take.ID)
	if got := fmt.Sprint(take.Params.V["providerPrompts"]); got != "["+want+"]" {
		t.Fatalf("saved prompt: %s", got)
	}
	// a second line with the same text and settings reuses the audio, and the prompt comes along
	seg2 := seg
	seg2.ID = ""
	a.DB.Create(&seg2)
	again, err := a.synthesizeSegment(context.Background(), &book, &seg2, chars, nil, u.ID, "", false)
	if err != nil || !again.Cached {
		t.Fatalf("reuse: %+v %v", again, err)
	}
	var reused model.Take
	a.DB.First(&reused, "id = ?", again.Take.ID)
	if got := fmt.Sprint(reused.Params.V["providerPrompts"]); got != "["+want+"]" || len(bodies()) != 1 {
		t.Fatalf("reused take prompt: %s (%d requests)", got, len(bodies()))
	}
}
