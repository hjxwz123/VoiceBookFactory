package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"voicebook/internal/ai/volc"
	"voicebook/internal/audio"
)

// Volcengine audio generation (豆包音频生成, model seed-audio-1.0), official API:
//
//	POST {base}/api/v3/tts/create
//	X-Api-Key: <key>        (old console: X-Api-App-Id + X-Api-Access-Key)
//	{"model": "seed-audio-1.0", "text_prompt": "...", "references": [{"speaker": "zh_female_vv_uranus_bigtts"}],
//	 "audio_config": {"format": "wav", "sample_rate": 44100, "speech_rate": 0, "pitch_rate": 0, "loudness_rate": 0}}
//
// The reply is JSON with the audio in base64 (`audio`) and its length (`original_duration`, the billed seconds,
// at most 120). The prompt is natural language: it describes the voice, the delivery, the scene, and carries the
// words to speak in quotes. A voice is chosen three ways:
//   - a preset voice: references[0].speaker (the 豆包语音合成模型2.0 voices, volc_voices_gen.go);
//   - a reference recording ("提示音"): references[0].audio_data, named @音频1 in the prompt. Cloned and designed
//     voices work this way: the recording is kept on this site (Account.Refs) and sent with every request, so no
//     voice has to be trained or stored on the platform;
//   - text only: no reference, the prompt describes the voice (used to design voices).

const (
	VolcAudioDefaultModel = "seed-audio-1.0"
	volcRefPrefix         = "ref:"
	volcPromptMax         = 3000
	volcRefMaxMs          = 29_500 // the platform takes reference clips of at most 30 s
)

// VolcVoice is one 豆包语音合成模型2.0 voice usable as `speaker`.
type VolcVoice struct {
	ID, Name, Gender, AgeGroup, Language string
	Tags                                 []string
	Description                          string
	Recommended, CrowdPool               bool
}

func VolcCatalog() []VolcVoice { return volcVoices }

var volcIndex = func() map[string]*VolcVoice {
	m := make(map[string]*VolcVoice, len(volcVoices))
	for i := range volcVoices {
		m[volcVoices[i].ID] = &volcVoices[i]
	}
	return m
}()

// VolcLookup finds a preset voice by id (nil for reference voices and unknown ids).
func VolcLookup(id string) *VolcVoice { return volcIndex[id] }

func (v VolcVoice) Remote() RemoteVoice {
	return RemoteVoice{ProviderVoiceID: v.ID, Name: v.Name, Gender: v.Gender, AgeGroup: v.AgeGroup, Language: v.Language, Tags: v.Tags,
		Description: v.Description, Recommended: v.Recommended, CrowdPool: v.CrowdPool, PriceTier: "standard", Model: VolcAudioDefaultModel}
}

type volcAudio struct {
	acc        Account
	base, key  string
	appID      string
	model      string
	sampleRate int
	maxChars   int
	pure       bool
	client     *http.Client
	retryWait  time.Duration
}

func newVolcAudio(acc Account) (Provider, error) {
	v := &volcAudio{acc: acc, base: volc.Base(acc.BaseURL), key: strings.TrimSpace(acc.APIKey), appID: volc.AppID(acc.Config), model: strings.TrimSpace(acc.Model),
		sampleRate: 44100, maxChars: 280, pure: true, client: &http.Client{Timeout: 5 * time.Minute}, retryWait: 2 * time.Second}
	if v.model == "" {
		v.model = VolcAudioDefaultModel
	}
	if n, ok := acc.Config["maxChars"].(float64); ok && n >= 50 && n <= 1000 {
		v.maxChars = int(n)
	}
	if b, ok := acc.Config["pureVoice"].(bool); ok {
		v.pure = b
	}
	if n, ok := acc.Config["sampleRate"].(float64); ok && volcRateOK(int(n)) {
		v.sampleRate = int(n)
	}
	return v, nil
}

func volcRateOK(r int) bool {
	switch r {
	case 8000, 16000, 24000, 32000, 44100, 48000:
		return true
	}
	return false
}

func init() { Register("volcengine_audio", newVolcAudio) }

func (v *volcAudio) Name() string          { return "volcengine_audio" }
func (v *volcAudio) BillsByDuration() bool { return true }

func (v *volcAudio) Capabilities() Capabilities {
	return Capabilities{
		Emotion: true, Emotions: allEmotions, InlineTags: true, Instruction: true, MaxChars: 5000, // tags are written into the prompt
		Languages:  []string{"zh-CN", "en-US", "ja-JP", "ko-KR", "es", "de", "fr", "pt", "th", "vi", "ms", "fil", "it", "ru", "nl", "pl", "tr"},
		QuickClone: true, CloneMinSeconds: 5, CloneMaxSeconds: 30,
		VoiceDesign: true, DesignPromptMax: 500,
	}
}

func (v *volcAudio) VoiceCapabilities(string, string) Capabilities { return v.Capabilities() }

func (v *volcAudio) ListVoices(context.Context) ([]RemoteVoice, error) {
	out := make([]RemoteVoice, 0, len(volcVoices))
	for _, x := range volcVoices {
		out = append(out, x.Remote())
	}
	return out, nil
}

// --- prompt ------------------------------------------------------------------

var volcEmotionEN = map[string]string{
	"sad": "sad", "angry": "angry", "happy": "happy", "surprised": "surprised", "fearful": "frightened", "disgusted": "disgusted", "tired": "tired",
	"whisper": "whispering", "shout": "shouting", "nervous": "nervous", "gentle": "gentle",
}

var volcStyleEN = map[string]string{
	"very slowly": "very slowly", "very fast": "very fast", "asmr": "soft ASMR voice", "whispers": "whispering", "shouting": "shouting",
	"crying": "crying", "reluctantly": "reluctantly", "trembling": "trembling voice", "like dracula": "eerie and sinister",
}

func volcChinese(lang string) bool {
	l := strings.ToLower(lang)
	return l == "" || strings.HasPrefix(l, "zh") || strings.HasPrefix(l, "yue")
}

// volcSoundZH / volcSoundEN say what a sound tag does, as an action between the quoted words.
var volcSoundZH = map[string]string{
	"laughing": "笑出声来", "giggles": "轻笑了一声", "snorts": "冷笑了一声", "sighing": "叹了口气", "gasp": "倒吸一口气",
	"cough": "咳嗽了一声", "clears throat": "清了清嗓子",
}

var volcSoundEN = map[string]string{
	"laughing": "laughs out loud", "giggles": "giggles", "snorts": "snorts", "sighing": "sighs", "gasp": "gasps",
	"cough": "coughs", "clears throat": "clears their throat",
}

func volcTagWords(t PerfTag, zh bool) string {
	if t.Kind == "sound" {
		if zh {
			if w, ok := volcSoundZH[t.Key]; ok {
				return w
			}
			return t.Label
		}
		if w, ok := volcSoundEN[t.Key]; ok {
			return w
		}
		return t.Key
	}
	if zh {
		return strings.TrimPrefix(strings.TrimSuffix(stylePhrase(t), "说"), "用")
	}
	if w, ok := volcStyleEN[t.Key]; ok {
		return w
	}
	return t.Key
}

// volcDirection is how the whole line is delivered: its emotion and strength, the style tags at its start and
// the free-form direction, in the prompt's language.
func volcDirection(r SynthesizeRequest, styles []string, zh bool) []string {
	var parts []string
	if r.Emotion != "" && r.Emotion != "neutral" {
		if zh {
			if w, ok := emotionWords[r.Emotion]; ok {
				parts = append(parts, w)
			}
		} else if w, ok := volcEmotionEN[r.Emotion]; ok {
			parts = append(parts, w)
		}
		if r.Intensity >= 3 {
			parts = append(parts, map[bool]string{true: "情绪饱满强烈", false: "with strong emotion"}[zh])
		}
	}
	parts = append(parts, styles...)
	if s := strings.TrimSpace(r.Instruction); s != "" {
		parts = append(parts, strings.TrimRight(s, "。.，, "))
	}
	return dedupe(parts)
}

// volcStretch is a run of words between performance tags: the sounds made just before it and the delivery it
// starts with.
type volcStretch struct {
	text           string
	sounds, styles []string
}

// volcLineCues returns the line's cues in order; whole-line tags from older scripts count as cues at the start.
func volcLineCues(r SynthesizeRequest) []Cue {
	cues := append([]Cue{}, r.Cues...)
	for _, t := range r.Tags {
		if k, ok := LegacyTagMap[t]; ok {
			cues = append(cues, Cue{At: 0, Tag: k})
		} else if pt, ok := LookupPerfTag(t); ok {
			cues = append(cues, Cue{At: 0, Tag: pt.Key})
		}
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].At < cues[j].At })
	return cues
}

// volcStretches cuts the line at its tags. Sounds at the very end come back separately; a style tag with no
// words after it has nothing to change and is dropped.
func volcStretches(r SynthesizeRequest, zh bool) ([]volcStretch, []string) {
	text := []rune(r.Text)
	var out []volcStretch
	var cur volcStretch
	pos := 0
	for _, c := range volcLineCues(r) {
		t, ok := LookupPerfTag(c.Tag)
		if !ok {
			continue
		}
		at := min(max(c.At, 0), len(text))
		if at > pos {
			if words := strings.TrimSpace(string(text[pos:at])); words != "" {
				cur.text = words
				out = append(out, cur)
				cur = volcStretch{}
			}
			pos = at
		}
		w := volcTagWords(t, zh)
		if t.Kind == "sound" {
			cur.sounds = append(cur.sounds, w)
		} else {
			cur.styles = append(cur.styles, w)
		}
	}
	if words := strings.TrimSpace(string(text[pos:])); words != "" {
		cur.text = words
		out = append(out, cur)
		cur = volcStretch{}
	}
	if len(out) == 0 {
		out = []volcStretch{{text: strings.TrimSpace(r.Text)}}
	}
	return out, dedupe(cur.sounds)
}

// volcQuoteZH keeps the line's own quotation marks from closing the quote around it.
func volcQuoteZH(s string) string {
	s = strings.NewReplacer("“", "「", "”", "」").Replace(s)
	open := true
	return strings.Map(func(r rune) rune {
		if r != '"' {
			return r
		}
		open = !open
		if open {
			return '」'
		}
		return '「'
	}, s)
}

// BuildVolcPrompt writes the natural-language prompt for one line (exported for tests). who names the voice
// ("@音频1" for a reference recording, "" for a preset voice). The words go in quotes; performance tags inside
// the line become actions and changes of delivery between quoted stretches, the way the platform's examples
// describe a scene:
//
//	（悲伤）说道：“你真傻。”轻笑了一声，接着（带着哭腔）说：“我早就知道了。”说完叹了口气。
func BuildVolcPrompt(r SynthesizeRequest, who string, pure bool) string {
	zh := volcChinese(r.Language)
	parts, trailing := volcStretches(r, zh)
	dir := volcDirection(r, parts[0].styles, zh)
	var b strings.Builder
	if zh {
		if pure {
			b.WriteString("纯人声，无背景音乐和音效。")
		}
		b.WriteString(who)
		if len(dir) > 0 {
			b.WriteString("（" + strings.Join(dir, "，") + "）")
		}
		if s := dedupe(parts[0].sounds); len(s) > 0 {
			b.WriteString(strings.Join(s, "，") + "，")
		}
		b.WriteString("说道：“" + volcQuoteZH(parts[0].text) + "”")
		for _, p := range parts[1:] {
			if s := dedupe(p.sounds); len(s) > 0 {
				b.WriteString(strings.Join(s, "，") + "，")
			}
			b.WriteString("接着")
			if s := dedupe(p.styles); len(s) > 0 {
				b.WriteString("（" + strings.Join(s, "，") + "）")
			}
			b.WriteString("说：“" + volcQuoteZH(p.text) + "”")
		}
		if len(trailing) > 0 {
			b.WriteString("说完" + strings.Join(trailing, "，") + "。")
		}
		return b.String()
	}
	quote := func(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "'") + "\"" }
	if pure {
		b.WriteString("Voice only, no background music or sound effects. ")
	}
	if who != "" {
		b.WriteString(who + " ")
	}
	if len(dir) > 0 {
		b.WriteString("(" + strings.Join(dir, ", ") + ") ")
	}
	if s := dedupe(parts[0].sounds); len(s) > 0 {
		b.WriteString(strings.Join(s, ", ") + ", then ")
	}
	b.WriteString("says: " + quote(parts[0].text))
	for _, p := range parts[1:] {
		b.WriteString(" Then ")
		if s := dedupe(p.sounds); len(s) > 0 {
			b.WriteString(strings.Join(s, ", ") + ", and ")
		}
		b.WriteString("continues")
		if s := dedupe(p.styles); len(s) > 0 {
			b.WriteString(" (" + strings.Join(s, ", ") + ")")
		}
		b.WriteString(": " + quote(p.text))
	}
	if len(trailing) > 0 {
		b.WriteString(" Then " + strings.Join(trailing, ", ") + ".")
	}
	return b.String()
}

// volcPieces cuts a long line into requests of at most maxChars characters (one request yields at most two
// minutes of audio). Each piece gets the cues that fall inside it, and the style in force where it starts.
func volcPieces(r SynthesizeRequest, maxChars int) []SynthesizeRequest {
	texts := splitForVolc(strings.TrimSpace(r.Text), maxChars)
	cues := volcLineCues(r)
	full := r.Text
	starts := make([]int, len(texts))
	cursor := 0 // byte offset in full
	for i, t := range texts {
		if j := strings.Index(full[cursor:], t); j >= 0 {
			cursor += j
		}
		starts[i] = utf8.RuneCountInString(full[:cursor])
		cursor += len(t)
	}
	out := make([]SynthesizeRequest, len(texts))
	var active []Cue // the style tags last set before the current piece
	activeAt, ci := -1, 0
	for i, t := range texts {
		pr := r
		pr.Text, pr.Tags, pr.Cues = t, nil, nil
		n := utf8.RuneCountInString(t)
		var mine []Cue
		for ci < len(cues) && (i == len(texts)-1 || cues[ci].At < starts[i+1]) {
			c := cues[ci]
			mine = append(mine, Cue{At: min(max(c.At-starts[i], 0), n), Tag: c.Tag})
			ci++
		}
		if i > 0 && len(active) > 0 && !volcStyleAtStart(mine) {
			pr.Cues = append(pr.Cues, active...)
		}
		pr.Cues = append(pr.Cues, mine...)
		for _, c := range mine {
			if t, ok := LookupPerfTag(c.Tag); ok && t.Kind == "style" {
				if c.At != activeAt {
					active, activeAt = nil, c.At
				}
				active = append(active, Cue{At: 0, Tag: c.Tag})
			}
		}
		activeAt = -1 // positions restart in the next piece
		out[i] = pr
	}
	return out
}

func volcStyleAtStart(cues []Cue) bool {
	for _, c := range cues {
		if t, ok := LookupPerfTag(c.Tag); ok && t.Kind == "style" && c.At == 0 {
			return true
		}
	}
	return false
}

// splitForVolc cuts long text at sentence ends into pieces of at most max characters (one request yields at
// most two minutes of audio).
func splitForVolc(text string, max int) []string {
	if utf8.RuneCountInString(text) <= max {
		return []string{text}
	}
	var sentences []string
	var cur []rune
	for _, r := range text {
		cur = append(cur, r)
		if strings.ContainsRune("。！？!?；;…\n", r) || (len(cur) >= max && strings.ContainsRune("，,、 ", r)) || len(cur) >= max*3/2 {
			sentences = append(sentences, string(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		sentences = append(sentences, string(cur))
	}
	var out []string
	var piece strings.Builder
	for _, s := range sentences {
		if piece.Len() > 0 && utf8.RuneCountInString(piece.String())+utf8.RuneCountInString(s) > max {
			out = append(out, strings.TrimSpace(piece.String()))
			piece.Reset()
		}
		piece.WriteString(s)
	}
	if strings.TrimSpace(piece.String()) != "" {
		out = append(out, strings.TrimSpace(piece.String()))
	}
	return out
}

// --- requests -----------------------------------------------------------------

type volcRef struct {
	Speaker   string `json:"speaker,omitempty"`
	AudioData string `json:"audio_data,omitempty"`
}

func (v *volcAudio) creator() volc.AudioClient {
	return volc.AudioClient{Base: v.base, Key: v.key, AppID: v.appID, Model: v.model, Client: v.client, RetryWait: v.retryWait}
}

func (v *volcAudio) Check(ctx context.Context) (string, error) { return v.creator().Check(ctx) }

func volcRates(r SynthesizeRequest) map[string]any {
	cfg := map[string]any{}
	if r.Speed > 0 && math.Abs(r.Speed-1) > 0.005 {
		cfg["speech_rate"] = int(math.Round(clampF((r.Speed-1)*100, -50, 100)))
	}
	if r.Pitch != 0 {
		cfg["pitch_rate"] = int(math.Round(clampF(r.Pitch, -12, 12)))
	}
	if r.Volume != 0 {
		cfg["loudness_rate"] = int(math.Round(clampF((math.Pow(10, r.Volume/20)-1)*100, -50, 100)))
	}
	return cfg
}

// voiceRef resolves the request's voice: a preset speaker id, or a reference recording kept on this site.
func (v *volcAudio) voiceRef(ctx context.Context, id string, lang string) (volcRef, string, error) {
	if strings.HasPrefix(id, volcRefPrefix) {
		if v.acc.Refs == nil {
			return volcRef{}, "", errors.New("找不到这个音色的参考录音")
		}
		data, err := v.acc.Refs.GetRef(ctx, strings.TrimPrefix(id, volcRefPrefix))
		if err != nil {
			return volcRef{}, "", fmt.Errorf("读取音色的参考录音失败：%w", err)
		}
		return volcRef{AudioData: base64.StdEncoding.EncodeToString(data)}, "@音频1", nil
	}
	if id == "" {
		id = "zh_female_vv_uranus_bigtts"
		if !volcChinese(lang) {
			id = "en_male_tim_uranus_bigtts"
		}
	}
	return volcRef{Speaker: id}, "", nil
}

func (v *volcAudio) Synthesize(ctx context.Context, r SynthesizeRequest) (*Audio, error) {
	if strings.TrimSpace(r.Text) == "" {
		return nil, errors.New("待合成文本为空")
	}
	ref, who, err := v.voiceRef(ctx, r.ProviderVoiceID, r.Language)
	if err != nil {
		return nil, err
	}
	rate := v.sampleRate
	cfg := volcRates(r)
	cfg["format"], cfg["sample_rate"] = "wav", rate
	pieces := volcPieces(r, v.maxChars)
	prompts := make([]string, len(pieces))
	var all *audio.PCM
	for i, pr := range pieces {
		prompt := BuildVolcPrompt(pr, who, v.pure)
		prompts[i] = prompt
		if utf8.RuneCountInString(prompt) > volcPromptMax {
			return nil, fmt.Errorf("这一句加上表演说明超过了火山引擎的 %d 字上限，请缩短后重试", volcPromptMax)
		}
		data, _, err := v.creator().Create(ctx, map[string]any{"text_prompt": prompt, "references": []volcRef{ref}, "audio_config": cfg})
		if err != nil {
			return nil, err
		}
		data = audio.FixWAVSizes(data)
		if len(pieces) == 1 {
			return &Audio{Data: data, Format: "wav", SampleRate: rate, Prompts: prompts}, nil
		}
		pcm, err := audio.DecodeWAV(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("火山引擎返回的音频无法读取：%v", err)
		}
		pcm = audio.Resample(audio.ToMono(pcm), rate)
		if all == nil {
			all = &audio.PCM{Rate: rate, Channels: 1}
		} else {
			all.Data = append(all.Data, make([]float32, rate/4)...) // a short breath between sentences
		}
		all.Data = append(all.Data, pcm.Data...)
	}
	var buf bytes.Buffer
	if err := audio.EncodeWAV16(&buf, all); err != nil {
		return nil, err
	}
	return &Audio{Data: buf.Bytes(), Format: "wav", SampleRate: rate, Prompts: prompts}, nil
}

// --- reference voices: cloning and design --------------------------------------

// refClip trims a recording to what the platform accepts as a reference (mono 16-bit WAV, at most 30 s).
func refClip(data []byte) ([]byte, error) {
	pcm, err := audio.DecodeWAV(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("声音样本无法读取：%v", err)
	}
	pcm = audio.ToMono(pcm)
	if max := pcm.Rate * volcRefMaxMs / 1000; len(pcm.Data) > max {
		pcm.Data = pcm.Data[:max]
	}
	if pcm.Rate > 32000 {
		pcm = audio.Resample(pcm, 32000)
	}
	var buf bytes.Buffer
	if err := audio.EncodeWAV16(&buf, pcm); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CloneVoice keeps the sample on this site; every later request sends it as the reference recording, so there
// is nothing to train and the voice never expires on the platform.
func (v *volcAudio) CloneVoice(ctx context.Context, r CloneRequest) (*CloneResult, error) {
	if v.acc.Refs == nil {
		return nil, errors.New("当前环境不能保存参考录音")
	}
	src := r.MergedPath
	if src == "" && len(r.SamplePaths) > 0 {
		src = r.SamplePaths[0]
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	clip, err := refClip(raw)
	if err != nil {
		return nil, err
	}
	id, err := v.acc.Refs.PutRef(ctx, "clone-"+r.Seed, clip)
	if err != nil {
		return nil, fmt.Errorf("保存参考录音失败：%w", err)
	}
	return &CloneResult{ProviderVoiceID: volcRefPrefix + id, Status: "ready", Model: VolcAudioDefaultModel, Message: "火山引擎会在每次生成时参照这段录音的音色，无需训练，也不会过期"}, nil
}

func (v *volcAudio) CloneStatus(_ context.Context, id string) (*CloneResult, error) {
	return &CloneResult{ProviderVoiceID: id, Status: "ready"}, nil
}

func (v *volcAudio) DeleteVoice(ctx context.Context, id string) error {
	if !strings.HasPrefix(id, volcRefPrefix) || v.acc.Refs == nil {
		return nil // preset voices belong to the platform
	}
	return v.acc.Refs.DeleteRef(ctx, strings.TrimPrefix(id, volcRefPrefix))
}

// VolcDesignPrompt describes a new voice in words and has it read the preview line (no reference recording).
func VolcDesignPrompt(desc, preview string, pure bool) string {
	desc = strings.TrimRight(strings.TrimSpace(desc), "。.，, ")
	p := ""
	if pure {
		p = "纯人声，无背景音乐和音效。"
	}
	preview = strings.NewReplacer("“", "「", "”", "」").Replace(strings.TrimSpace(preview))
	return p + "一个全新的声音（" + desc + "）说道：“" + preview + "”"
}

// DesignVoice generates candidates from the description alone; each candidate's audio becomes its reference
// recording, so the chosen voice keeps sounding the same in every later line.
func (v *volcAudio) DesignVoice(ctx context.Context, r DesignRequest) ([]DesignCandidate, error) {
	if v.acc.Refs == nil {
		return nil, errors.New("当前环境不能保存参考录音")
	}
	n := r.Count
	if n <= 0 || n > 3 {
		n = 3
	}
	preview := firstNonBlank(r.PreviewText, "你好，很高兴认识你。这是根据你的描述生成的声音，听听看是否合适。")
	cfg := map[string]any{"format": "wav", "sample_rate": v.sampleRate}
	type result struct {
		cand DesignCandidate
		err  error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data, _, err := v.creator().Create(ctx, map[string]any{"text_prompt": VolcDesignPrompt(r.Description, preview, v.pure), "audio_config": cfg})
			if err != nil {
				results[i].err = err
				return
			}
			data = audio.FixWAVSizes(data)
			clip, err := refClip(data)
			if err != nil {
				results[i].err = err
				return
			}
			id, err := v.acc.Refs.PutRef(ctx, fmt.Sprintf("design-%s-%d", r.Seed, i), clip)
			if err != nil {
				results[i].err = err
				return
			}
			results[i].cand = DesignCandidate{ProviderVoiceID: volcRefPrefix + id, Model: VolcAudioDefaultModel, Preview: &Audio{Data: data, Format: "wav", SampleRate: v.sampleRate}}
		}(i)
	}
	wg.Wait()
	var out []DesignCandidate
	var firstErr error
	for _, x := range results {
		if x.err != nil {
			if firstErr == nil {
				firstErr = x.err
			}
			continue
		}
		out = append(out, x.cand)
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}
