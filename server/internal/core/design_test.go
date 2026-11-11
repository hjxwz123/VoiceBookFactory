package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"voicebook/internal/model"
)

// fakeEnrollment records Qianwen voice-enrollment deletions.
func fakeEnrollment(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Input["action"] == "delete_voice" {
			mu.Lock()
			deleted = append(deleted, body.Input["voice_id"].(string))
			mu.Unlock()
		}
		w.Write([]byte(`{"output":{},"request_id":"r"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, deleted...) }
}

func qwenTestAccount(t *testing.T, a *App, base string) *model.ProviderAccount {
	a.Cfg.SecretKey = []byte(strings.Repeat("k", 32))
	acc := &model.ProviderAccount{Kind: model.KindTTS, Scope: "platform", Provider: "qwen", Name: "千问测试", BaseURL: base, CredentialsEnc: a.Encrypt("sk-test"), Enabled: true, Priority: 5, Config: model.EmptyMap()}
	acc.Models.V = []model.ProviderModel{}
	if err := a.DB.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	return acc
}

func TestDesignCandidatesCleanup(t *testing.T) {
	a, u := testApp(t)
	srv, deleted := fakeEnrollment(t)
	acc := qwenTestAccount(t, a, srv.URL)
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	at(t, start)
	d := &model.VoiceDesign{UserID: u.ID, StudioID: u.PersonalStudio, Prompt: "温柔的女声", Provider: "qwen", AccountID: acc.ID, Status: "ready", Attributes: model.EmptyMap()}
	d.Candidates.V = []model.DesignCandidate{
		{ProviderVoiceID: "qwen-audio-3.1-tts-plus-vd-a-1", Model: "qwen-audio-3.1-tts-plus", PriceTier: "premium"},
		{ProviderVoiceID: "qwen-audio-3.1-tts-plus-vd-a-2", Model: "qwen-audio-3.1-tts-plus", PriceTier: "premium"},
		{ProviderVoiceID: "qwen-audio-3.1-tts-plus-vd-a-3", Model: "qwen-audio-3.1-tts-plus", PriceTier: "premium"},
	}
	d.CreatedAt = start
	if err := a.DB.Create(d).Error; err != nil {
		t.Fatal(err)
	}
	x := &Access{User: u, StudioID: u.PersonalStudio}
	v, err := a.SaveDesignedVoice(x, d, 0, "我的设计", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Model != "qwen-audio-3.1-tts-plus" || v.PriceTier != "premium" || v.Origin != model.VoiceDesigned {
		t.Fatalf("saved voice = model %q tier %q origin %q", v.Model, v.PriceTier, v.Origin)
	}
	if _, err := a.SaveDesignedVoice(x, d, 0, "再存一次", nil, ""); err == nil {
		t.Fatal("a candidate must not be saved twice (two voices would share one platform voice)")
	}

	a.CleanupDesignCandidates() // too early: nothing happens
	if len(deleted()) != 0 {
		t.Fatalf("deleted before the keep period: %v", deleted())
	}
	at(t, start.Add(designKeep+time.Minute))
	a.CleanupDesignCandidates()
	got := deleted()
	if len(got) != 2 || got[0] == "qwen-audio-3.1-tts-plus-vd-a-1" || got[1] == "qwen-audio-3.1-tts-plus-vd-a-1" {
		t.Fatalf("expected the two unsaved candidates to be deleted, got %v", got)
	}
	var fresh model.VoiceDesign
	a.DB.First(&fresh, "id = ?", d.ID)
	if !fresh.Candidates.V[1].Discarded || !fresh.Candidates.V[2].Discarded || fresh.Candidates.V[0].Discarded {
		t.Fatalf("candidates = %+v", fresh.Candidates.V)
	}
	if _, err := a.SaveDesignedVoice(x, &fresh, 1, "太晚了", nil, ""); err == nil || !strings.Contains(err.Error(), "已从语音平台清理") {
		t.Fatalf("saving a discarded candidate: %v", err)
	}
	a.CleanupDesignCandidates() // idempotent
	if len(deleted()) != 2 {
		t.Fatalf("cleanup ran twice: %v", deleted())
	}

	// deleting the designed voice removes it on the platform, unless another voice still uses it
	twin := *v
	twin.ID = model.NewID()
	a.DB.Create(&twin)
	if err := a.DeleteVoice(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	if len(deleted()) != 2 {
		t.Fatalf("platform voice deleted while another voice still uses it: %v", deleted())
	}
	if err := a.DeleteVoice(context.Background(), &twin); err != nil {
		t.Fatal(err)
	}
	if got := deleted(); len(got) != 3 || got[2] != "qwen-audio-3.1-tts-plus-vd-a-1" {
		t.Fatalf("last voice deleted locally but not on the platform: %v", got)
	}
}
