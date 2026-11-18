package gen

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"voicebook/internal/ai/volc"
)

// Volcengine audio generation (豆包音频生成, seed-audio-1.0) used for sound effects and ambience. Same endpoint as
// the voice adapter (POST {base}/api/v3/tts/create); the prompt is Chinese or English natural language and may
// refer to up to three reference recordings as @音频1…@音频3 ("提示音": imitate, continue or vary a sound).
// One request yields at most 120 seconds.

const VolcSFXMaxSeconds = 120

type volcSFX struct {
	client volc.AudioClient
	pure   bool
}

func newVolcSFX(a Account) *volcSFX {
	model := strings.TrimSpace(a.Model)
	if model == "" {
		model = "seed-audio-1.0"
	}
	v := &volcSFX{client: volc.AudioClient{Base: volc.Base(a.BaseURL), Key: strings.TrimSpace(a.APIKey), AppID: volc.AppID(a.Config), Model: model,
		Client: &http.Client{Timeout: 5 * time.Minute}, RetryWait: 2 * time.Second}, pure: true}
	if b, ok := a.Config["sfxOnly"].(bool); ok {
		v.pure = b
	}
	return v
}

func (v *volcSFX) Name() string { return "volcengine_audio" }

func (v *volcSFX) Check(ctx context.Context) (string, error) { return v.client.Check(ctx) }

// wantsVoiceOrMusic: the description asks for speech or music itself, so the "effects only" hint is left out.
// Only the words decide: naming a reference clip (@音频1) or picking one without naming it gives the same prompt.
func wantsVoiceOrMusic(p string) bool {
	for _, k := range []string{"“", "\"", "说", "台词", "对白", "人声", "唱", "音乐", "旋律", "配乐", "voice", "music", "says", "sing"} {
		if strings.Contains(strings.ToLower(p), k) {
			return true
		}
	}
	return false
}

// VolcSFXPrompt adds length, looping and references to the user's description (exported for tests).
func VolcSFXPrompt(prompt string, durationMs int, loop bool, refs int, pure bool) string {
	p := strings.TrimRight(strings.TrimSpace(prompt), "。.，, ")
	if refs > 0 && !strings.Contains(p, "@音频") {
		names := make([]string, refs)
		for i := range names {
			names[i] = fmt.Sprintf("@音频%d", i+1)
		}
		p = "参考" + strings.Join(names, "、") + "的声音，" + p
	}
	var extra []string
	if pure && !wantsVoiceOrMusic(prompt) {
		extra = append(extra, "只有音效和环境声，没有人声和背景音乐")
	}
	if durationMs > 0 {
		secs := math.Round(math.Min(float64(durationMs)/1000, VolcSFXMaxSeconds)*10) / 10
		extra = append(extra, fmt.Sprintf("总时长约 %g 秒", secs))
	}
	if loop {
		extra = append(extra, "声音平稳持续，结尾能自然接回开头，适合循环播放")
	}
	if len(extra) > 0 {
		p += "。" + strings.Join(extra, "，")
	}
	return p + "。"
}

func (v *volcSFX) GenerateSFX(ctx context.Context, r SFXRequest) (*File, error) {
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, errors.New("请描述想要的声音")
	}
	if len(r.References) > 3 {
		return nil, errors.New("最多使用 3 段参考音频")
	}
	prompt := VolcSFXPrompt(r.Prompt, r.DurationMs, r.Loop, len(r.References), v.pure)
	if len([]rune(prompt)) > 3000 {
		return nil, errors.New("描述太长了，火山引擎音频生成最多支持 3000 个字符")
	}
	body := map[string]any{"text_prompt": prompt, "audio_config": map[string]any{"format": "mp3", "sample_rate": 44100}}
	if len(r.References) > 0 {
		var refs []map[string]any
		for _, b := range r.References {
			if len(b) > 10<<20 {
				return nil, errors.New("参考音频超过 10 MB")
			}
			refs = append(refs, map[string]any{"audio_data": base64.StdEncoding.EncodeToString(b)})
		}
		body["references"] = refs
	}
	data, secs, err := v.client.Create(ctx, body)
	if err != nil {
		return nil, err
	}
	if len(data) < 256 {
		return nil, fmt.Errorf("火山引擎没有返回有效的音频（%d 字节）", len(data))
	}
	meta := map[string]any{"provider": "volcengine_audio", "model": v.client.Model, "prompt": r.Prompt, "providerPrompt": prompt}
	if secs > 0 {
		meta["billedSeconds"] = secs
	}
	if len(r.References) > 0 {
		meta["references"] = len(r.References)
	}
	return &File{Data: data, Mime: "audio/mpeg", Ext: "mp3", Meta: meta}, nil
}
