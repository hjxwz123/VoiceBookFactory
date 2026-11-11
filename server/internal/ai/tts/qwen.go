package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"voicebook/internal/audio"
)

// ---------------------------------------------------------------------------
// Qianwen AI platform (DashScope protocol): Qwen-Audio-TTS and CosyVoice, non-realtime HTTP API.
//   POST {base}/api/v1/services/audio/tts/SpeechSynthesizer
// Both model families share the request shape; capabilities differ per model and per voice,
// and a voice can only be used with the model it belongs to.
// ---------------------------------------------------------------------------

const QwenDefaultBaseURL = "https://maas.qianwenaiapi.com"

// QwenVoice is one built-in voice of one model (see qwen_voices_gen.go).
type QwenVoice struct {
	Model       string
	ID          string
	Name        string
	Gender      string
	AgeGroup    string
	Language    string // primary language code used for filtering and casting
	Languages   string // human readable list from the official docs
	Traits      string
	Scene       string
	SSML        bool
	Instruct    bool
	SampleURL   string // official preview audio (system voices)
	PreviewFile string // file name inside the official base-voice preview pack
	Base        bool   // one of the ~600 "基础音色" of qwen-audio-3.0 models
}

// QwenModels lists the supported synthesis models, best first.
var QwenModels = []string{
	"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash", "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash",
	"cosyvoice-v3-plus", "cosyvoice-v3-flash", "cosyvoice-v2",
}

var qwenModelLabels = map[string]string{
	"cosyvoice-v3.5-plus":      "CosyVoice v3.5 Plus",
	"cosyvoice-v3.5-flash":     "CosyVoice v3.5 Flash",
	"qwen-audio-3.1-tts-plus":  "Qwen-Audio 3.1 Plus",
	"qwen-audio-3.1-tts-flash": "Qwen-Audio 3.1 Flash",
	"qwen-audio-3.0-tts-plus":  "Qwen-Audio 3.0 Plus",
	"qwen-audio-3.0-tts-flash": "Qwen-Audio 3.0 Flash",
	"cosyvoice-v3-plus":        "CosyVoice v3 Plus",
	"cosyvoice-v3-flash":       "CosyVoice v3 Flash",
	"cosyvoice-v2":             "CosyVoice v2",
}

// QwenModelLabel returns a short display name for a model.
func QwenModelLabel(model string) string {
	if l, ok := qwenModelLabels[model]; ok {
		return l
	}
	return model
}

// QwenPriceTier maps a model to the platform's price tier (affects credits and casting policy).
func QwenPriceTier(model string) string {
	switch model {
	case "qwen-audio-3.1-tts-plus", "qwen-audio-3.0-tts-plus":
		return "premium"
	case "cosyvoice-v2", "cosyvoice-v3-flash", "cosyvoice-v3.5-flash":
		return "economy"
	}
	return "standard"
}

var qwenDefaultVoice = map[string]string{
	"qwen-audio-3.1-tts-plus":  "longanhuan_v3.1",
	"qwen-audio-3.1-tts-flash": "longanhuan_v3.1",
	"qwen-audio-3.0-tts-plus":  "longanlingxin",
	"qwen-audio-3.0-tts-flash": "longanhuan_v3.6",
	"cosyvoice-v3-plus":        "longanyang",
	"cosyvoice-v3-flash":       "longanyang",
	"cosyvoice-v2":             "longxiaochun_v2",
}

var qwenIndex = func() map[string]*QwenVoice {
	m := make(map[string]*QwenVoice, len(qwenVoices))
	for i := range qwenVoices {
		v := &qwenVoices[i]
		m[v.Model+"|"+v.ID] = v
	}
	return m
}()

// QwenCatalog returns the built-in voices of every supported model.
func QwenCatalog() []QwenVoice { return qwenVoices }

// QwenLookup finds a built-in voice; nil for cloned/designed voices.
func QwenLookup(model, voiceID string) *QwenVoice { return qwenIndex[model+"|"+voiceID] }

func isQwenAudio(model string) bool { return strings.HasPrefix(model, "qwen-audio") }

type qwen struct {
	acc    Account
	client *http.Client
}

func newQwen(acc Account) (Provider, error) {
	if acc.BaseURL == "" {
		acc.BaseURL = QwenDefaultBaseURL
	}
	return &qwen{acc: acc, client: &http.Client{Timeout: 3 * time.Minute}}, nil
}

func init() {
	Register("qwen", newQwen)
	Register("aliyun", newQwen) // DashScope accounts created before the Qianwen platform rename
}

func (q *qwen) Name() string { return "qwen" }

// Capabilities describes the platform as a whole (the best a voice can do); use VoiceCapabilities for a concrete voice.
func (q *qwen) Capabilities() Capabilities {
	return Capabilities{
		Emotion: true, Emotions: allEmotions, InlineTags: true, Instruction: true, SSML: true, Phoneme: true, PronunciationDict: true,
		MaxChars: 5000, Languages: []string{"zh-CN", "en-US", "ja-JP", "ko-KR", "fr", "de", "es", "pt", "it", "ru", "id-ID", "vi", "th"},
		QuickClone: true, CloneMinSeconds: 10, CloneMaxSeconds: 60, ServerDenoise: qwenClonePreprocess[q.cloneModel()],
		VoiceDesign: true, DesignPromptMax: qwenDesignPromptMax,
	}
}

// VoiceCapabilities resolves what a specific model+voice supports.
func (q *qwen) VoiceCapabilities(model, voiceID string) Capabilities {
	return QwenVoiceCapabilities(q.model(model), voiceID)
}

// QwenVoiceCapabilities is exported so the API can describe voices without an account.
func QwenVoiceCapabilities(model, voiceID string) Capabilities {
	c := Capabilities{MaxChars: 5000, Languages: []string{"zh-CN", "en-US"}}
	v := QwenLookup(model, voiceID)
	ssml, instruct := true, true // cloned / designed voices support both
	if v != nil {
		ssml, instruct = v.SSML, v.Instruct
		if v.Language != "" {
			c.Languages = []string{v.Language}
		}
	}
	hotfix := model != "cosyvoice-v2"
	c.SSML = ssml
	c.Phoneme = ssml || hotfix
	c.PronunciationDict = hotfix
	if isQwenAudio(model) && instruct {
		// emotion & paralinguistic tags in text, natural-language direction via `instruction`
		c.Emotion, c.Emotions, c.InlineTags, c.Instruction = true, allEmotions, true, true
	} else if v == nil && strings.HasPrefix(model, "cosyvoice") && model != "cosyvoice-v2" && instruct {
		// CosyVoice cloned/designed voices accept free-form instructions
		c.Emotion, c.Emotions, c.Instruction = true, allEmotions, true
	}
	return c
}

func (q *qwen) model(m string) string {
	if m == "" {
		m = q.acc.Model
	}
	if m == "" {
		m = "cosyvoice-v3-flash"
	}
	return m
}

// --- emotion & tag mapping -------------------------------------------------

var qwenEmotionTag = map[string]string{
	"sad": "[sad]", "angry": "[angry]", "happy": "[excited]", "surprised": "[amazed]", "fearful": "[panicked]", "disgusted": "[scornful]",
	"tired": "[tired]", "whisper": "[whispers]", "shout": "[shouting]", "nervous": "[trembling]",
}

var emotionWords = map[string]string{
	"sad": "悲伤", "angry": "愤怒", "happy": "开心", "surprised": "惊讶", "fearful": "恐惧", "disgusted": "厌恶", "tired": "疲惫",
	"whisper": "低声耳语", "shout": "大声喊叫", "nervous": "紧张", "gentle": "温柔",
}

// stylePhrases turns style tags into natural-language direction for voices without inline tag support.
var stylePhrases = map[string]string{
	"very slowly": "放慢语速", "very fast": "加快语速", "asmr": "用轻柔的气声说", "whispers": "压低声音耳语", "like dracula": "阴森诡异地说",
	"shouting": "大声喊", "deep and loud shouting": "浑厚有力地吼", "crying": "带着哭腔说", "reluctantly": "不情愿地说", "trembling": "声音发抖",
}

func stylePhrase(t PerfTag) string {
	if p, ok := stylePhrases[t.Key]; ok {
		return p
	}
	return "用" + t.Label + "的语气说"
}

type qwenInput struct {
	Text          string         `json:"text"`
	Voice         string         `json:"voice"`
	Format        string         `json:"format"`
	SampleRate    int            `json:"sample_rate"`
	Volume        int            `json:"volume"`
	Rate          float64        `json:"rate"`
	Pitch         float64        `json:"pitch"`
	EnableSSML    bool           `json:"enable_ssml,omitempty"`
	Seed          int            `json:"seed,omitempty"`
	LanguageHints []string       `json:"language_hints,omitempty"`
	Instruction   string         `json:"instruction,omitempty"`
	HotFix        map[string]any `json:"hot_fix,omitempty"`
}

// BuildQwenInput turns a platform-neutral request into the DashScope input (exported for tests).
func BuildQwenInput(model string, r SynthesizeRequest) (qwenInput, error) {
	voice := r.ProviderVoiceID
	if voice == "" {
		voice = qwenDefaultVoice[model]
	}
	if voice == "" {
		return qwenInput{}, fmt.Errorf("没有为模型 %s 指定音色", model)
	}
	caps := QwenVoiceCapabilities(model, voice)
	in := qwenInput{Voice: voice, Format: "wav", SampleRate: 44100, Volume: 50, Rate: 1, Pitch: 1, Seed: r.Seed}
	if r.SampleRate == 48000 || r.SampleRate == 24000 || r.SampleRate == 16000 {
		in.SampleRate = r.SampleRate
	}
	if r.Speed > 0 {
		in.Rate = clampF(r.Speed, 0.5, 2)
	}
	if r.Pitch != 0 {
		in.Pitch = clampF(math.Pow(2, r.Pitch/12), 0.5, 2)
	}
	if r.Volume != 0 {
		in.Volume = int(clampF(50*math.Pow(10, r.Volume/20), 0, 100) + 0.5)
	}
	if lang := hintLanguage(r.Language); lang != "" {
		in.LanguageHints = []string{lang}
	}

	// pronunciation overrides: prefer hot_fix (keeps inline tags usable), SSML when readings conflict or hot_fix is unavailable
	useSSML := false
	var hot []map[string]string
	if len(r.Prons) > 0 {
		conflict := false
		seen := map[string]string{}
		for _, p := range r.Prons {
			py := NumericPinyin(p.Pinyin)
			if prev, ok := seen[p.Char]; ok && prev != py {
				conflict = true
			}
			seen[p.Char] = py
		}
		if caps.SSML && (conflict || !caps.PronunciationDict) {
			useSSML = true
		} else if caps.PronunciationDict {
			for ch, py := range seen {
				hot = append(hot, map[string]string{ch: py})
			}
		}
	}
	if len(hot) > 0 {
		in.HotFix = map[string]any{"pronunciation": hot}
	}

	var instr []string
	if caps.Instruction && strings.TrimSpace(r.Instruction) != "" {
		instr = append(instr, strings.TrimSpace(r.Instruction))
	}
	// positional cues (+ earlier whole-line tags, placed at the start)
	cues := append([]Cue{}, r.Cues...)
	for _, t := range r.Tags {
		if k, ok := LegacyTagMap[t]; ok {
			cues = append(cues, Cue{At: 0, Tag: k})
		}
	}
	cues = NormalizeCues(cues, utf8.RuneCountInString(r.Text))
	inline := caps.InlineTags && !useSSML
	styleAtStart := false
	for _, c := range cues {
		if t, _ := LookupPerfTag(c.Tag); c.At == 0 && t.Kind == "style" {
			styleAtStart = true
		}
	}
	prefix := ""
	if caps.Emotion && r.Emotion != "" && r.Emotion != "neutral" {
		if tag, ok := qwenEmotionTag[r.Emotion]; ok && inline {
			if !styleAtStart { // an explicit style cue at the start wins over the line emotion
				prefix = tag
			}
		} else if w, ok := emotionWords[r.Emotion]; ok && caps.Instruction {
			instr = append(instr, "用"+w+"的语气说")
		}
		if caps.Instruction && r.Intensity >= 3 { // 1 (light) and 2 are the natural baseline
			instr = append(instr, "情绪表达强烈、饱满")
		}
	}
	if !inline && caps.Instruction {
		for _, c := range cues {
			if t, _ := LookupPerfTag(c.Tag); t.Kind == "style" {
				instr = append(instr, stylePhrase(t))
			}
		}
	}
	if len(instr) > 0 {
		in.Instruction = strings.Join(dedupe(instr), "，")
		if !isQwenAudio(model) && instructionUnits(in.Instruction) > 100 {
			in.Instruction = truncateUnits(in.Instruction, 100) // CosyVoice: <= 100 units, CJK counts as 2
		}
	}

	if useSSML {
		in.EnableSSML = true
		in.Text = buildSSML(r.Text, r.Prons)
	} else if inline {
		in.Text = prefix + InsertCues(r.Text, cues)
	} else {
		in.Text = r.Text
	}
	if strings.TrimSpace(r.Text) == "" {
		return in, errors.New("待合成文本为空")
	}
	return in, nil
}

func (q *qwen) Synthesize(ctx context.Context, r SynthesizeRequest) (*Audio, error) {
	if q.acc.APIKey == "" {
		return nil, errors.New("千问 AI 平台账号缺少 API Key")
	}
	model := q.model(r.Model)
	in, err := BuildQwenInput(model, r)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"model": model, "input": in})
	url := strings.TrimRight(q.acc.BaseURL, "/") + "/api/v1/services/audio/tts/SpeechSynthesizer"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+q.acc.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if ws, ok := q.acc.Config["workspace"].(string); ok && ws != "" {
		req.Header.Set("X-DashScope-WorkSpace", ws)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接千问语音服务失败：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out struct {
		RequestID string `json:"request_id"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		Output    struct {
			FinishReason string `json:"finish_reason"`
			Audio        struct {
				URL  string `json:"url"`
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"output"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.Code != "" {
		return nil, qwenError(resp.StatusCode, out.Code, out.Message, model, in.Voice, out.RequestID)
	}
	if out.Output.Audio.URL == "" {
		return nil, fmt.Errorf("千问语音服务未返回音频（request_id=%s）", out.RequestID)
	}
	data, err := q.download(ctx, out.Output.Audio.URL)
	if err != nil {
		return nil, err
	}
	return &Audio{Data: data, Format: in.Format, SampleRate: in.SampleRate}, nil
}

func (q *qwen) download(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := q.client.Do(req)
		if err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && rerr == nil && len(data) > 0 {
				return audio.FixWAVSizes(data), nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
			if rerr != nil {
				err = rerr
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 800 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("下载合成音频失败：%v", lastErr)
}

// RetryableError marks provider failures worth retrying (rate limit, server errors).
type RetryableError struct{ Err error }

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

func qwenError(status int, code, msg, model, voice, rid string) error {
	m := strings.TrimSpace(msg)
	switch {
	case status == http.StatusUnauthorized || code == "InvalidApiKey":
		return fmt.Errorf("千问 AI 平台 API Key 无效或已过期，请在管理端检查账号配置")
	case code == "Arrearage" || strings.Contains(m, "Arrearage"):
		return fmt.Errorf("千问 AI 平台账户欠费，请充值后重试")
	case status == http.StatusTooManyRequests || code == "Throttling" || strings.HasPrefix(code, "Throttling"):
		return &RetryableError{fmt.Errorf("千问语音服务限流，请稍后重试（%s）", firstNonBlank(m, code))}
	case status >= 500:
		return &RetryableError{fmt.Errorf("千问语音服务暂时不可用（HTTP %d %s）", status, firstNonBlank(m, code))}
	case code == "InvalidParameter" && (strings.Contains(m, "411") || strings.Contains(strings.ToLower(m), "voice")):
		return fmt.Errorf("音色「%s」不能用于模型 %s（音色与模型须一一对应）：%s", voice, model, m)
	}
	if rid != "" {
		return fmt.Errorf("千问语音合成失败：%s（%s，request_id=%s）", firstNonBlank(m, "未知错误"), code, rid)
	}
	return fmt.Errorf("千问语音合成失败：%s（HTTP %d %s）", firstNonBlank(m, "未知错误"), status, code)
}

// ListVoices returns the built-in catalog (used by the admin "同步音色" action).
func (q *qwen) ListVoices(context.Context) ([]RemoteVoice, error) {
	var out []RemoteVoice
	for _, v := range qwenVoices {
		out = append(out, v.Remote())
	}
	return out, nil
}

// Remote converts a catalog entry into the generic remote-voice shape used for importing.
func (v QwenVoice) Remote() RemoteVoice {
	tags := splitList(v.Traits)
	for _, s := range splitList(strings.NewReplacer("（", "、", "）", "、", "/", "、", "-", "、").Replace(v.Scene)) {
		if s != "" && len([]rune(s)) <= 8 {
			tags = append(tags, s)
		}
	}
	for _, d := range []string{"粤语", "广东话", "东北话", "河南话", "湖南话", "陕西话", "山东话", "四川话", "安徽话", "闽南话", "上海话", "重庆话", "云南话", "宁波话", "甘肃话"} {
		if strings.Contains(v.Languages, d) {
			tags = append(tags, d)
		}
	}
	if v.Base {
		tags = append(tags, "基础音色")
	} else {
		tags = append(tags, "系统音色")
	}
	desc := strings.Trim(strings.Join([]string{v.Traits, scenePhrase(v.Scene)}, "，"), "，")
	if v.Languages != "" {
		desc += "。支持：" + v.Languages
	}
	isBook := strings.Contains(v.Scene, "有声") || strings.Contains(v.Scene, "旁白")
	return RemoteVoice{
		ProviderVoiceID: v.ID, Name: v.Name, Gender: v.Gender, AgeGroup: v.AgeGroup, Language: v.Language, Tags: dedupe(tags), Description: desc,
		PreviewURL: v.SampleURL, Model: v.Model, PriceTier: QwenPriceTier(v.Model), Recommended: isBook && !v.Base, PreviewFile: v.PreviewFile,
		CrowdPool: v.Base && (v.Scene == "日常对话" || v.Scene == "社交互动"), Base: v.Base,
	}
}

func scenePhrase(s string) string {
	if s == "" || strings.HasSuffix(s, "音色") {
		return ""
	}
	return "适合" + s
}

// --- helpers ----------------------------------------------------------------

func hintLanguage(lang string) string {
	l := strings.ToLower(lang)
	if l == "" || strings.HasPrefix(l, "zh") {
		return "" // Chinese is the default; avoid sending hints the model does not need
	}
	code := strings.SplitN(l, "-", 2)[0]
	switch code {
	case "en", "fr", "de", "ja", "ko", "ru", "pt", "th", "id", "vi", "es", "it", "ms", "fil", "ar":
		return code
	}
	return "other"
}

var toneMarks = map[rune][2]any{
	'ā': {'a', 1}, 'á': {'a', 2}, 'ǎ': {'a', 3}, 'à': {'a', 4},
	'ē': {'e', 1}, 'é': {'e', 2}, 'ě': {'e', 3}, 'è': {'e', 4},
	'ī': {'i', 1}, 'í': {'i', 2}, 'ǐ': {'i', 3}, 'ì': {'i', 4},
	'ō': {'o', 1}, 'ó': {'o', 2}, 'ǒ': {'o', 3}, 'ò': {'o', 4},
	'ū': {'u', 1}, 'ú': {'u', 2}, 'ǔ': {'u', 3}, 'ù': {'u', 4},
	'ǖ': {'v', 1}, 'ǘ': {'v', 2}, 'ǚ': {'v', 3}, 'ǜ': {'v', 4}, 'ü': {'v', 5},
	'ń': {'n', 2}, 'ň': {'n', 3}, 'ǹ': {'n', 4},
}

// NumericPinyin converts tone-marked pinyin ("xíng", "lǜ") to the numbered form the API expects ("xing2", "lv4").
// Multi-syllable input is converted syllable by syllable; already-numbered input is kept.
func NumericPinyin(p string) string {
	var parts []string
	for _, syl := range strings.Fields(p) {
		tone := 0
		var b strings.Builder
		for _, r := range syl {
			if m, ok := toneMarks[r]; ok {
				b.WriteRune(m[0].(rune))
				if t := m[1].(int); t != 5 {
					tone = t
				}
				continue
			}
			if r >= '1' && r <= '5' {
				tone = int(r - '0')
				continue
			}
			b.WriteRune(unicode.ToLower(r))
		}
		if tone == 0 {
			tone = 5 // neutral tone
		}
		parts = append(parts, fmt.Sprintf("%s%d", b.String(), tone))
	}
	return strings.Join(parts, " ")
}

func buildSSML(text string, prons []Pron) string {
	at := map[int]Pron{}
	for _, p := range prons {
		at[p.Offset] = p
	}
	var b strings.Builder
	b.WriteString("<speak>")
	i := 0
	for _, r := range text {
		ch := xmlEscape(string(r))
		if p, ok := at[i]; ok && string(r) == p.Char {
			fmt.Fprintf(&b, `<phoneme alphabet="py" ph="%s">%s</phoneme>`, xmlEscape(NumericPinyin(p.Pinyin)), ch)
		} else {
			b.WriteString(ch)
		}
		i++
	}
	b.WriteString("</speak>")
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// instructionUnits counts CosyVoice instruction length: CJK ideographs count 2, everything else 1.
func instructionUnits(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func truncateUnits(s string, max int) string {
	n := 0
	var b strings.Builder
	for _, r := range s {
		w := 1
		if unicode.Is(unicode.Han, r) {
			w = 2
		}
		if n+w > max {
			break
		}
		n += w
		b.WriteRune(r)
	}
	return b.String()
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '、' || r == ',' || r == '，' || r == ' ' })
}

func firstNonBlank(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// --- voice cloning (voice-enrollment) ---------------------------------------------
//   POST {base}/api/v1/services/audio/tts/customization  model=voice-enrollment
//   actions: create_voice -> query_voice (DEPLOYING / OK / UNDEPLOYED) ; delete_voice
// A cloned voice is bound to its target_model and works only with that model. Voices are kept until deleted.

// QwenCloneModels lists the models a clone can be bound to, best first.
var QwenCloneModels = []string{
	"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash", "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash",
	"cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash", "cosyvoice-v3-plus", "cosyvoice-v3-flash", "cosyvoice-v2",
}

// QwenDefaultCloneModel suits audiobooks: best quality, robust to noisy or reverberant samples.
const QwenDefaultCloneModel = "qwen-audio-3.1-tts-plus"

// models accepting language_hints, enable_preprocess, enable_volume_normalization and max_prompt_audio_length
var qwenClonePreprocess = map[string]bool{
	"qwen-audio-3.1-tts-plus": true, "qwen-audio-3.1-tts-flash": true, "qwen-audio-3.0-tts-plus": true, "qwen-audio-3.0-tts-flash": true,
	"cosyvoice-v3.5-plus": true, "cosyvoice-v3.5-flash": true, "cosyvoice-v3-flash": true,
}

var qwenCloneLanguages = map[string][]string{
	"qwen-audio": {"zh", "en", "fr", "de", "ja", "ko", "ru", "pt", "th", "id", "vi", "it", "es", "ms", "fil", "ar"},
	"cosyvoice-v3-plus": {"zh", "en", "fr", "de", "ja", "ko", "ru"},
	"cosyvoice": {"zh", "en", "fr", "de", "ja", "ko", "ru", "pt", "th", "id", "vi"},
}

// cloneModel is the account's target model for new clones (config "cloneModel").
func (q *qwen) cloneModel() string {
	if m, ok := q.acc.Config["cloneModel"].(string); ok {
		for _, x := range QwenCloneModels {
			if x == m {
				return m
			}
		}
	}
	return QwenDefaultCloneModel
}

// qwenCloneHint returns the language_hints value for a sample language on a model ("" = not supported).
func qwenCloneHint(model, lang string) string {
	if !qwenClonePreprocess[model] {
		return ""
	}
	code := strings.ToLower(strings.SplitN(lang, "-", 2)[0])
	if code == "" || code == "yue" {
		code = "zh"
	}
	key := "cosyvoice"
	if isQwenAudio(model) {
		key = "qwen-audio"
	} else if model == "cosyvoice-v3-plus" {
		key = "cosyvoice-v3-plus"
	}
	for _, c := range qwenCloneLanguages[key] {
		if c == code {
			return c
		}
	}
	return ""
}

// QwenClonePrefix makes the voice name prefix: letters and digits only, at most 10 characters.
func QwenClonePrefix(seed string) string {
	var b strings.Builder
	b.WriteString("vb")
	for _, r := range strings.ToLower(seed) {
		if b.Len() >= 10 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var qwenAudioMime = map[string]string{".wav": "audio/wav", ".mp3": "audio/mpeg", ".m4a": "audio/mp4"}

// qwenCloneSample checks a sample against the platform rules and returns it as a Base64 data URI.
func qwenCloneSample(p string) (string, float64, error) {
	ext := strings.ToLower(filepath.Ext(p))
	mime, ok := qwenAudioMime[ext]
	if !ok {
		return "", 0, fmt.Errorf("千问只接受 wav、mp3、m4a 格式的复刻样本（当前 %s）", ext)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", 0, fmt.Errorf("读取声音样本失败：%w", err)
	}
	if len(data)*4/3 >= 10<<20 { // the Base64 form must stay under 10 MB
		return "", 0, fmt.Errorf("声音样本太大（%.1f MB），千问要求编码后小于 10 MB，请缩短到 1 分钟以内", float64(len(data))/(1<<20))
	}
	sec := 0.0
	if ext == ".wav" {
		pcm, err := audio.DecodeWAV(bytes.NewReader(data))
		if err != nil {
			return "", 0, fmt.Errorf("声音样本无法读取：%w", err)
		}
		if pcm.Rate < 16000 {
			return "", 0, fmt.Errorf("声音样本的采样率太低（%d Hz），千问要求不低于 16 kHz", pcm.Rate)
		}
		sec = float64(pcm.DurationMs()) / 1000
		if sec < 5 {
			return "", 0, fmt.Errorf("声音样本太短：千问需要至少 5 秒连续清晰的朗读（建议 10～20 秒），当前约 %.0f 秒", sec)
		}
		if sec > 60 {
			return "", 0, fmt.Errorf("声音样本超过 1 分钟（千问上限），当前约 %.0f 秒", sec)
		}
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), sec, nil
}

type qwenEnrollResp struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Output    struct {
		VoiceID      string `json:"voice_id"`
		Status       string `json:"status"`
		TargetModel  string `json:"target_model"`
		PreviewAudio struct {
			Data           string `json:"data"`
			SampleRate     int    `json:"sample_rate"`
			ResponseFormat string `json:"response_format"`
		} `json:"preview_audio"`
	} `json:"output"`
}

// enroll calls the voice-enrollment service with one action.
func (q *qwen) enroll(ctx context.Context, input map[string]any) (*qwenEnrollResp, error) {
	return q.enrollWith(ctx, input, nil)
}

func (q *qwen) enrollWith(ctx context.Context, input, params map[string]any) (*qwenEnrollResp, error) {
	if q.acc.APIKey == "" {
		return nil, errors.New("千问 AI 平台账号缺少 API Key")
	}
	payload := map[string]any{"model": "voice-enrollment", "input": input}
	if params != nil {
		payload["parameters"] = params
	}
	body, _ := json.Marshal(payload)
	url := strings.TrimRight(q.acc.BaseURL, "/") + "/api/v1/services/audio/tts/customization"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+q.acc.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if ws, ok := q.acc.Config["workspace"].(string); ok && ws != "" {
		req.Header.Set("X-DashScope-WorkSpace", ws)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &RetryableError{fmt.Errorf("连接千问声音复刻服务失败：%w", err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var out qwenEnrollResp
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.Code != "" {
		return &out, qwenCloneError(resp.StatusCode, out.Code, out.Message, out.RequestID)
	}
	return &out, nil
}

func qwenCloneError(status int, code, msg, rid string) error {
	m := strings.TrimSpace(msg)
	switch {
	case code == "Audio.AudioShortError" || code == "Audio.AudioSilentError":
		return errors.New("样本里的有效人声太短：千问需要至少一段 5 秒以上连续、清晰的朗读（建议总长 10～20 秒），请重新录一段")
	case code == "Audio.PreprocessError":
		return errors.New("样本预处理失败：可能有效人声太短、几乎没有声音或噪音太大，请换一段清晰的朗读录音")
	case code == "BadRequest.UnsupportedFileFormat":
		return errors.New("样本格式不被千问支持（需要 16 位 WAV、MP3 或 M4A）")
	case code == "BadRequest.TooLarge":
		return errors.New("样本文件太大，千问要求小于 10 MB")
	case code == "Throttling.AllocationQuota" || strings.Contains(m, "Maximum voice storage limit"):
		return errors.New("千问账号的自定义音色数量已达上限（1000 个），请删除不用的复刻音色后再试")
	case code == "BadRequest.ResourceNotExist":
		return errors.New("音色在千问上不存在（可能已被删除）")
	case code == "DataInspectionFailed" || strings.Contains(code, "DataInspection"):
		return errors.New("内容没有通过千问的内容审核，请调整后重试（声音描述不要涉及敏感内容，也不要要求模仿真实人物）")
	case status == http.StatusUnauthorized || code == "InvalidApiKey" || code == "Arrearage" || strings.HasPrefix(code, "Throttling") ||
		status == http.StatusTooManyRequests || status >= 500:
		return qwenError(status, code, msg, "", "", rid) // key, billing, throttling and outages read the same as for synthesis
	}
	return fmt.Errorf("千问创建自定义音色失败：%s（%s，request_id=%s）", firstNonBlank(m, "未知错误"), firstNonBlank(code, fmt.Sprintf("HTTP %d", status)), rid)
}

// CloneVoice creates a clone bound to the account's clone model and waits briefly for it to pass review.
func (q *qwen) CloneVoice(ctx context.Context, r CloneRequest) (*CloneResult, error) {
	if r.Tier == "pro" {
		return nil, errors.New("千问只提供快速复刻，请选择「快速克隆」")
	}
	sample := r.MergedPath
	if sample == "" && len(r.SamplePaths) > 0 {
		sample = r.SamplePaths[0]
	}
	if sample == "" {
		return nil, errors.New("没有可用的声音样本")
	}
	uri, sec, err := qwenCloneSample(sample)
	if err != nil {
		return nil, err
	}
	model := q.cloneModel()
	input := map[string]any{"action": "create_voice", "target_model": model, "prefix": QwenClonePrefix(firstNonBlank(r.Seed, r.Name)), "audio": uri}
	if qwenClonePreprocess[model] {
		if hint := qwenCloneHint(model, r.Language); hint != "" {
			input["language_hints"] = []string{hint}
		}
		input["enable_preprocess"] = r.Denoise
		input["enable_volume_normalization"] = "true"
		if sec > 0 { // use up to 20 s of the sample (recommended 10-20 s; the service default is 10 s)
			input["max_prompt_audio_length"] = math.Round(clampF(sec, 3, 20)*10) / 10
		}
	}
	out, err := withRetry(ctx, func() (*qwenEnrollResp, error) { return q.enroll(ctx, input) })
	if err != nil {
		return nil, err
	}
	if out.Output.VoiceID == "" {
		return nil, fmt.Errorf("千问没有返回音色 ID（request_id=%s）", out.RequestID)
	}
	res := &CloneResult{ProviderVoiceID: out.Output.VoiceID, Status: "training", Model: model, PriceTier: QwenPriceTier(model),
		Message: fmt.Sprintf("千问复刻完成，音色长期有效（只能用于 %s 模型）", QwenModelLabel(model))}
	// review normally finishes within seconds; longer reviews continue through CloneStatus
	delay := qwenClonePoll
	for i := 0; i < 20; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		st, err := q.CloneStatus(ctx, res.ProviderVoiceID)
		if err != nil {
			var re *RetryableError
			if errors.As(err, &re) {
				continue
			}
			return nil, err
		}
		if st.Status != "training" {
			res.Status = st.Status
			if st.Status == "failed" {
				res.Message = st.Message
			}
			break
		}
		if delay < 3*time.Second {
			delay += delay / 2
		}
	}
	return res, nil
}

var qwenClonePoll = time.Second

// CloneStatus maps the enrollment status: DEPLOYING -> training, OK -> ready, UNDEPLOYED -> failed.
func (q *qwen) CloneStatus(ctx context.Context, id string) (*CloneResult, error) {
	out, err := q.enroll(ctx, map[string]any{"action": "query_voice", "voice_id": id})
	if err != nil {
		return nil, err
	}
	res := &CloneResult{ProviderVoiceID: id}
	switch strings.ToUpper(out.Output.Status) {
	case "OK":
		res.Status = "ready"
	case "UNDEPLOYED":
		res.Status, res.Message = "failed", "千问审核未通过，这个音色不能使用。请换一段正常朗读、内容合规的录音"
	default:
		res.Status = "training"
	}
	return res, nil
}

// DeleteVoice removes a clone on the platform; a voice that no longer exists counts as deleted.
func (q *qwen) DeleteVoice(ctx context.Context, id string) error {
	if id == "" || q.acc.APIKey == "" {
		return nil
	}
	out, err := q.enroll(ctx, map[string]any{"action": "delete_voice", "voice_id": id})
	if err != nil && out != nil && out.Code == "BadRequest.ResourceNotExist" {
		return nil
	}
	return err
}

// --- voice design (voice-enrollment with voice_prompt) --------------------------
// Same service as cloning: create_voice with a text description instead of audio. The response carries a preview;
// the voice is reviewed (DEPLOYING -> OK / UNDEPLOYED), bound to its target_model and counts against the account's
// 1000 custom voices (shared with clones). Qianwen deletes voices unused for a year.

// QwenDesignModels lists the models a designed voice can be bound to, best first.
var QwenDesignModels = []string{
	"qwen-audio-3.1-tts-plus", "qwen-audio-3.1-tts-flash", "qwen-audio-3.0-tts-plus", "qwen-audio-3.0-tts-flash",
	"cosyvoice-v3.5-plus", "cosyvoice-v3.5-flash", "cosyvoice-v3-plus", "cosyvoice-v3-flash",
}

const (
	qwenDesignPromptMax = 500
	qwenDesignPreviewDefault = "你好，很高兴认识你。这是根据你的描述设计出来的声音，听听看是否合适。"
)

// QwenDesignPrefix keeps the candidate number inside the 10-character prefix limit ("vb" + 7 chars + digit).
func QwenDesignPrefix(seed string, i int) string {
	p := QwenClonePrefix(seed)
	if len(p) > 9 {
		p = p[:9]
	}
	return p + strconv.Itoa(i%10)
}

// QwenDesignModelFor returns the model designed voices are bound to: config "designModel", else the clone model
// when it supports design, else the default.
func QwenDesignModelFor(cfg map[string]any) string {
	for _, key := range []string{"designModel", "cloneModel"} {
		if m, ok := cfg[key].(string); ok {
			for _, x := range QwenDesignModels {
				if x == m {
					return m
				}
			}
		}
	}
	return QwenDefaultCloneModel
}

// cutRunes shortens text to at most n runes, preferring to end at a sentence or clause boundary.
func cutRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	r = r[:n]
	for i := len(r) - 1; i >= n*2/3; i-- {
		if strings.ContainsRune("。！？；!?;，,、", r[i]) {
			return string(r[:i+1])
		}
	}
	return string(r)
}

// QwenDesignPrompt normalizes a description for voice_prompt (Chinese/English, at most 500 characters).
func QwenDesignPrompt(desc string) string {
	return cutRunes(strings.Join(strings.Fields(desc), " "), qwenDesignPromptMax)
}

// QwenDesignPreview picks the preview line (15-200 characters, Chinese or English only) and its language hint.
func QwenDesignPreview(text string) (string, string) {
	t := cutRunes(strings.Join(strings.Fields(text), " "), 200)
	ok := utf8.RuneCountInString(t) >= 15
	han, latin := 0, 0
	for _, r := range t {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case r < 0x80:
			if unicode.IsLetter(r) {
				latin++
			}
		case unicode.IsPunct(r) || unicode.IsSpace(r) || strings.ContainsRune("，。！？、；：“”‘’（）《》…—", r):
		default:
			ok = false // kana, hangul, Cyrillic ...: the preview only speaks Chinese or English
		}
	}
	if !ok || han+latin == 0 {
		return qwenDesignPreviewDefault, "zh"
	}
	if han == 0 {
		return t, "en"
	}
	return t, "zh"
}

// DesignVoice creates Count candidate voices (at most 3) from one description, in parallel. Candidates that fail
// review are deleted; the call fails only when no candidate survives.
func (q *qwen) DesignVoice(ctx context.Context, r DesignRequest) ([]DesignCandidate, error) {
	prompt := QwenDesignPrompt(r.Description)
	if utf8.RuneCountInString(prompt) < 4 {
		return nil, errors.New("请用一句话描述想要的声音")
	}
	preview, lang := QwenDesignPreview(r.PreviewText)
	model := QwenDesignModelFor(q.acc.Config)
	n := r.Count
	if n <= 0 {
		n = 3
	}
	if n > 3 {
		n = 3
	}
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
			input := map[string]any{"action": "create_voice", "target_model": model, "voice_prompt": prompt, "preview_text": preview,
				"prefix": QwenDesignPrefix(r.Seed, i), "language_hints": []string{lang}}
			out, err := withRetry(ctx, func() (*qwenEnrollResp, error) {
				return q.enrollWith(ctx, input, map[string]any{"sample_rate": 48000, "response_format": "wav"})
			})
			if err != nil {
				results[i].err = err
				return
			}
			c := DesignCandidate{ProviderVoiceID: out.Output.VoiceID, Model: model, PriceTier: QwenPriceTier(model)}
			if c.ProviderVoiceID == "" {
				results[i].err = fmt.Errorf("千问没有返回音色 ID（request_id=%s）", out.RequestID)
				return
			}
			if data, err := base64.StdEncoding.DecodeString(out.Output.PreviewAudio.Data); err == nil && len(data) > 0 {
				format := firstNonBlank(out.Output.PreviewAudio.ResponseFormat, "wav")
				if format == "wav" {
					data = audio.FixWAVSizes(data)
				}
				c.Preview = &Audio{Data: data, Format: format, SampleRate: out.Output.PreviewAudio.SampleRate}
			}
			if err := q.waitReviewed(ctx, c.ProviderVoiceID); err != nil {
				_ = q.DeleteVoice(context.WithoutCancel(ctx), c.ProviderVoiceID)
				results[i].err = err
				return
			}
			if c.Preview == nil {
				_ = q.DeleteVoice(context.WithoutCancel(ctx), c.ProviderVoiceID)
				results[i].err = errors.New("千问没有返回试听音频")
				return
			}
			results[i].cand = c
		}(i)
	}
	wg.Wait()
	var out []DesignCandidate
	var firstErr error
	for _, res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		out = append(out, res.cand)
	}
	if ctx.Err() != nil { // the job was canceled: do not leave voices behind
		for _, c := range out {
			_ = q.DeleteVoice(context.WithoutCancel(ctx), c.ProviderVoiceID)
		}
		return nil, ctx.Err()
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

// waitReviewed polls a new voice until it passes review; an UNDEPLOYED voice is an error, a slow review is not.
func (q *qwen) waitReviewed(ctx context.Context, id string) error {
	delay := qwenClonePoll
	for i := 0; i < 15; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		st, err := q.CloneStatus(ctx, id)
		if err != nil {
			var re *RetryableError
			if errors.As(err, &re) {
				continue
			}
			return err
		}
		switch st.Status {
		case "ready":
			return nil
		case "failed":
			return errors.New("千问审核没有通过这个声音描述，请换一种描述（不要要求模仿真实人物，避免敏感内容）")
		}
		if delay < 3*time.Second {
			delay += delay / 2
		}
	}
	return nil // still in review: usable once approved
}
