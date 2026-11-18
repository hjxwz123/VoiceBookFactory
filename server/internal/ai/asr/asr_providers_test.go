package asr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These fakes speak the Qianwen and Volcengine protocols on 127.0.0.1; nothing reaches the real services.

func writeAudio(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	os.WriteFile(p, make([]byte, size), 0o644)
	return p
}

func TestQwenASRTranscribe(t *testing.T) {
	var got map[string]any
	var auth, sse string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			w.WriteHeader(404)
			return
		}
		auth, sse = r.Header.Get("Authorization"), r.Header.Get("X-DashScope-SSE")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"output":{"sentence":{"begin_time":760,"end_time":3800,"text":"你好，世界。","words":[
			{"begin_time":760,"end_time":1040,"text":"你好","punctuation":"，"},{"begin_time":1040,"end_time":3800,"text":"世界","punctuation":"。"}]},
			"text":"你好，世界。"},"usage":{"duration":4},"request_id":"rid-1"}`))
	}))
	defer srv.Close()
	p, err := New(Account{Provider: "qwen", BaseURL: srv.URL + "/api/v1", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	path := writeAudio(t, "clip.mp3", 2048)
	res, err := p.Transcribe(context.Background(), path, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-test" || sse != "disable" {
		t.Fatalf("headers: %q %q", auth, sse)
	}
	if got["model"] != QwenASRDefaultModel {
		t.Fatalf("model: %v", got["model"])
	}
	content := got["input"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	data := content["input_audio"].(map[string]any)["data"].(string)
	if content["type"] != "input_audio" || data != "data:audio/mpeg;base64,"+base64.StdEncoding.EncodeToString(make([]byte, 2048)) {
		t.Fatalf("audio part: %v", content["type"])
	}
	params := got["parameters"].(map[string]any)
	if params["format"] != "mp3" || params["language_hints"].([]any)[0] != "zh" {
		t.Fatalf("parameters: %v", params)
	}
	if res.Text != "你好，世界。" || len(res.Words) != 2 || res.Words[0].Text != "你好，" || res.Words[1].StartMs != 1040 || res.Words[1].EndMs != 3800 {
		t.Fatalf("result: %+v", res)
	}
	if ChunkMs(p, 9*60*1000) != 270_000 {
		t.Fatal("long audio must be cut into pieces under 5 minutes")
	}
}

func TestQwenASRErrorsAndRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key provided.","request_id":"r1"}`))
			return
		}
		if n == 1 {
			w.WriteHeader(429)
			w.Write([]byte(`{"code":"Throttling.RateQuota","message":"slow down"}`))
			return
		}
		w.Write([]byte(`{"output":{"text":"好"},"request_id":"r2"}`))
	}))
	defer srv.Close()
	path := writeAudio(t, "a.wav", 100)
	p := newQwen(Account{BaseURL: srv.URL, APIKey: "k"})
	p.retryWait = time.Millisecond
	res, err := p.Transcribe(context.Background(), path, "")
	if err != nil || res.Text != "好" || calls.Load() != 2 {
		t.Fatalf("a rate-limited request is retried: %v %v %d", res, err, calls.Load())
	}
	bad := newQwen(Account{BaseURL: srv.URL, APIKey: "bad"})
	if _, err := bad.Transcribe(context.Background(), path, ""); err == nil || !strings.Contains(err.Error(), "API Key 无效") || !strings.Contains(err.Error(), "r1") {
		t.Fatalf("bad key: %v", err)
	}
	big := writeAudio(t, "big.mp3", qwenASRMaxBytes+1)
	if _, err := p.Transcribe(context.Background(), big, ""); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Fatalf("too large: %v", err)
	}
}

func TestQwenASRCheckSendsNoAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "input_audio") {
			t.Error("the check must not upload audio")
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			w.Write([]byte(`{"code":"InvalidApiKey","message":"bad"}`))
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"code":"InvalidParameter","message":"messages must contain an input_audio"}`))
	}))
	defer srv.Close()
	msg, err := newQwen(Account{BaseURL: srv.URL, APIKey: "good"}).Check(context.Background())
	if err != nil || !strings.Contains(msg, "密钥有效") {
		t.Fatalf("good key: %q %v", msg, err)
	}
	if _, err := newQwen(Account{BaseURL: srv.URL, APIKey: "nope"}).Check(context.Background()); err == nil {
		t.Fatal("bad key accepted")
	}
}

// fakeVolcASR keeps tasks in memory and downloads the audio from the published link like the real service.
type fakeVolcASR struct {
	mu        sync.Mutex
	submitted map[string]map[string]any
	headers   http.Header
	polls     int
	fetched   int
	finish    string // status code reported once the task is done
	ownIDs    bool   // the submit reply names its own task_id, which the query must use
	asList    bool   // `result` comes back as a list
	hiccups   int    // "reading the result failed" answers before the result
}

func (f *fakeVolcASR) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.Header.Get("X-Api-Request-Id")
		f.headers = r.Header.Clone()
		if r.Header.Get("X-Api-Key") != "vk" {
			w.Header().Set("X-Api-Status-Code", "45000010")
			w.Header().Set("X-Api-Message", "invalid api key")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v3/auc/bigmodel/submit":
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &body)
			if f.submitted == nil {
				f.submitted = map[string]map[string]any{}
			}
			reply := `{}`
			if f.ownIDs {
				id = "srv-" + id
				reply = `{"task_id":"` + id + `"}`
			}
			f.submitted[id] = body
			link := body["audio"].(map[string]any)["url"].(string)
			f.mu.Unlock() // the link may point back at this server
			resp, err := http.Get(link)
			f.mu.Lock()
			if err == nil {
				if resp.StatusCode == 200 {
					f.fetched++
				}
				resp.Body.Close()
			}
			w.Header().Set("X-Api-Status-Code", "20000000")
			w.Header().Set("X-Api-Message", "OK")
			w.Write([]byte(reply))
		case "/api/v3/auc/bigmodel/query":
			if _, ok := f.submitted[id]; !ok {
				w.Header().Set("X-Api-Status-Code", "45000001")
				w.Header().Set("X-Api-Message", "task not found")
				w.Write([]byte(`{}`))
				return
			}
			f.polls++
			if f.polls < 2 {
				w.Header().Set("X-Api-Status-Code", "20000001")
				w.Write([]byte(`{}`))
				return
			}
			if f.hiccups > 0 {
				f.hiccups--
				w.Header().Set("X-Api-Status-Code", "55000001")
				w.Header().Set("X-Api-Message", "read result failed")
				w.Write([]byte(`{}`))
				return
			}
			code := f.finish
			if code == "" {
				code = "20000000"
			}
			if f.asList {
				w.Header().Set("X-Api-Status-Code", code)
				w.Write([]byte(`{"result":[{"text":"第一句。","utterances":[{"text":"第一句。","start_time":0,"end_time":900,"words":[{"text":"第一句","start_time":0,"end_time":900}]}]},
					{"text":"第二句。","utterances":[{"text":"第二句。","start_time":1000,"end_time":1900,"words":[{"text":"第二句","start_time":1000,"end_time":1900}]}]}]}`))
				return
			}
			w.Header().Set("X-Api-Status-Code", code)
			w.Header().Set("X-Tt-Logid", "lg-9")
			if code != "20000000" {
				w.Write([]byte(`{}`))
				return
			}
			w.Write([]byte(`{"audio_info":{"duration":2600},"result":{"text":"刚刚还在想你。","utterances":[{"text":"刚刚还在想你。","start_time":480,"end_time":2500,
				"words":[{"text":"刚","start_time":480,"end_time":600},{"text":"刚","start_time":680,"end_time":800},{"text":"还在想你","start_time":800,"end_time":2500}]}]}}`))
		default:
			w.WriteHeader(404)
		}
	})
}

func TestVolcASRSubmitAndQuery(t *testing.T) {
	f := &fakeVolcASR{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("audio")) }))
	defer files.Close()
	cleaned := false
	p := newVolc(Account{Provider: "volcengine", BaseURL: srv.URL, APIKey: "vk", Publish: func(ctx context.Context, path string) (string, func(), error) {
		return files.URL + "/files/tmp/share/x.mp3?s=sig", func() { cleaned = true }, nil
	}})
	p.poll = 5 * time.Millisecond
	path := writeAudio(t, "chapter.mp3", 500)
	res, err := p.Transcribe(context.Background(), path, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "刚刚还在想你。" || len(res.Words) != 3 || res.Words[2].StartMs != 800 || len(res.Segments) != 1 {
		t.Fatalf("result: %+v", res)
	}
	if !cleaned || f.fetched != 1 {
		t.Fatalf("the published file must be fetched once and removed afterwards: cleaned=%v fetched=%d", cleaned, f.fetched)
	}
	if f.headers.Get("X-Api-Resource-Id") != VolcASRDefaultResource {
		t.Fatalf("resource: %v", f.headers)
	}
	for _, body := range f.submitted {
		audio := body["audio"].(map[string]any)
		req := body["request"].(map[string]any)
		if audio["format"] != "mp3" || audio["language"] != nil || req["model_name"] != "bigmodel" || req["show_utterances"] != true {
			t.Fatalf("submit body: %v", body)
		}
	}
	// English sets the language; wav is accepted
	f.polls = 0
	if _, err := p.Transcribe(context.Background(), writeAudio(t, "a.wav", 10), "en-US"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, body := range f.submitted {
		if body["audio"].(map[string]any)["language"] == "en-US" {
			found = true
		}
	}
	if !found {
		t.Fatal("english language not sent")
	}
}

func TestVolcASRSilenceFailuresAndFormats(t *testing.T) {
	f := &fakeVolcASR{finish: "20000003"}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	pub := func(ctx context.Context, path string) (string, func(), error) {
		return srv.URL + "/nothing", func() {}, nil
	}
	p := newVolc(Account{BaseURL: srv.URL, APIKey: "vk", Publish: pub})
	p.poll = 5 * time.Millisecond
	res, err := p.Transcribe(context.Background(), writeAudio(t, "s.mp3", 10), "")
	if err != nil || res.Text != "" {
		t.Fatalf("silent audio is an empty result, not an error: %+v %v", res, err)
	}
	f.finish, f.polls = "45000151", 0
	if _, err := p.Transcribe(context.Background(), writeAudio(t, "s.mp3", 10), ""); err == nil || !strings.Contains(err.Error(), "格式") || !strings.Contains(err.Error(), "lg-9") {
		t.Fatalf("failed task: %v", err)
	}
	if _, err := p.Transcribe(context.Background(), writeAudio(t, "s.webm", 10), ""); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("webm: %v", err)
	}
	bad := newVolc(Account{BaseURL: srv.URL, APIKey: "nope", Publish: pub})
	if _, err := bad.Transcribe(context.Background(), writeAudio(t, "s.mp3", 10), ""); err == nil || !strings.Contains(err.Error(), "密钥") {
		t.Fatalf("bad key: %v", err)
	}
}

// What the submit and query documentation allows: the reply's task_id, a list-shaped result, a query that
// briefly fails to read the result, and audio with nothing in it.
func TestVolcASRDocumentedVariants(t *testing.T) {
	f := &fakeVolcASR{ownIDs: true, asList: true, hiccups: 2}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	p := newVolc(Account{BaseURL: srv.URL, APIKey: "vk", Publish: func(ctx context.Context, path string) (string, func(), error) {
		return srv.URL + "/nothing", func() {}, nil
	}})
	p.poll = 5 * time.Millisecond
	res, err := p.Transcribe(context.Background(), writeAudio(t, "a.mp3", 10), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "第一句。第二句。" || len(res.Words) != 2 || res.Words[1].StartMs != 1000 {
		t.Fatalf("list result: %+v", res)
	}
	f.asList, f.ownIDs, f.polls, f.finish = false, false, 0, "45000002"
	if res, err := p.Transcribe(context.Background(), writeAudio(t, "a.mp3", 10), ""); err != nil || res.Text != "" {
		t.Fatalf("empty audio is an empty result: %+v %v", res, err)
	}
	f.finish, f.polls, f.hiccups = "", 0, 20
	if _, err := p.Transcribe(context.Background(), writeAudio(t, "a.mp3", 10), ""); err == nil {
		t.Fatal("a result that never becomes readable must end in an error")
	}
}

func TestVolcASRNeedsAPublicAddress(t *testing.T) {
	p := newVolc(Account{BaseURL: "https://openspeech.bytedance.com", APIKey: "vk", Publish: func(ctx context.Context, path string) (string, func(), error) {
		return "http://localhost:8080/files/tmp/share/x.mp3", func() {}, nil
	}})
	_, err := p.Transcribe(context.Background(), writeAudio(t, "a.mp3", 10), "")
	if err == nil || !strings.Contains(err.Error(), "VB_PUBLIC_URL") {
		t.Fatalf("a private site address cannot be downloaded by the real service: %v", err)
	}
}

func TestVolcASRCheck(t *testing.T) {
	f := &fakeVolcASR{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	msg, err := newVolc(Account{BaseURL: srv.URL, APIKey: "vk"}).Check(context.Background())
	if err != nil || !strings.Contains(msg, "密钥有效") {
		t.Fatalf("good key: %q %v", msg, err)
	}
	if len(f.submitted) != 0 {
		t.Fatal("the check must not submit a task")
	}
	if _, err := newVolc(Account{BaseURL: srv.URL, APIKey: "nope"}).Check(context.Background()); err == nil {
		t.Fatal("bad key accepted")
	}
}
