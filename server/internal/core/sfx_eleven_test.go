package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"voicebook/internal/ai/gen"
	"voicebook/internal/audio"
	"voicebook/internal/model"
	"voicebook/internal/storage"
)

// A sound effect ordered in the studio goes to ElevenLabs (here a fake on 127.0.0.1) with the Chinese
// description rewritten in English, is saved to the asset library and costs what it lasted.
func TestElevenLabsSoundEffectsJob(t *testing.T) {
	a, u := testApp(t)
	a.Cfg.SecretKey = []byte("0123456789abcdef0123456789abcdef") // so the account key can be stored encrypted
	a.FF = &audio.FF{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	local, err := storage.NewLocal(t.TempDir(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	a.Store = storage.NewStore(local)
	f := newFakeLLM(t)
	a.DB.Create(&model.ProviderAccount{Kind: model.KindLLM, Provider: "openai_chat", Scope: "platform", Name: "本机模拟", BaseURL: f.srv.URL + "/v1", Model: "fake-model", CredentialsEnc: a.Encrypt("sk-test"), Enabled: true, Priority: 1000, Config: model.EmptyMap()})
	ph, _ := gen.NewSFX(gen.Account{Provider: "placeholder"})
	sample, err := ph.GenerateSFX(context.Background(), gen.SFXRequest{Prompt: "雨", DurationMs: 3000})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []map[string]any
	el := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sound-generation" || r.Header.Get("xi-api-key") != "sk_el" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(sample.Data)
	}))
	t.Cleanup(el.Close)
	acc := model.ProviderAccount{Kind: model.KindSFX, Provider: "elevenlabs_sfx", Scope: "platform", Name: "ElevenLabs 本机模拟", BaseURL: el.URL, CredentialsEnc: a.Encrypt("sk_el"), Enabled: true, Priority: 1000, CostPerUnit: 1.2, Config: model.EmptyMap()}
	a.DB.Create(&acc)
	a.Grant(u.PersonalStudio, 100000, "grant", nil, "测试", "")
	startQueue(t, a)
	x, err := a.StudioAccess(u, u.PersonalStudio)
	if err != nil {
		t.Fatal(err)
	}
	opts := a.GenOptions(u.PersonalStudio)["sfx"].(map[string]any)
	if opts["name"] != "ElevenLabs" || opts["autoDuration"] != true {
		t.Fatalf("options: %v", opts)
	}
	j, err := a.EnqueueGenerate(x, GenRequest{Kind: model.KindSFX, Prompt: "夜里大雨打在铁皮屋顶上", Count: 2, Loop: true})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, a, j.ID); done.Status != model.JobSucceeded {
		t.Fatalf("job %s: %s", done.Status, done.Error)
	}
	if len(bodies) != 2 {
		t.Fatalf("want 2 generations, got %d", len(bodies))
	}
	for _, b := range bodies {
		if b["text"] != "heavy rain drumming on a tin roof at night, close-mic, steady ambience" || b["loop"] != true || b["model_id"] != "eleven_text_to_sound_v3" {
			t.Fatalf("request: %v", b)
		}
		if _, ok := b["duration_seconds"]; ok {
			t.Fatalf("no length chosen: the service decides, got %v", b["duration_seconds"])
		}
	}
	var assets []model.Asset
	a.DB.Where("studio_id = ? AND kind = ?", u.PersonalStudio, model.AssetSFX).Find(&assets)
	if len(assets) != 2 || assets[0].License != "provider_terms" || assets[0].Meta.V["providerPrompt"] == nil || assets[0].DurationMs < 2500 {
		t.Fatalf("assets: %+v", assets)
	}
	var use model.UsageRecord
	a.DB.First(&use, "job_id = ?", j.ID)
	want := float64(assets[0].DurationMs+assets[1].DurationMs) / 60000 * 1.2
	if use.Feature != FeatSFX || use.AccountID != acc.ID || use.CostCNY < want*0.99 || use.CostCNY > want*1.01 {
		t.Fatalf("usage: %+v (want cost %.4f)", use, want)
	}
	var stored model.ProviderAccount
	a.DB.First(&stored, "id = ?", acc.ID)
	if stored.Calls != 2 || stored.HealthStatus != "ok" {
		t.Fatalf("account stats: %d calls, %s", stored.Calls, stored.HealthStatus)
	}
	// the account test checks the key and never generates
	n := len(bodies)
	a.TestAccount(context.Background(), &stored)
	if len(bodies) != n {
		t.Fatal("testing the account generated a billed sound")
	}
}
