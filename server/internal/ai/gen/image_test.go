package gen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func init() { retryUnit = time.Millisecond }

func tinyPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func readJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, _ := io.ReadAll(r.Body)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("body is not JSON: %s", raw)
	}
	return m
}

func TestOpenAIImageGenerate(t *testing.T) {
	pic := tinyPNG(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("unexpected call %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		body = readJSON(t, r)
		fmt.Fprintf(w, `{"created":1,"output_format":"png","size":"1024x1024","quality":"medium","data":[{"b64_json":%q},{"b64_json":%q}]}`, pic, pic)
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "openai_image", BaseURL: srv.URL + "/v1", APIKey: "sk-test", Config: map[string]any{"quality": "medium"}})
	files, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "雨夜古宅", Aspect: "1:1", Count: 2, User: "vb_x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Mime != "image/png" || files[0].Ext != "png" || len(files[0].Data) == 0 {
		t.Fatalf("got %d files %+v", len(files), files[0].Meta)
	}
	if body["model"] != DefaultOpenAIImageModel || body["n"] != float64(2) || body["size"] != "1024x1024" || body["quality"] != "medium" || body["output_format"] != "jpeg" {
		t.Fatalf("request body %v", body)
	}
	if _, ok := body["safety_identifier"]; ok {
		t.Fatal("safety_identifier must only go to api.openai.com, relays may reject it")
	}
	if files[0].Meta["model"] != DefaultOpenAIImageModel {
		t.Fatalf("meta %v", files[0].Meta)
	}
}

func TestOpenAISizes(t *testing.T) {
	cases := map[[3]string]string{
		{"gpt-image-1.5", "2k", "1:1"}:       "1024x1024", // 1.x only knows fixed sizes
		{"gpt-image-2.6-flare", "", "1:1"}:   "1024x1024",
		{"gpt-image-2.6-flare", "2k", "1:1"}: "2048x2048",
		{"gpt-image-2", "", "3:4"}:           "1024x1536",
		{"gpt-image-2", "", "16:9"}:          "1536x1024",
	}
	for in, want := range cases {
		if got := openAISize(in[0], in[1], in[2]); got != want {
			t.Errorf("%v: got %s want %s", in, got, want)
		}
	}
	// every 2K size must satisfy the documented limits
	for _, a := range []string{"1:1", "3:4", "2:3", "4:5", "9:16", "4:3", "3:2", "5:4", "16:9", "21:9"} {
		var w, h int
		fmt.Sscanf(openAISize("gpt-image-2.5-sunburst", "2k", a), "%dx%d", &w, &h)
		px := w * h
		ratio := float64(w) / float64(h)
		if w%16 != 0 || h%16 != 0 || w > 3840 || h > 3840 || px < 655360 || px > 8294400 || ratio > 3 || ratio < 1.0/3 {
			t.Errorf("%s -> %dx%d breaks the API limits", a, w, h)
		}
	}
}

func TestOpenAIImageErrors(t *testing.T) {
	var calls int32
	reply := func(code int, body string) *httptest.Server {
		atomic.StoreInt32(&calls, 0)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(code)
			io.WriteString(w, body)
		}))
	}
	for _, c := range []struct {
		code       int
		body, want string
		calls      int32
	}{
		{400, `{"error":{"message":"Your request was rejected by the safety system.","code":"moderation_blocked"}}`, "安全审核", 1},
		{403, `{"error":{"message":"Your organization must be verified to use the model gpt-image-2.6-flare."}}`, "组织验证", 1},
		{429, `{"error":{"message":"You exceeded your current quota","code":"insufficient_quota"}}`, "余额", 1},
		{401, `{"error":{"message":"Incorrect API key provided"}}`, "API Key", 1},
		{503, `{"error":{"message":"overloaded"}}`, "暂时不可用", 3}, // retried
	} {
		srv := reply(c.code, c.body)
		p, _ := NewImage(Account{Provider: "openai_image", BaseURL: srv.URL, APIKey: "k"})
		_, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "x"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("HTTP %d: got %v, want %q", c.code, err, c.want)
		}
		if got := atomic.LoadInt32(&calls); got != c.calls {
			t.Errorf("HTTP %d: %d calls, want %d", c.code, got, c.calls)
		}
	}
}

func TestOpenAICheckFallsBackToModelList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			io.WriteString(w, `{"data":[{"id":"gpt-image-2.5-flare"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "openai_image", BaseURL: srv.URL, APIKey: "k", Model: "gpt-image-2.5-flare"})
	msg, err := p.(Checker).Check(context.Background())
	if err != nil || !strings.Contains(msg, "不产生费用") {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
	p, _ = NewImage(Account{Provider: "openai_image", BaseURL: srv.URL, APIKey: "k", Model: "gpt-image-9"})
	if _, err := p.(Checker).Check(context.Background()); err == nil || !strings.Contains(err.Error(), "gpt-image-9") {
		t.Fatalf("missing model should fail, got %v", err)
	}
}

func TestGeminiInteractions(t *testing.T) {
	pic := tinyPNG(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/interactions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "g-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Api-Revision") != geminiAPIRevision {
			t.Errorf("headers %v", r.Header)
		}
		b := readJSON(t, r)
		f, _ := b["response_format"].(map[string]any)
		if b["model"] != DefaultGeminiImageModel || f["type"] != "image" || f["aspect_ratio"] != "1:1" || f["image_size"] != "2K" || b["store"] != false {
			t.Errorf("body %v", b)
		}
		if atomic.AddInt32(&calls, 1) == 2 {
			w.WriteHeader(500) // one transient failure is retried
			return
		}
		fmt.Fprintf(w, `{"id":"i1","status":"completed","steps":[{"type":"thought","content":[]},{"type":"model_output","content":[{"type":"text","text":"好的"},{"type":"image","mime_type":"image/jpeg","data":%q}]}]}`, pic)
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "gemini_image", BaseURL: srv.URL, APIKey: "g-key", Config: map[string]any{"imageSize": "2k"}})
	files, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "封面", Width: 1200, Height: 1200, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Mime != "image/png" { // the bytes decide the type, not the label
		t.Fatalf("got %d files, mime %s", len(files), files[0].Mime)
	}
	if n := atomic.LoadInt32(&calls); n != 4 {
		t.Fatalf("%d calls, want 3 + 1 retry", n)
	}
}

func TestGeminiPartialAndTextOnly(t *testing.T) {
	pic := tinyPNG(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			io.WriteString(w, `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"text","text":"抱歉，我不能生成这张图片。"}]}]}`)
			return
		}
		fmt.Fprintf(w, `{"status":"completed","steps":[{"type":"model_output","content":[{"type":"image","data":%q}]}]}`, pic)
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "gemini_image", BaseURL: srv.URL + "/v1beta", APIKey: "k", Config: map[string]any{}})
	files, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "x", Count: 2})
	if err != nil || len(files) != 1 {
		t.Fatalf("partial success should keep the image that arrived: %d files, %v", len(files), err)
	}
	atomic.StoreInt32(&calls, 0)
	_, err = p.GenerateImage(context.Background(), ImageRequest{Prompt: "x", Count: 1})
	if err == nil || !strings.Contains(err.Error(), "不能生成") {
		t.Fatalf("text-only answer should surface the model's reply, got %v", err)
	}
}

func TestGeminiGenerateContentProtocol(t *testing.T) {
	pic := tinyPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/gemini-3-pro-image:generateContent" {
			t.Errorf("path %s", r.URL.Path)
		}
		b := readJSON(t, r)
		gc, _ := b["generationConfig"].(map[string]any)
		ic, _ := gc["imageConfig"].(map[string]any)
		if ic["aspectRatio"] != "1:1" || ic["imageSize"] != "1K" {
			t.Errorf("generationConfig %v", gc)
		}
		fmt.Fprintf(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"draft","thought":true},{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, pic)
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "gemini_image", BaseURL: srv.URL, APIKey: "k", Model: "models/gemini-3-pro-image", Config: map[string]any{"protocol": "generate_content"}})
	files, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "x"})
	if err != nil || len(files) != 1 || files[0].Meta["model"] != "gemini-3-pro-image" {
		t.Fatalf("files=%v err=%v", len(files), err)
	}
}

func TestGeminiErrorsAndCheck(t *testing.T) {
	for _, c := range []struct {
		code       int
		body, want string
	}{
		{400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`, "API Key 无效"},
		{400, `{"error":{"code":400,"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}}`, "地区"},
		{404, `{"error":{"code":404,"message":"models/x is not found","status":"NOT_FOUND"}}`, "找不到模型"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.code)
			io.WriteString(w, c.body)
		}))
		p, _ := NewImage(Account{Provider: "gemini_image", BaseURL: srv.URL, APIKey: "k"})
		_, err := p.GenerateImage(context.Background(), ImageRequest{Prompt: "x"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("got %v, want %q", err, c.want)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models/gemini-nano-banana-2.1" {
			t.Errorf("check hit %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"name":"models/gemini-nano-banana-2.1","displayName":"Nano Banana 2.1"}`)
	}))
	defer srv.Close()
	p, _ := NewImage(Account{Provider: "gemini_image", BaseURL: srv.URL, APIKey: "k"})
	msg, err := p.(Checker).Check(context.Background())
	if err != nil || !strings.Contains(msg, "Nano Banana 2.1") {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
}
