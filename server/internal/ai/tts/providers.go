package tts

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"voicebook/internal/audio"
)

// ---------------------------------------------------------------------------
// Built-in placeholder engine
// ---------------------------------------------------------------------------

type placeholder struct{}

func init() {
	Register("placeholder", func(Account) (Provider, error) { return placeholder{}, nil })
	for _, s := range stubs {
		s := s
		Register(s.name, func(Account) (Provider, error) { return s, nil })
	}
}

var allEmotions = []string{"neutral", "happy", "sad", "angry", "fearful", "surprised", "disgusted", "gentle", "nervous", "tired", "whisper", "shout"}

func (placeholder) Name() string { return "placeholder" }

func (placeholder) Capabilities() Capabilities {
	return Capabilities{
		Emotion: true, Emotions: allEmotions, InlineTags: true, Instruction: true, Phoneme: true,
		VoiceDesign: true, QuickClone: true, ProClone: true, VoiceChange: true, Dialogue: true,
		MaxChars: 5000, Languages: []string{"zh-CN", "en-US"},
	}
}

func encode(p *audio.PCM) (*Audio, error) {
	var buf bytes.Buffer
	if err := audio.EncodeWAV16(&buf, p); err != nil {
		return nil, err
	}
	return &Audio{Data: buf.Bytes(), Format: "wav", SampleRate: p.Rate}, nil
}

func (placeholder) Synthesize(ctx context.Context, r SynthesizeRequest) (*Audio, error) {
	if utf8.RuneCountInString(r.Text) > 5000 {
		return nil, fmt.Errorf("单次合成文本过长（最多 5000 字）")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p := audio.Babble(r.Text, audio.VoiceStyle{
		Gender: r.VoiceGender, AgeGroup: r.VoiceAge, Seed: audio.Seed(r.VoiceSeed, r.ProviderVoiceID),
		Speed: r.Speed, Pitch: r.Pitch, Volume: r.Volume, Emotion: r.Emotion, Intensity: r.Intensity,
	})
	return encode(p)
}

func (placeholder) ListVoices(context.Context) ([]RemoteVoice, error) {
	return nil, nil // placeholder voices are seeded into the official library
}

func (placeholder) DesignVoice(_ context.Context, r DesignRequest) ([]DesignCandidate, error) {
	n := r.Count
	if n <= 0 {
		n = 3
	}
	text := r.PreviewText
	if text == "" {
		text = "你好，很高兴认识你。这是根据你的描述生成的声音，听听看是否合适。"
	}
	var out []DesignCandidate
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ph-design-%d", audio.Seed(r.Seed, r.Description, fmt.Sprint(i))%1_000_000_000)
		p := audio.Babble(text, audio.VoiceStyle{Gender: r.Gender, AgeGroup: r.AgeGroup, Seed: audio.Seed(id), Speed: 1, Pitch: float64(i-1) * 1.5})
		a, err := encode(p)
		if err != nil {
			return nil, err
		}
		out = append(out, DesignCandidate{ProviderVoiceID: id, Preview: a})
	}
	return out, nil
}

func (placeholder) CloneVoice(_ context.Context, r CloneRequest) (*CloneResult, error) {
	id := fmt.Sprintf("ph-clone-%d", audio.Seed(r.Seed, strings.Join(r.SamplePaths, ","))%1_000_000_000)
	return &CloneResult{ProviderVoiceID: id, Status: "ready", Message: "占位引擎已生成克隆音色（未进行真实训练）"}, nil
}

func (placeholder) CloneStatus(_ context.Context, id string) (*CloneResult, error) {
	return &CloneResult{ProviderVoiceID: id, Status: "ready"}, nil
}

func (placeholder) DeleteVoice(context.Context, string) error { return nil }

func (p placeholder) ConvertVoice(ctx context.Context, r VoiceChangeRequest) (*Audio, error) {
	return p.Synthesize(ctx, SynthesizeRequest{Text: r.Text, ProviderVoiceID: r.ProviderVoiceID, VoiceGender: r.VoiceGender, VoiceAge: r.VoiceAge, VoiceSeed: r.VoiceSeed, Speed: 1})
}

func (p placeholder) SynthesizeDialogue(ctx context.Context, turns []DialogueTurn) (*Audio, error) {
	var all []float32
	for _, t := range turns {
		a, err := p.Synthesize(ctx, t.SynthesizeRequest)
		if err != nil {
			return nil, err
		}
		pcm, err := audio.DecodeWAV(bytes.NewReader(a.Data))
		if err != nil {
			return nil, err
		}
		all = append(all, pcm.Data...)
		all = append(all, make([]float32, audio.SampleRate/3)...)
	}
	return encode(&audio.PCM{Rate: audio.SampleRate, Channels: 1, Data: all})
}

// ---------------------------------------------------------------------------
// Reserved third-party adapters. Capabilities describe the platform so the UI can adapt;
// the API calls themselves are intentionally not implemented yet.
// ---------------------------------------------------------------------------

type stub struct {
	name string
	caps Capabilities
}

func (s stub) Name() string               { return s.name }
func (s stub) Capabilities() Capabilities { return s.caps }
func (s stub) Synthesize(context.Context, SynthesizeRequest) (*Audio, error) {
	return nil, ErrNotImplemented
}
func (s stub) ListVoices(context.Context) ([]RemoteVoice, error) { return nil, ErrNotImplemented }
func (s stub) DesignVoice(context.Context, DesignRequest) ([]DesignCandidate, error) {
	return nil, ErrNotImplemented
}
func (s stub) CloneVoice(context.Context, CloneRequest) (*CloneResult, error) {
	return nil, ErrNotImplemented
}
func (s stub) CloneStatus(context.Context, string) (*CloneResult, error) {
	return nil, ErrNotImplemented
}
func (s stub) DeleteVoice(context.Context, string) error { return ErrNotImplemented }
func (s stub) ConvertVoice(context.Context, VoiceChangeRequest) (*Audio, error) {
	return nil, ErrNotImplemented
}

var stubs = []stub{
	{"volcengine", Capabilities{Emotion: true, Emotions: allEmotions, SSML: true, Phoneme: true, QuickClone: true, MaxChars: 1024, Languages: []string{"zh-CN", "en-US"}}},
	{"elevenlabs", Capabilities{InlineTags: true, PronunciationDict: true, VoiceDesign: true, QuickClone: true, ProClone: true, VoiceChange: true, Dialogue: true, MaxChars: 5000, Languages: []string{"zh-CN", "en-US", "ja-JP"}}},
	{"openai", Capabilities{Instruction: true, MaxChars: 4096, Languages: []string{"zh-CN", "en-US"}}},
	{"azure", Capabilities{Emotion: true, Emotions: allEmotions, SSML: true, Phoneme: true, MaxChars: 5000, Languages: []string{"zh-CN", "en-US"}}},
	{"selfhost", Capabilities{Emotion: true, Emotions: allEmotions, Instruction: true, QuickClone: true, MaxChars: 1000, Languages: []string{"zh-CN", "en-US"}}},
}
