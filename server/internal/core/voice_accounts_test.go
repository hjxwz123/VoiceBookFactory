package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"voicebook/internal/model"
)

// fakeQwenVoices answers Qianwen voice-enrollment: "full" refuses new voices like an account at its limit,
// "undeployed" fails review, otherwise voices are created and pass review. Deletions are recorded.
type fakeQwenVoices struct {
	*httptest.Server
	mode    string
	created atomic.Int32
	mu      sync.Mutex
	deleted []string
}

func newFakeQwenVoices(t *testing.T, mode string) *fakeQwenVoices {
	f := &fakeQwenVoices{mode: mode}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/audio/tts/customization") {
			http.NotFound(w, r) // synthesis is not part of these tests
			return
		}
		var body struct {
			Input map[string]any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mode := f.getMode()
		switch body.Input["action"] {
		case "create_voice":
			if mode == "full" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"code":"Throttling.AllocationQuota","message":"Maximum voice storage limit exceeded","request_id":"r"}`))
				return
			}
			n := f.created.Add(1)
			id := fmt.Sprintf("qwen-audio-3.1-tts-plus-vc-%s-%d", mode, n)
			preview := base64.StdEncoding.EncodeToString(toneWAV(2))
			json.NewEncoder(w).Encode(map[string]any{"request_id": "r", "output": map[string]any{"voice_id": id,
				"preview_audio": map[string]any{"data": preview, "response_format": "wav", "sample_rate": 44100}}})
		case "query_voice":
			st := "OK"
			if mode == "undeployed" {
				st = "UNDEPLOYED"
			}
			w.Write([]byte(`{"request_id":"r","output":{"status":"` + st + `"}}`))
		case "delete_voice":
			f.mu.Lock()
			f.deleted = append(f.deleted, body.Input["voice_id"].(string))
			f.mu.Unlock()
			w.Write([]byte(`{"request_id":"r","output":{}}`))
		default:
			w.Write([]byte(`{"request_id":"r","output":{}}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeQwenVoices) getMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *fakeQwenVoices) setMode(m string) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
}

func (f *fakeQwenVoices) gone() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.deleted...)
}

func qwenAccount(t *testing.T, a *App, name, base string, priority int, limit int) *model.ProviderAccount {
	t.Helper()
	acc := qwenTestAccount(t, a, base)
	up := map[string]any{"name": name, "priority": priority}
	if limit > 0 {
		acc.Config.V["voiceLimit"] = float64(limit)
		up["config"] = acc.Config
	}
	a.DB.Model(acc).Updates(up)
	a.DB.First(acc, "id = ?", acc.ID)
	a.InvalidateTTSProviders()
	return acc
}

func customVoice(t *testing.T, a *App, u *model.User, acc *model.ProviderAccount, id string) *model.Voice {
	t.Helper()
	v := &model.Voice{Scope: model.ScopeUser, StudioID: u.PersonalStudio, OwnerUserID: u.ID, Name: "我的声音 " + id, Origin: model.VoiceCloned, Provider: acc.Provider,
		ProviderAccountID: acc.ID, ProviderVoiceID: id, Model: "qwen-audio-3.1-tts-plus", Language: "zh-CN", Status: model.VoiceReady, CreatedBy: u.ID, Emotions: model.Strings(), Tags: model.Strings()}
	if err := a.DB.Create(v).Error; err != nil {
		t.Fatal(err)
	}
	return v
}

func opsAdmin(t *testing.T, a *App) *model.User {
	admin := &model.User{Nickname: "运营", AdminRoles: model.Strings(model.AdminOps), Status: "active"}
	if err := a.DB.Create(admin).Error; err != nil {
		t.Fatal(err)
	}
	return admin
}

func opsAlerts(a *App, userID string) []model.Notification {
	var ns []model.Notification
	a.DB.Where("user_id = ? AND type = ?", userID, "ops_alert").Order("created_at").Find(&ns)
	return ns
}

// A cloned or designed voice exists only on the account that created it: it is synthesized and deleted there,
// and never handed to another account of the same platform.
func TestCustomVoiceStaysOnItsAccount(t *testing.T) {
	a, u := testApp(t)
	srvA, srvB := newFakeQwenVoices(t, "ok"), newFakeQwenVoices(t, "ok")
	accA := qwenAccount(t, a, "千问 A", srvA.URL, 5, 0)
	accB := qwenAccount(t, a, "千问 B", srvB.URL, 10, 0) // B is preferred for everything new
	v := customVoice(t, a, u, accA, "qwen-audio-3.1-tts-plus-vc-a")

	if _, acc, err := a.TTSFor(v); err != nil || acc.ID != accA.ID {
		t.Fatalf("a voice made on A is spoken with A, got %v %v", acc, err)
	}
	preset := &model.Voice{Scope: model.ScopeSystem, Name: "龙小淳", Origin: model.VoicePreset, Provider: "qwen", ProviderVoiceID: "longxiaochun", Model: "cosyvoice-v3-flash", Status: model.VoiceReady, Listed: true, Emotions: model.Strings(), Tags: model.Strings()}
	a.DB.Create(preset)

	a.DB.Model(accA).Update("enabled", false)
	a.InvalidateTTSProviders()
	_, acc, err := a.TTSFor(v)
	var e *Error
	if err == nil || !errors.As(err, &e) || e.Code != "voice_account_unavailable" || !strings.Contains(e.Msg, "停用") {
		t.Fatalf("with A disabled the voice must not move to B: acc=%v err=%v", acc, err)
	}
	if _, acc, err := a.TTSFor(preset); err != nil || acc.ID != accB.ID {
		t.Fatalf("built-in voices still work on any account: %v %v", acc, err)
	}
	if view := a.VoiceView(v, u.ID); view["unavailable"] != true || !strings.Contains(view["statusNote"].(string), "停用") {
		t.Fatalf("the voice list says why the voice cannot be used: %v", view["statusNote"])
	}
	for _, c := range a.CastingPool(u.PersonalStudio, u.ID, "") {
		if c.ID == v.ID {
			t.Fatal("casting must not pick a voice nobody can speak")
		}
	}

	// deleting the voice goes to A (even disabled), the only account that has it
	twin := customVoice(t, a, u, accA, "qwen-audio-3.1-tts-plus-vc-a2")
	if err := a.DeleteVoice(context.Background(), twin); err != nil {
		t.Fatal(err)
	}
	if got := srvA.gone(); len(got) != 1 || got[0] != "qwen-audio-3.1-tts-plus-vc-a2" || len(srvB.gone()) != 0 {
		t.Fatalf("deleted on A=%v B=%v", srvA.gone(), srvB.gone())
	}

	a.DB.Delete(accA)
	a.InvalidateTTSProviders()
	if _, _, err := a.TTSFor(v); err == nil || !strings.Contains(err.Error(), "已被删除") {
		t.Fatalf("account removed: %v", err)
	}

	// voices kept as recordings on this site (Volcengine) work on any account of the platform
	off := model.ProviderAccount{Kind: model.KindTTS, Provider: "volcengine_audio", Scope: "platform", Name: "火山 旧", Enabled: false, Config: model.EmptyMap()}
	on := model.ProviderAccount{Kind: model.KindTTS, Provider: "volcengine_audio", Scope: "platform", Name: "火山 新", Enabled: true, Config: model.EmptyMap()}
	a.DB.Create(&off)
	a.DB.Create(&on)
	a.InvalidateTTSProviders()
	ref := &model.Voice{Scope: model.ScopeUser, OwnerUserID: u.ID, StudioID: u.PersonalStudio, Name: "参考录音", Origin: model.VoiceCloned, Provider: "volcengine_audio", ProviderAccountID: off.ID, ProviderVoiceID: "ref:abc", Status: model.VoiceReady}
	if _, acc, err := a.TTSFor(ref); err != nil || acc.ID != on.ID {
		t.Fatalf("a reference-recording voice moves to another account: %v %v", acc, err)
	}
	if a.VoiceView(ref, u.ID)["unavailable"] == true {
		t.Fatal("a reference-recording voice is never unavailable because of its account")
	}
}

// Qianwen keeps at most 1000 custom voices per account: new voices go to an account with room, administrators
// are told in time, and users are told plainly when no account has room.
func TestCustomVoiceQuota(t *testing.T) {
	a, u := testApp(t)
	admin := opsAdmin(t, a)
	srv := newFakeQwenVoices(t, "ok")
	accA := qwenAccount(t, a, "千问 A", srv.URL, 10, 2)
	accB := qwenAccount(t, a, "千问 B", srv.URL, 5, 2)
	def := qwenAccount(t, a, "千问 默认", srv.URL, 1, 0)
	if q := a.VoiceQuotaOf(def); q == nil || q.Limit != 1000 {
		t.Fatalf("Qianwen's documented limit is 1000 per account: %+v", q)
	}
	a.DB.Delete(def)
	customVoice(t, a, u, accA, "vc-1")
	gone := customVoice(t, a, u, accA, "vc-2")
	failed := customVoice(t, a, u, accA, "vc-x")
	a.DB.Model(failed).Updates(map[string]any{"status": model.VoiceFailed})
	if q := a.VoiceQuotaOf(accA); q.Used != 2 || !q.Full {
		t.Fatalf("A: %+v (failed voices are not counted)", q)
	}
	if _, acc, _, err := a.voiceAccount("", u.PersonalStudio, wantClone("quick"), 1, nil); err != nil || acc.ID != accB.ID {
		t.Fatalf("A is full, new voices go to B: %v %v", acc, err)
	}
	if o := a.CloneOptions(u.PersonalStudio)["quick"]; !o.Available || o.Full || o.Provider != "qwen" {
		t.Fatalf("clone options: %+v", o)
	}
	if len(opsAlerts(a, admin.ID)) != 0 {
		t.Fatal("nothing to report yet")
	}

	// B: one saved voice plus one design candidate nobody saved yet
	customVoice(t, a, u, accB, "vc-3")
	d := &model.VoiceDesign{UserID: u.ID, StudioID: u.PersonalStudio, Prompt: "温柔", Provider: "qwen", AccountID: accB.ID, Status: "ready", Attributes: model.EmptyMap()}
	d.Candidates.V = []model.DesignCandidate{{ProviderVoiceID: "vd-1", Saved: true}, {ProviderVoiceID: "vd-2"}, {ProviderVoiceID: "vd-3", Discarded: true}}
	a.DB.Create(d)
	if q := a.VoiceQuotaOf(accB); q.Used != 2 || !q.Full {
		t.Fatalf("B: %+v", q)
	}
	demo := model.ProviderAccount{Kind: model.KindTTS, Provider: "placeholder", Scope: "platform", Name: "内置演示引擎", Enabled: true, Config: model.EmptyMap()}
	a.DB.Create(&demo) // full real accounts are not replaced by the demo engine
	a.Cfg.DevMode = true
	if o := a.CloneOptions(u.PersonalStudio)["quick"]; !o.Available || !o.Full || !strings.Contains(o.FullNote, "名额") || o.Provider != "qwen" {
		t.Fatalf("every account full: %+v", o)
	}
	if o := a.DesignOptions(u.PersonalStudio); !o.Available || !o.Full || o.Demo {
		t.Fatalf("design options: %+v", o)
	}
	x := &Access{User: u, StudioID: u.PersonalStudio, Role: model.RoleOwner}
	_, _, err := a.StartVoiceDesign(x, "二十岁左右的女生，声音清亮温柔", "", "", "")
	var e *Error
	if !errors.As(err, &e) || e.Code != "voice_quota_full" {
		t.Fatalf("design with every account full: %v", err)
	}
	alerts := opsAlerts(a, admin.ID)
	if len(alerts) == 0 || !strings.Contains(alerts[len(alerts)-1].Title, "名额已用完") || !strings.Contains(alerts[len(alerts)-1].Body, "添加一个新的千问账号") ||
		!strings.Contains(alerts[len(alerts)-1].Body, "API Key 共用") {
		t.Fatalf("administrators are asked to add an account: %+v", alerts)
	}
	n := len(alerts)
	a.StartVoiceDesign(x, "二十岁左右的女生，声音清亮温柔", "", "", "")
	if len(opsAlerts(a, admin.ID)) != n {
		t.Fatal("the same alert is sent at most once a day")
	}
	dash := a.VoiceQuotaAlerts()
	if len(dash) == 0 || dash[0]["level"] != "danger" || !strings.Contains(dash[0]["title"].(string), "名额已用完") {
		t.Fatalf("dashboard alerts: %v", dash)
	}

	// deleting a voice frees a slot on its own account
	if err := a.DeleteVoice(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	if _, acc, _, err := a.voiceAccount("", u.PersonalStudio, wantClone("quick"), 1, nil); err != nil || acc.ID != accA.ID {
		t.Fatalf("A has room again: %v %v", acc, err)
	}
	// the platform's own count wins when it refuses a voice; a deletion on that account clears it
	a.markVoicesFull(accA)
	if r := a.voiceRoom(accA); r != 0 {
		t.Fatalf("room after the platform said full: %d", r)
	}
	a.voicesFreed(accA.ID)
	var fresh model.ProviderAccount
	a.DB.First(&fresh, "id = ?", accA.ID)
	if fresh.VoicesFullAt != nil || a.voiceRoom(&fresh) != 1 {
		t.Fatalf("mark not cleared: %v room %d", fresh.VoicesFullAt, a.voiceRoom(&fresh))
	}
}

// Cloning moves on when the platform says an account is full, and a voice that fails review does not stay on
// the platform taking a slot.
func TestCloneUsesAnotherAccountAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	a, u := speechTestApp(t)
	admin := opsAdmin(t, a)
	a.Grant(u.PersonalStudio, 100000, "grant", nil, "测试", "")
	startQueue(t, a)
	full, ok := newFakeQwenVoices(t, "full"), newFakeQwenVoices(t, "ok")
	accFull := qwenAccount(t, a, "千问 满", full.URL, 10, 0)
	accOK := qwenAccount(t, a, "千问 空", ok.URL, 5, 0)
	sample, err := a.SaveAssetBytes(context.Background(), AssetInput{StudioID: u.PersonalStudio, Kind: model.AssetSample, Name: "样本", Ext: "wav", Origin: "upload", CreatedBy: u.ID}, toneWAV(12))
	if err != nil {
		t.Fatal(err)
	}
	clone := func(name string) *model.Voice {
		v := &model.Voice{Scope: model.ScopeUser, StudioID: u.PersonalStudio, OwnerUserID: u.ID, Name: name, Origin: model.VoiceCloned, Provider: "qwen", Language: "zh-CN",
			Status: model.VoiceTraining, CloneTier: "quick", SampleAssetID: sample.ID, CreatedBy: u.ID, Emotions: model.Strings(), Tags: model.Strings()}
		a.DB.Create(v)
		a.enqueueClone(v, u.ID, map[string]any{"samples": []any{sample.ID}})
		a.DB.First(v, "id = ?", v.ID)
		waitJob(t, a, v.TrainingJobID)
		a.DB.First(v, "id = ?", v.ID)
		return v
	}
	v := clone("换账号")
	if v.Status != model.VoiceReady || v.ProviderAccountID != accOK.ID || !strings.HasPrefix(v.ProviderVoiceID, "qwen-audio-3.1-tts-plus-vc-ok-") {
		t.Fatalf("clone: status %s account %s voice %s note %s", v.Status, v.ProviderAccountID, v.ProviderVoiceID, v.StatusNote)
	}
	a.DB.First(accFull, "id = ?", accFull.ID)
	if accFull.VoicesFullAt == nil {
		t.Fatal("the full account is remembered")
	}
	if alerts := opsAlerts(a, admin.ID); len(alerts) != 1 || !strings.Contains(alerts[0].Title, "千问 满") || !strings.Contains(alerts[0].Body, "自动使用其他千问账号") {
		t.Fatalf("alerts: %+v", alerts)
	}

	ok.setMode("undeployed")
	bad := clone("审核不过")
	if bad.Status != model.VoiceFailed || bad.ProviderVoiceID != "" {
		t.Fatalf("failed review: status %s voice %q", bad.Status, bad.ProviderVoiceID)
	}
	if got := ok.gone(); len(got) != 1 || !strings.HasPrefix(got[0], "qwen-audio-3.1-tts-plus-vc-undeployed-") {
		t.Fatalf("a voice that failed review is deleted from the platform: %v", got)
	}
	if q := a.VoiceQuotaOf(accOK); q.Used != 1 {
		t.Fatalf("only the kept voice counts: %+v", q)
	}
}

// Designs move on as well, and ask an account for no more candidates than it has room for.
func TestDesignUsesAccountWithRoom(t *testing.T) {
	a, u := speechTestApp(t)
	a.Grant(u.PersonalStudio, 100000, "grant", nil, "测试", "")
	startQueue(t, a)
	full, ok := newFakeQwenVoices(t, "full"), newFakeQwenVoices(t, "ok")
	qwenAccount(t, a, "千问 满", full.URL, 10, 0)
	accOK := qwenAccount(t, a, "千问 剩两个", ok.URL, 5, 3)
	customVoice(t, a, u, accOK, "vc-old")
	x := &Access{User: u, StudioID: u.PersonalStudio, Role: model.RoleOwner}
	d, j, err := a.StartVoiceDesign(x, "二十岁左右的女生，声音清亮温柔", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if done := waitJob(t, a, j.ID); done.Status != model.JobSucceeded {
		t.Fatalf("design job %s: %s", done.Status, done.Error)
	}
	a.DB.First(d, "id = ?", d.ID)
	if d.AccountID != accOK.ID || len(d.Candidates.V) != 2 || ok.created.Load() != 2 {
		t.Fatalf("design on %s with %d candidates (%d created)", d.AccountID, len(d.Candidates.V), ok.created.Load())
	}
	if q := a.VoiceQuotaOf(accOK); q.Used != 3 || !q.Full {
		t.Fatalf("candidates take slots until saved or cleaned up: %+v", q)
	}
}

// The voice library only offers models of platforms an administrator configured.
func TestVoiceModelsOnlyConfiguredPlatforms(t *testing.T) {
	a, u := testApp(t)
	for _, v := range []model.Voice{
		{Name: "千问音色", Provider: "qwen", Model: "qwen-audio-3.1-tts-flash"},
		{Name: "千问音色 Plus", Provider: "qwen", Model: "qwen-audio-3.1-tts-plus"},
		{Name: "MiniMax 音色", Provider: "minimax", Model: "speech-2.8-hd"},
		{Name: "火山音色", Provider: "volcengine_audio", Model: "seed-audio-1.0"},
	} {
		v.Scope, v.Origin, v.Status, v.Listed, v.Emotions, v.Tags = model.ScopeSystem, model.VoicePreset, model.VoiceReady, true, model.Strings(), model.Strings()
		a.DB.Create(&v)
	}
	values := func() []string {
		var out []string
		for _, m := range a.VoiceModelOptions(u.ID, u.PersonalStudio) {
			out = append(out, m.Value)
		}
		return out
	}
	if got := values(); len(got) != 0 {
		t.Fatalf("no platform configured, no model offered: %v", got)
	}
	a.DB.Create(&model.ProviderAccount{Kind: model.KindTTS, Provider: "qwen", Scope: "platform", Name: "千问", Enabled: true, Config: model.EmptyMap()})
	a.DB.Create(&model.ProviderAccount{Kind: model.KindTTS, Provider: "minimax", Scope: "platform", Name: "MiniMax", Enabled: false, Config: model.EmptyMap()})
	a.InvalidateTTSProviders()
	if got := strings.Join(values(), ","); got != "qwen-audio-3.1-tts-plus,qwen-audio-3.1-tts-flash" {
		t.Fatalf("only the enabled platform's models, best first: %s", got)
	}
	if s := a.ConfiguredServices(); strings.Join(s[model.KindTTS], ",") != "千问" {
		t.Fatalf("public pages name configured services only: %v", s)
	}
}
