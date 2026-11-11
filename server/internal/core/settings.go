package core

import (
	"encoding/json"
	"sync"
	"time"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

type SiteSettings struct {
	Name              string `json:"name"`
	Slogan            string `json:"slogan"`
	Logo              string `json:"logo"`
	RegistrationOpen  bool   `json:"registrationOpen"`
	InviteOnly        bool   `json:"inviteOnly"`
	InviteCode        string `json:"inviteCode"`
	SignupCredits     int64  `json:"signupCredits"`
	MaxUploadMB       int    `json:"maxUploadMb"`
	MaxBookChars      int    `json:"maxBookChars"`
	FreePreviewsDaily int    `json:"freePreviewsDaily"`
	SupportEmail      string `json:"supportEmail"`
	SpeechRate        int    `json:"speechRate"`
	ICP               string `json:"icp"`
	Operator          string `json:"operator"` // legal entity running the site, shown in the agreements
}

type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	PassEnc  string `json:"passEnc"`
	From     string `json:"from"`
	FromName string `json:"fromName"`
	TLS      bool   `json:"tls"`
}

type AILabelSettings struct {
	Explicit bool   `json:"explicit"`
	Text     string `json:"text"`
	Position string `json:"position"` // intro | outro
	Implicit bool   `json:"implicit"`
	Producer string `json:"producer"`
}

type ModerationSettings struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"` // pre | post
	UseLLM  bool   `json:"useLlm"`
}

type BillingSettings struct {
	BYOKFeePer1K int64   `json:"byokFeePer1k"`
	MarketShare  float64 `json:"marketShare"` // share paid to voice owner
	DefaultPlan  string  `json:"defaultPlan"`
	TrialPlan    string  `json:"trialPlan"` // plan code new users get on trial ("" = no trial)
	TrialDays    int     `json:"trialDays"`
}

type ExportPreset struct {
	Key            string  `json:"key"`
	Name           string  `json:"name"`
	Format         string  `json:"format"`
	Bitrate        int     `json:"bitrate"`
	SampleRate     int     `json:"sampleRate"`
	Channels       int     `json:"channels"`
	LoudnessI      float64 `json:"loudnessI"`
	TruePeak       float64 `json:"truePeak"`
	RMSMin         float64 `json:"rmsMin"`
	RMSMax         float64 `json:"rmsMax"`
	NoiseFloorMax  float64 `json:"noiseFloorMax"`
	HeadSilenceMin int64   `json:"headSilenceMin"`
	HeadSilenceMax int64   `json:"headSilenceMax"`
	TailSilenceMin int64   `json:"tailSilenceMin"`
	TailSilenceMax int64   `json:"tailSilenceMax"`
	Note           string  `json:"note"`
}

var defaultPresets = []ExportPreset{
	{Key: "generic", Name: "通用（推荐）", Format: "mp3", Bitrate: 192, SampleRate: 44100, Channels: 2, LoudnessI: -16, TruePeak: -1.5, NoiseFloorMax: -55, HeadSilenceMin: 300, HeadSilenceMax: 2000, TailSilenceMin: 500, TailSilenceMax: 4000, Note: "适用于大多数音频平台的通用参数"},
	{Key: "ximalaya", Name: "喜马拉雅", Format: "mp3", Bitrate: 128, SampleRate: 44100, Channels: 2, LoudnessI: -16, TruePeak: -1, NoiseFloorMax: -55, HeadSilenceMin: 200, HeadSilenceMax: 3000, TailSilenceMin: 300, TailSilenceMax: 5000, Note: "建议值，请以平台最新规范为准"},
	{Key: "spotify", Name: "Spotify", Format: "mp3", Bitrate: 192, SampleRate: 44100, Channels: 2, LoudnessI: -16, TruePeak: -1, NoiseFloorMax: -55, HeadSilenceMin: 200, HeadSilenceMax: 3000, TailSilenceMin: 300, TailSilenceMax: 5000, Note: "建议值，请以平台最新规范为准"},
	{Key: "apple", Name: "Apple Podcasts", Format: "m4a", Bitrate: 128, SampleRate: 44100, Channels: 2, LoudnessI: -16, TruePeak: -1, NoiseFloorMax: -55, HeadSilenceMin: 200, HeadSilenceMax: 3000, TailSilenceMin: 300, TailSilenceMax: 5000, Note: "建议值"},
	{Key: "acx", Name: "ACX（Audible）", Format: "mp3", Bitrate: 192, SampleRate: 44100, Channels: 1, LoudnessI: -20, TruePeak: -3, RMSMin: -23, RMSMax: -18, NoiseFloorMax: -60, HeadSilenceMin: 500, HeadSilenceMax: 1000, TailSilenceMin: 1000, TailSilenceMax: 5000, Note: "ACX 要求 RMS -23~-18dB、峰值 ≤ -3dB、底噪 ≤ -60dB；注意 ACX 对 AI 音频有限制"},
	{Key: "master", Name: "母带（无损 WAV）", Format: "wav", Bitrate: 0, SampleRate: 48000, Channels: 2, LoudnessI: -18, TruePeak: -1, NoiseFloorMax: -55, HeadSilenceMin: 0, HeadSilenceMax: 5000, TailSilenceMin: 0, TailSilenceMax: 8000, Note: "用于归档或交给后期软件"},
}

var defaultCasting = map[string]model.CastingPolicy{
	model.TierEconomy:  {Narrator: "economy", Lead: "standard", Supporting: "economy", Minor: "economy", Crowd: "economy"},
	model.TierStandard: {Narrator: "standard", Lead: "standard", Supporting: "standard", Minor: "economy", Crowd: "economy"},
	model.TierPremium:  {Narrator: "premium", Lead: "premium", Supporting: "standard", Minor: "standard", Crowd: "economy"},
}

var settingDefaults = map[string]any{
	"site":           SiteSettings{Name: "声书工坊", Slogan: "让每一本书都被好好听见", RegistrationOpen: true, SignupCredits: 20000, MaxUploadMB: 200, MaxBookChars: 3_000_000, FreePreviewsDaily: 30, SpeechRate: 220},
	"smtp":           SMTPSettings{Port: 465, TLS: true},
	"ai_label":       AILabelSettings{Explicit: false, Text: "本作品音频由人工智能辅助生成。", Position: "outro", Implicit: true, Producer: "声书工坊"},
	"moderation":     ModerationSettings{Enabled: true, Mode: "post", UseLLM: false},
	"billing":        BillingSettings{BYOKFeePer1K: 0, MarketShare: 0.7, DefaultPlan: "free"},
	"export_presets": defaultPresets,
	"casting_policy": defaultCasting,
}

var settingMu sync.Mutex

// Setting loads a typed setting (with defaults) into out.
func (a *App) Setting(key string, out any) {
	if v, ok := a.settings.Load(key); ok {
		json.Unmarshal(v.([]byte), out)
		return
	}
	var row model.SystemSetting
	var raw []byte
	if err := a.DB.First(&row, "key = ?", key).Error; err == nil {
		raw = []byte(row.Value)
	} else if def, ok := settingDefaults[key]; ok {
		raw, _ = json.Marshal(def)
	} else {
		raw = []byte("null")
	}
	// overlay onto defaults so new fields get default values
	if def, ok := settingDefaults[key]; ok {
		b, _ := json.Marshal(def)
		json.Unmarshal(b, out)
	}
	json.Unmarshal(raw, out)
	merged, _ := json.Marshal(out)
	a.settings.Store(key, merged)
}

func (a *App) SetSetting(key string, v any) error {
	settingMu.Lock()
	defer settingMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	row := model.SystemSetting{Key: key, Value: string(b), UpdatedAt: time.Now().UTC()}
	if err := a.DB.Save(&row).Error; err != nil {
		return err
	}
	a.settings.Delete(key)
	a.publish(topicCache, cacheMsg{Kind: "setting", Key: key}) // other instances drop their copy too
	return nil
}

func (a *App) Site() SiteSettings {
	var s SiteSettings
	a.Setting("site", &s)
	return s
}

func (a *App) Presets() []ExportPreset {
	var p []ExportPreset
	a.Setting("export_presets", &p)
	if len(p) == 0 {
		return defaultPresets
	}
	return p
}

func (a *App) Preset(key string) ExportPreset {
	for _, p := range a.Presets() {
		if p.Key == key {
			return p
		}
	}
	return a.Presets()[0]
}

func (a *App) Billing() BillingSettings {
	var b BillingSettings
	a.Setting("billing", &b)
	return b
}

func (a *App) CastingPolicy(tier string) model.CastingPolicy {
	m := map[string]model.CastingPolicy{}
	a.Setting("casting_policy", &m)
	if p, ok := m[tier]; ok {
		return p
	}
	return defaultCasting[model.TierStandard]
}

// Secret helpers for encrypted settings fields.
func (a *App) Encrypt(s string) string {
	v, _ := secure.Encrypt(a.Cfg.SecretKey, s)
	return v
}

func (a *App) Decrypt(s string) string {
	v, _ := secure.Decrypt(a.Cfg.SecretKey, s)
	return v
}
