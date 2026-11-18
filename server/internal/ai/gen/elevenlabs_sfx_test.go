package gen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeEleven speaks the ElevenLabs sound-generation protocol on 127.0.0.1; it never reaches the real service.
func fakeEleven(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &body)
		}
		handle(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestElevenLabsSFXRequest(t *testing.T) {
	var got map[string]any
	var path, query, key string
	srv := fakeEleven(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		got, path, query, key = body, r.URL.Path, r.URL.RawQuery, r.Header.Get("xi-api-key")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("request-id", "req_1")
		w.Write(make([]byte, 4096))
	})
	p, err := NewSFX(Account{Provider: "elevenlabs_sfx", BaseURL: srv.URL + "/", APIKey: " sk_test ", Config: map[string]any{"promptInfluence": 0.6}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.GenerateSFX(context.Background(), SFXRequest{Prompt: "rain on a tin roof, close-mic", DurationMs: 45000, Loop: true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/sound-generation" || query != "output_format=mp3_44100_128" || key != "sk_test" {
		t.Fatalf("request: %s?%s key %q", path, query, key)
	}
	if got["text"] != "rain on a tin roof, close-mic" || got["model_id"] != ElevenSFXDefaultModel || got["duration_seconds"] != 30.0 || got["loop"] != true || got["prompt_influence"] != 0.6 {
		t.Fatalf("body: %v", got)
	}
	if f.Ext != "mp3" || len(f.Data) != 4096 || f.Meta["requestId"] != "req_1" || f.Meta["provider"] != "elevenlabs_sfx" {
		t.Fatalf("file: %s %d %v", f.Ext, len(f.Data), f.Meta)
	}
	// no duration: the model chooses; no loop flag sent
	p.GenerateSFX(context.Background(), SFXRequest{Prompt: "door creak"})
	if _, ok := got["duration_seconds"]; ok || got["loop"] != nil {
		t.Fatalf("auto duration body: %v", got)
	}
	// a short request is raised to the 0.5 s minimum
	p.GenerateSFX(context.Background(), SFXRequest{Prompt: "click", DurationMs: 100})
	if got["duration_seconds"] != 0.5 {
		t.Fatalf("minimum duration: %v", got["duration_seconds"])
	}
}

func TestElevenLabsSFXErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, "密钥无效"},
		{401, `{"detail":{"status":"missing_permissions","message":"The API key you used is missing the permission sound_generation"}}`, "Sound Effects"},
		{401, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota"}}`, "额度用完"},
		{429, `{"detail":{"status":"too_many_concurrent_requests","message":"busy"}}`, "繁忙"},
		{422, `{"detail":[{"loc":["body","duration_seconds"],"msg":"ensure this value is less than or equal to 30","type":"value_error"}]}`, "duration_seconds: ensure"},
		{500, `oops`, "暂时出错"},
	}
	for _, c := range cases {
		srv := fakeEleven(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		})
		p, _ := NewSFX(Account{Provider: "elevenlabs_sfx", BaseURL: srv.URL, APIKey: "k"})
		_, err := p.GenerateSFX(context.Background(), SFXRequest{Prompt: "x"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d: %v", c.status, err)
		}
	}
	// JSON with status 200 is not audio
	srv := fakeEleven(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	p, _ := NewSFX(Account{Provider: "elevenlabs_sfx", BaseURL: srv.URL, APIKey: "k"})
	if _, err := p.GenerateSFX(context.Background(), SFXRequest{Prompt: "x"}); err == nil {
		t.Fatal("a JSON reply is not a sound")
	}
	if _, err := (&elevenSFX{base: srv.URL}).GenerateSFX(context.Background(), SFXRequest{Prompt: "x"}); err == nil {
		t.Fatal("no key")
	}
}

func TestElevenLabsSFXCheckDoesNotGenerate(t *testing.T) {
	generated := false
	srv := fakeEleven(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch r.URL.Path {
		case "/v1/user/subscription":
			if r.Header.Get("xi-api-key") == "limited" {
				w.WriteHeader(401)
				w.Write([]byte(`{"detail":{"status":"missing_permissions","message":"missing user_read"}}`))
				return
			}
			if r.Header.Get("xi-api-key") != "good" {
				w.WriteHeader(401)
				w.Write([]byte(`{"detail":{"status":"invalid_api_key","message":"bad"}}`))
				return
			}
			w.Write([]byte(`{"tier":"creator","character_count":1200,"character_limit":100000,"status":"active"}`))
		default:
			generated = true
		}
	})
	check := func(key string) (string, error) {
		p, _ := NewSFX(Account{Provider: "elevenlabs_sfx", BaseURL: srv.URL, APIKey: key})
		return p.(Checker).Check(context.Background())
	}
	if msg, err := check("good"); err != nil || !strings.Contains(msg, "creator") || !strings.Contains(msg, "1200 / 100000") {
		t.Fatalf("good key: %q %v", msg, err)
	}
	if msg, err := check("limited"); err != nil || !strings.Contains(msg, "密钥有效") {
		t.Fatalf("a key limited to sound effects is still valid: %q %v", msg, err)
	}
	if _, err := check("bad"); err == nil {
		t.Fatal("bad key accepted")
	}
	if generated {
		t.Fatal("checking a key must not generate (it is billed)")
	}
}
