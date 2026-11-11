package model

import "time"

const (
	ScopeSystem   = "system"
	ScopeStudio   = "studio"
	ScopeUser     = "user"
	ScopePlatform = "platform"

	VoicePreset   = "preset"
	VoiceDesigned = "designed"
	VoiceCloned   = "cloned"
	VoiceTuned    = "tuned"

	VoiceReviewing = "reviewing"
	VoiceTraining  = "training"
	VoiceReady     = "ready"
	VoiceFailed    = "failed"
	VoiceExpired   = "expired"
	VoiceDisabled  = "disabled"
)

type VoiceDefaults struct {
	Speed   float64 `json:"speed"`
	Pitch   float64 `json:"pitch"`
	Volume  float64 `json:"volume"`
	Emotion string  `json:"emotion"`
}

type Voice struct {
	Base
	Scope             string              `gorm:"size:16;index" json:"scope"`
	StudioID          string              `gorm:"size:36;index" json:"studioId"`
	OwnerUserID       string              `gorm:"size:36;index" json:"ownerUserId"`
	Name              string              `gorm:"size:64" json:"name"`
	Origin            string              `gorm:"size:16" json:"origin"`
	Provider          string              `gorm:"size:32;index" json:"provider"`
	ProviderAccountID string              `gorm:"size:36" json:"providerAccountId"`
	ProviderVoiceID   string              `gorm:"size:128" json:"providerVoiceId"`
	Model             string              `gorm:"size:64" json:"model"`
	Language          string              `gorm:"size:16" json:"language"`
	Gender            string              `gorm:"size:16;index" json:"gender"`
	AgeGroup          string              `gorm:"size:16" json:"ageGroup"`
	Tags              StringList          `json:"tags"`
	Description       string              `gorm:"size:1000" json:"description"`
	DesignPrompt      string              `gorm:"size:1000" json:"designPrompt"`
	SampleAssetID     string              `gorm:"size:36" json:"sampleAssetId"`
	SampleURL         string              `gorm:"size:500" json:"-"` // official remote preview (built-in platform voices)
	SampleText        string              `gorm:"size:500" json:"sampleText"`
	Defaults          JSON[VoiceDefaults] `json:"defaults"`
	Emotions          StringList          `json:"emotions"`
	PriceTier         string              `gorm:"size:16" json:"priceTier"`
	Status            string              `gorm:"size:16;index" json:"status"`
	StatusNote        string              `gorm:"size:255" json:"statusNote"`
	ConsentID         string              `gorm:"size:36" json:"consentId"`
	ExpiresAt         *time.Time          `json:"expiresAt"`
	Listed            bool                `json:"listed"`
	Recommended       bool                `json:"recommended"`
	SortOrder         int                 `json:"sortOrder"`
	Shared            bool                `json:"shared"`    // personal voice shared to its studio
	CrowdPool         bool                `json:"crowdPool"` // eligible for crowd/extra rotation
	ParentVoiceID     string              `gorm:"size:36" json:"parentVoiceId"`
	UsageCount        int                 `json:"usageCount"`
	CloneTier         string              `gorm:"size:16" json:"cloneTier"` // quick | pro
	TrainingJobID     string              `gorm:"size:36" json:"trainingJobId"`
	CreatedBy         string              `gorm:"size:36" json:"createdBy"`
}

type VoiceConsent struct {
	Base
	VoiceID       string     `gorm:"size:36;index" json:"voiceId"`
	Type          string     `gorm:"size:16" json:"type"` // self | other
	UserID        string     `gorm:"size:36;index" json:"userId"`
	StudioID      string     `gorm:"size:36" json:"studioId"`
	SpeakerName   string     `gorm:"size:64" json:"speakerName"`
	Challenge     string     `gorm:"size:255" json:"challenge"`
	EvidenceAsset string     `gorm:"size:36" json:"evidenceAssetId"`
	DocumentAsset string     `gorm:"size:36" json:"documentAssetId"`
	IdentityInfo  string     `gorm:"size:255" json:"identityInfo"`
	Statement     string     `gorm:"size:1000" json:"statement"`
	Status        string     `gorm:"size:16;index" json:"status"` // pending | approved | rejected
	ReviewerID    string     `gorm:"size:36" json:"reviewerId"`
	ReviewNote    string     `gorm:"size:255" json:"reviewNote"`
	ReviewedAt    *time.Time `json:"reviewedAt"`
}

type VoiceFavorite struct {
	Base
	UserID  string `gorm:"size:36;uniqueIndex:idx_fav_voice" json:"userId"`
	VoiceID string `gorm:"size:36;uniqueIndex:idx_fav_voice" json:"voiceId"`
}

type VoiceListing struct {
	Base
	VoiceID     string `gorm:"size:36;uniqueIndex" json:"voiceId"`
	OwnerUserID string `gorm:"size:36;index" json:"ownerUserId"`
	PricePer1K  int64  `json:"pricePer1k"` // extra credits per 1000 chars
	Commercial  bool   `json:"commercial"`
	Intro       string `gorm:"size:1000" json:"intro"`
	Status      string `gorm:"size:16;index" json:"status"` // pending | listed | delisted | rejected
	Uses        int64  `json:"uses"`
	Earned      int64  `json:"earned"`
}

type Withdrawal struct {
	Base
	UserID  string `gorm:"size:36;index" json:"userId"`
	Amount  int64  `json:"amount"`
	Status  string `gorm:"size:16" json:"status"` // pending | done | rejected
	Note    string `gorm:"size:255" json:"note"`
	Account string `gorm:"size:255" json:"account"`
}

// ---------- Assets ----------

const (
	AssetBGM        = "bgm"
	AssetSFX        = "sfx"
	AssetAmbience   = "ambience"
	AssetImage      = "image"
	AssetSample     = "voice_sample"
	AssetTake       = "take"
	AssetRender     = "render"
	AssetRecording  = "recording"
	AssetDocument   = "document"
	AssetExport     = "export"
	AssetPreview    = "preview"
	AssetAttachment = "attachment"
	AssetJingle     = "jingle" // fixed intro/outro audio
)

// LibraryKinds are the asset kinds shown in the asset library.
var LibraryKinds = []string{AssetBGM, AssetSFX, AssetAmbience, AssetImage}

type Asset struct {
	Base
	Scope       string     `gorm:"size:16;index" json:"scope"`
	StudioID    string     `gorm:"size:36;index" json:"studioId"`
	OwnerUserID string     `gorm:"size:36;index" json:"ownerUserId"`
	Kind        string     `gorm:"size:24;index" json:"kind"`
	Name        string     `gorm:"size:200" json:"name"`
	StorageKey  string     `gorm:"size:512" json:"-"`
	Mime        string     `gorm:"size:64" json:"mime"`
	Size        int64      `json:"size"`
	DurationMs  int64      `json:"durationMs"`
	SampleRate  int        `json:"sampleRate"`
	Channels    int        `json:"channels"`
	Checksum    string     `gorm:"size:64;index" json:"checksum"`
	Tags        StringList `json:"tags"`
	License     string     `gorm:"size:32" json:"license"` // commercial | platform | attribution
	Origin      string     `gorm:"size:16" json:"origin"`  // upload | ai | system | generated
	PeaksKey    string     `gorm:"size:512" json:"-"`
	Listed      bool       `json:"listed"`
	Description string     `gorm:"size:500" json:"description"`
	Meta        Map        `json:"meta"`
	CreatedBy   string     `gorm:"size:36" json:"createdBy"`
}

type AssetFavorite struct {
	Base
	UserID  string `gorm:"size:36;uniqueIndex:idx_fav_asset" json:"userId"`
	AssetID string `gorm:"size:36;uniqueIndex:idx_fav_asset" json:"assetId"`
}

// Intro/outro template modes.
const (
	TplCompose = "compose" // music bed + announced text, rendered per chapter (placeholders allowed)
	TplAudio   = "audio"   // one fixed audio file, identical in every chapter
)

// AudioTemplate is an intro/outro template: either music bed + announced text, or a fixed audio file.
// In audio mode the compose fields are kept as the recipe the fixed audio was generated from.
type AudioTemplate struct {
	Base
	Scope        string  `gorm:"size:16;index" json:"scope"`
	StudioID     string  `gorm:"size:36;index" json:"studioId"`
	Kind         string  `gorm:"size:16" json:"kind"` // intro | outro
	Name         string  `gorm:"size:64" json:"name"`
	Mode         string  `gorm:"size:16" json:"mode"` // compose (default) | audio
	AudioAssetID string  `gorm:"size:36" json:"audioAssetId"`
	AudioGain    float64 `json:"audioGain"` // dB on top of automatic level matching
	MusicAssetID string  `gorm:"size:36" json:"musicAssetId"`
	Text         string  `gorm:"size:1000" json:"text"`
	VoiceID      string  `gorm:"size:36" json:"voiceId"`
	MusicVolume  float64 `json:"musicVolume"` // dB
	LeadMs       int64   `json:"leadMs"`      // music before speech
	TailMs       int64   `json:"tailMs"`      // music after speech
	Disclaimer   bool    `json:"disclaimer"`  // append AI-generated content statement
	CreatedBy    string  `gorm:"size:36" json:"createdBy"`
}

type EffectPreset struct {
	Base
	Scope    string     `gorm:"size:16;index" json:"scope"`
	StudioID string     `gorm:"size:36;index" json:"studioId"`
	Name     string     `gorm:"size:64" json:"name"`
	Effects  StringList `json:"effects"`
	Note     string     `gorm:"size:255" json:"note"`
}

type QuickTTS struct {
	Base
	UserID     string            `gorm:"size:36;index" json:"userId"`
	StudioID   string            `gorm:"size:36;index" json:"studioId"`
	Title      string            `gorm:"size:100" json:"title"`
	Lines      JSON[[]QuickLine] `json:"lines"`
	AssetID    string            `gorm:"size:36" json:"assetId"`
	DurationMs int64             `json:"durationMs"`
	Status     string            `gorm:"size:16" json:"status"`
	Credits    int64             `json:"credits"`
	Error      string            `gorm:"size:255" json:"error"`
}

type QuickLine struct {
	Speaker string   `json:"speaker"`
	VoiceID string   `json:"voiceId"`
	Text    string   `json:"text"`
	Emotion string   `json:"emotion"`
	Speed   *float64 `json:"speed,omitempty"`
}

// VoiceDesign keeps candidate previews generated from a text description.
type VoiceDesign struct {
	Base
	UserID      string                  `gorm:"size:36;index" json:"userId"`
	StudioID    string                  `gorm:"size:36" json:"studioId"`
	Prompt      string                  `gorm:"size:1000" json:"prompt"`
	Provider    string                  `gorm:"size:32" json:"provider"`
	AccountID   string                  `gorm:"size:36" json:"accountId"`
	PreviewText string                  `gorm:"size:500" json:"previewText"`
	Status      string                  `gorm:"size:16" json:"status"`
	Candidates  JSON[[]DesignCandidate] `json:"candidates"`
	Attributes  Map                     `json:"attributes"`
	Error       string                  `gorm:"size:255" json:"error"`
	JobID       string                  `gorm:"size:36" json:"jobId"`
}

type DesignCandidate struct {
	ProviderVoiceID string `json:"providerVoiceId"`
	PreviewAssetID  string `json:"previewAssetId"`
	Seed            int    `json:"seed"`
	Saved           bool   `json:"saved"`
	Model           string `json:"model,omitempty"`     // model the voice is bound to (Qianwen)
	PriceTier       string `json:"priceTier,omitempty"` // tier of synthesizing with it
	Discarded       bool   `json:"discarded,omitempty"` // not saved in time: removed from the platform
}
