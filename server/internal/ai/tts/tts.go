// Package tts defines the speech synthesis provider interfaces (synthesis, voice design,
// cloning, voice changing, multi-speaker dialogue). Qianwen (qwen.go) and MiniMax (minimax.go) are implemented;
// other platforms are reserved stubs, and the built-in placeholder engine keeps every workflow runnable.
package tts

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

var ErrNotImplemented = errors.New("该平台的语音合成适配器为预留接口，尚未实现，请在管理端选择其他平台或接入实现")

type Capabilities struct {
	Emotion           bool     `json:"emotion"`
	Emotions          []string `json:"emotions"`
	InlineTags        bool     `json:"inlineTags"`
	TagKeys           []string `json:"tagKeys,omitempty"` // when set, only these performance tags render inline
	Instruction       bool     `json:"instruction"`
	SSML              bool     `json:"ssml"`
	Phoneme           bool     `json:"phoneme"`
	PronunciationDict bool     `json:"pronunciationDict"`
	VoiceDesign       bool     `json:"voiceDesign"`
	QuickClone        bool     `json:"quickClone"`
	ProClone          bool     `json:"proClone"`
	VoiceChange       bool     `json:"voiceChange"`
	Dialogue          bool     `json:"dialogue"`
	Streaming         bool     `json:"streaming"`
	MaxChars          int      `json:"maxChars"`
	Languages         []string `json:"languages"`
	CloneExpiryDays   int      `json:"cloneExpiryDays"`
	// CloneExpiresUnused: the platform deletes a clone that is not used within CloneExpiryDays, and its first
	// synthesis is billed (activation). Clones are then kept, and no sample is synthesized right after cloning.
	CloneExpiresUnused bool `json:"cloneExpiresUnused,omitempty"`
	CloneMinSeconds    int  `json:"cloneMinSeconds,omitempty"` // sample length the platform accepts
	CloneMaxSeconds    int  `json:"cloneMaxSeconds,omitempty"`
	ServerDenoise      bool `json:"serverDenoise,omitempty"` // the platform denoises clone samples itself
	DesignPromptMax    int  `json:"designPromptMax,omitempty"` // longest voice description the platform accepts (characters)
}

type Pron struct {
	Offset    int    `json:"offset"`
	Char      string `json:"char"`
	Pinyin    string `json:"pinyin"`
	Homophone string `json:"homophone"`
}

type SynthesizeRequest struct {
	Text            string
	ProviderVoiceID string
	Model           string
	Language        string
	Emotion         string
	Intensity       int
	Speed           float64 // 1.0 = normal
	Pitch           float64 // semitones
	Volume          float64 // dB
	Instruction     string
	Tags            []string // earlier whole-line performance tags (Chinese), rendered at the start
	Cues            []Cue    // positional performance tags (offsets into Text)
	Prons           []Pron
	SampleRate      int
	Seed            int // 0 = provider default; set a random value to get a different rendition on re-generation
	// Voice metadata (used by the placeholder engine; real providers ignore it).
	VoiceGender string
	VoiceAge    string
	VoiceSeed   string
	Extra       map[string]any
}

type Audio struct {
	Data       []byte
	Format     string // wav | mp3 | pcm
	SampleRate int
}

type RemoteVoice struct {
	ProviderVoiceID string   `json:"providerVoiceId"`
	Name            string   `json:"name"`
	Gender          string   `json:"gender"`
	AgeGroup        string   `json:"ageGroup"`
	Language        string   `json:"language"`
	Tags            []string `json:"tags"`
	Description     string   `json:"description"`
	PreviewURL      string   `json:"previewUrl"`
	Model           string   `json:"model"`
	PriceTier       string   `json:"priceTier"`
	Recommended     bool     `json:"recommended"`
	CrowdPool       bool     `json:"crowdPool"`
	PreviewFile     string   `json:"previewFile"`
	Base            bool     `json:"base"`
}

type DesignRequest struct {
	Description string
	PreviewText string
	Gender      string
	AgeGroup    string
	Count       int
	Seed        string
}

type DesignCandidate struct {
	ProviderVoiceID string
	Preview         *Audio
	Model           string // model the designed voice is bound to ("" = any)
	PriceTier       string
}

type CloneRequest struct {
	Name        string
	SamplePaths []string // original uploads (any format)
	MergedPath  string   // all samples merged into one mono WAV (32 kHz, at most 290 s)
	Tier        string   // quick | pro
	Language    string
	Gender      string
	AgeGroup    string
	Seed        string // stable per voice; used to derive the platform voice id
	Denoise     bool
	PreviewText string // a line the platform reads with the new voice, when it offers a preview
}

type CloneResult struct {
	ProviderVoiceID string
	Status          string // training | ready | failed
	Message         string
	ExpiresInDays   int
	Preview         *Audio // preview read by the platform with the new voice, if any
	Model           string // model the voice is bound to ("" = any model of the account)
	PriceTier       string // price tier of synthesizing with the voice ("" = keep the default)
}

type VoiceChangeRequest struct {
	SourcePath      string
	Text            string
	ProviderVoiceID string
	VoiceGender     string
	VoiceAge        string
	VoiceSeed       string
}

type DialogueTurn struct {
	SynthesizeRequest
}

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Synthesize(ctx context.Context, req SynthesizeRequest) (*Audio, error)
}

// VoiceCapabilityProvider is implemented by platforms whose features differ per model or voice.
type VoiceCapabilityProvider interface {
	VoiceCapabilities(model, voiceID string) Capabilities
}

// CapsFor returns what a concrete voice supports on a provider.
func CapsFor(p Provider, model, voiceID string) Capabilities {
	if vc, ok := p.(VoiceCapabilityProvider); ok {
		return vc.VoiceCapabilities(model, voiceID)
	}
	return p.Capabilities()
}

type VoiceLister interface {
	ListVoices(ctx context.Context) ([]RemoteVoice, error)
}

type VoiceDesigner interface {
	DesignVoice(ctx context.Context, req DesignRequest) ([]DesignCandidate, error)
}

type VoiceCloner interface {
	CloneVoice(ctx context.Context, req CloneRequest) (*CloneResult, error)
	CloneStatus(ctx context.Context, providerVoiceID string) (*CloneResult, error)
	DeleteVoice(ctx context.Context, providerVoiceID string) error
}

type VoiceChanger interface {
	ConvertVoice(ctx context.Context, req VoiceChangeRequest) (*Audio, error)
}

// VoiceprintVerifier compares two recordings and returns speaker similarity (0..1).
// Reserved for platforms that offer speaker verification; used to confirm self-cloning consent.
type VoiceprintVerifier interface {
	CompareSpeakers(ctx context.Context, pathA, pathB string) (float64, error)
}

type DialogueSynthesizer interface {
	SynthesizeDialogue(ctx context.Context, turns []DialogueTurn) (*Audio, error)
}

// Account carries the decrypted configuration of a provider account.
type Account struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Config   map[string]any
	RPM      int // requests per minute allowed by the account (0 = adapter default)
}

type Factory func(Account) (Provider, error)

var registry = map[string]Factory{}

func Register(name string, f Factory) { registry[name] = f }

func New(acc Account) (Provider, error) {
	f, ok := registry[acc.Provider]
	if !ok {
		return nil, fmt.Errorf("未知的语音平台：%s", acc.Provider)
	}
	return f(acc)
}

func Names() []string {
	var out []string
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
