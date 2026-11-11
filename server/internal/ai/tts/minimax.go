package tts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"voicebook/internal/audio"
)

// ---------------------------------------------------------------------------
// MiniMax speech synthesis (T2A v2).
//   sync : POST {base}/v1/t2a_v2                         (text < 10,000 chars, audio returned as hex)
//   async: POST {base}/v1/t2a_async_v2 -> GET /v1/query/t2a_async_query_v2 -> GET /v1/files/retrieve_content
// System voices work with every model; the model decides quality and price (hd / turbo).
// ---------------------------------------------------------------------------

const MiniMaxDefaultBaseURL = "https://api.minimaxi.com"

// MiniMaxModels lists the supported speech models, best first.
var MiniMaxModels = []string{"speech-2.8-hd", "speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo", "speech-02-hd", "speech-02-turbo"}

// MiniMaxCatalogModels are the models each system voice is listed under in the voice library.
var MiniMaxCatalogModels = []string{"speech-2.8-hd", "speech-2.8-turbo"}

var miniMaxModelLabels = map[string]string{
	"speech-2.8-hd": "MiniMax 2.8 HD", "speech-2.8-turbo": "MiniMax 2.8 Turbo",
	"speech-2.6-hd": "MiniMax 2.6 HD", "speech-2.6-turbo": "MiniMax 2.6 Turbo",
	"speech-02-hd": "MiniMax 02 HD", "speech-02-turbo": "MiniMax 02 Turbo",
	"speech-01-hd": "MiniMax 01 HD", "speech-01-turbo": "MiniMax 01 Turbo",
}

// ModelLabel returns a short display name for any supported synthesis model.
func ModelLabel(model string) string {
	if l, ok := qwenModelLabels[model]; ok {
		return l
	}
	if l, ok := miniMaxModelLabels[model]; ok {
		return l
	}
	return model
}

// MiniMaxPriceTier: HD models are the premium tier, turbo models the standard tier.
func MiniMaxPriceTier(model string) string {
	if strings.HasSuffix(model, "-hd") {
		return "premium"
	}
	return "standard"
}

// MiniMaxVoice is one system voice (see minimax_voices_gen.go).
type MiniMaxVoice struct {
	ID        string
	Name      string // Chinese display name
	NameEN    string // official English name (non-Chinese voices)
	Gender    string
	AgeGroup  string
	Language  string
	LangLabel string
	Traits    string
	Tags      []string
	Narration bool // suited to long-form narration
	Crowd     bool // everyday voice usable for background characters
}

var miniMaxIndex = func() map[string]*MiniMaxVoice {
	m := make(map[string]*MiniMaxVoice, len(miniMaxVoices))
	for i := range miniMaxVoices {
		m[miniMaxVoices[i].ID] = &miniMaxVoices[i]
	}
	return m
}()

// MiniMaxCatalog returns the built-in system voices.
func MiniMaxCatalog() []MiniMaxVoice { return miniMaxVoices }

// MiniMaxLookup finds a system voice; nil for cloned/designed voices.
func MiniMaxLookup(voiceID string) *MiniMaxVoice { return miniMaxIndex[voiceID] }

// Remote converts a catalog entry into the generic remote-voice shape for one model.
func (v MiniMaxVoice) Remote(model string) RemoteVoice {
	tags := append(append([]string{}, v.Tags...), "系统音色")
	desc := v.Traits
	if v.NameEN != "" {
		desc += "。官方名称：" + v.NameEN
	}
	if v.LangLabel != "" {
		desc += "。语言：" + v.LangLabel
	}
	isTurbo := strings.HasSuffix(model, "-turbo")
	return RemoteVoice{
		ProviderVoiceID: v.ID, Name: v.Name, Gender: v.Gender, AgeGroup: v.AgeGroup, Language: v.Language, Tags: dedupe(tags),
		Description: desc, Model: model, PriceTier: MiniMaxPriceTier(model),
		Recommended: v.Narration && strings.HasPrefix(v.Language, "zh") && !strings.Contains(v.Name, "Beta"),
		CrowdPool:   v.Crowd && isTurbo && !strings.Contains(v.Name, "Beta"),
	}
}

// --- capabilities -----------------------------------------------------------

var miniMaxLanguages = []string{"zh-CN", "zh-HK", "en-US", "ja-JP", "ko-KR", "es", "pt", "fr", "de", "ru", "it", "ar", "tr", "nl", "uk", "vi", "id-ID", "th", "pl", "ro", "el", "cs", "fi", "hi"}

// interjection tags of speech-2.8 models, keyed by performance-tag key
var miniMaxSoundTags = map[string]string{
	"laughing": "(laughs)", "giggles": "(chuckle)", "snorts": "(snorts)", "sighing": "(sighs)",
	"gasp": "(gasps)", "cough": "(coughs)", "clears throat": "(clear-throat)",
}

// style tags at the start of a line become the line's emotion (MiniMax has no inline style tags)
var miniMaxStyleEmotion = map[string]string{
	"sad": "sad", "crying": "sad", "angry": "angry", "shouting": "angry", "deep and loud shouting": "angry",
	"excited": "happy", "amazed": "surprised", "curious": "surprised", "panicked": "fearful", "trembling": "fearful",
	"scornful": "disgusted", "sarcastic": "disgusted", "whispers": "whisper", "asmr": "whisper",
}

var miniMaxSoundKeys = func() []string {
	var k []string
	for t := range miniMaxSoundTags {
		k = append(k, t)
	}
	sort.Strings(k)
	return k
}()

func miniMaxModelFamily(model string) string {
	switch {
	case strings.HasPrefix(model, "speech-2.8"):
		return "2.8"
	case strings.HasPrefix(model, "speech-2.6"):
		return "2.6"
	}
	return "legacy"
}

// MiniMaxCapabilities describes what a model supports (every system voice behaves the same on a model).
func MiniMaxCapabilities(model string) Capabilities {
	emotions := []string{"neutral", "happy", "sad", "angry", "fearful", "disgusted", "surprised"}
	fam := miniMaxModelFamily(model)
	if fam == "2.6" {
		emotions = append(emotions, "whisper")
	}
	c := Capabilities{Emotion: true, Emotions: emotions, Phoneme: true, PronunciationDict: true, MaxChars: 5000, Languages: miniMaxLanguages}
	if fam == "2.8" {
		c.InlineTags, c.TagKeys = true, miniMaxSoundKeys
	}
	return c
}

// --- options & request building --------------------------------------------

// MiniMaxOptions are the account-level settings (ProviderAccount.Config).
type MiniMaxOptions struct {
	Mode              string // auto | sync | async
	TextNormalization string // "" | basic | quality
	LanguageBoost     string // "" | auto (book language still wins when it is not Chinese)
	GroupID           string
	Watermark         bool
}

func miniMaxOptions(cfg map[string]any) MiniMaxOptions {
	s := func(k string) string {
		v, _ := cfg[k].(string)
		return strings.TrimSpace(v)
	}
	o := MiniMaxOptions{Mode: s("synthMode"), TextNormalization: s("textNormalization"), LanguageBoost: s("languageBoost"), GroupID: s("groupId")}
	o.Watermark, _ = cfg["watermark"].(bool)
	switch o.Mode {
	case "sync", "async":
	default:
		o.Mode = "auto"
	}
	if o.TextNormalization != "basic" && o.TextNormalization != "quality" {
		o.TextNormalization = ""
	}
	return o
}

// MiniMaxRequest is the platform-neutral request rendered for MiniMax (exported for tests).
type MiniMaxRequest struct {
	Model         string
	Text          string
	VoiceID       string
	Speed         float64
	Vol           float64
	Pitch         int
	Emotion       string
	SampleRate    int
	LanguageBoost string
	Normalization string
	Watermark     bool
}

// miniMaxBillingChars counts characters the way MiniMax bills them (CJK = 2).
func miniMaxBillingChars(s string) int {
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

var miniMaxRates = []int{8000, 16000, 22050, 24000, 32000, 44100}

var miniMaxBoost = map[string]string{
	"en": "English", "ja": "Japanese", "ko": "Korean", "es": "Spanish", "pt": "Portuguese", "fr": "French", "de": "German",
	"ru": "Russian", "it": "Italian", "ar": "Arabic", "tr": "Turkish", "nl": "Dutch", "uk": "Ukrainian", "vi": "Vietnamese",
	"id": "Indonesian", "th": "Thai", "pl": "Polish", "ro": "Romanian", "el": "Greek", "cs": "Czech", "fi": "Finnish",
	"hi": "Hindi", "bg": "Bulgarian", "da": "Danish", "he": "Hebrew", "ms": "Malay", "fa": "Persian", "sk": "Slovak",
	"sv": "Swedish", "hr": "Croatian", "fil": "Filipino", "tl": "Filipino", "hu": "Hungarian", "no": "Norwegian", "nb": "Norwegian",
	"sl": "Slovenian", "ca": "Catalan", "nn": "Nynorsk", "ta": "Tamil", "af": "Afrikaans",
}

func miniMaxLanguageBoost(lang, voiceID, fallback string) string {
	l := strings.ToLower(lang)
	if strings.HasPrefix(voiceID, "Cantonese_") || l == "zh-hk" || l == "yue" || strings.HasPrefix(l, "yue-") {
		return "Chinese,Yue" // required for Cantonese voices
	}
	if b, ok := miniMaxBoost[strings.SplitN(l, "-", 2)[0]]; ok {
		return b
	}
	if fallback == "auto" {
		return "auto"
	}
	return ""
}

func miniMaxEmotion(e, model string) string {
	switch e {
	case "happy", "sad", "angry", "fearful", "disgusted", "surprised":
		return e
	case "whisper":
		if miniMaxModelFamily(model) == "2.6" {
			return e
		}
	}
	return "" // neutral & everything else: let the model pick the emotion from the text
}

// BuildMiniMaxRequest renders text (readings, interjections), emotion and prosody for MiniMax.
func BuildMiniMaxRequest(model string, r SynthesizeRequest, o MiniMaxOptions) (MiniMaxRequest, error) {
	voice := r.ProviderVoiceID
	if voice == "" {
		voice = "male-qn-qingse"
	}
	req := MiniMaxRequest{Model: model, VoiceID: voice, Speed: 1, Vol: 1, SampleRate: 44100, Normalization: o.TextNormalization, Watermark: o.Watermark}
	for _, sr := range miniMaxRates {
		if r.SampleRate == sr {
			req.SampleRate = sr
		}
	}
	if r.Speed > 0 {
		req.Speed = r.Speed
	}
	if r.Pitch != 0 {
		req.Pitch = int(math.Round(clampF(r.Pitch, -12, 12)))
	}
	if r.Volume != 0 {
		req.Vol = math.Round(clampF(math.Pow(10, r.Volume/20), 0.1, 10)*100) / 100
	}
	req.LanguageBoost = miniMaxLanguageBoost(r.Language, voice, o.LanguageBoost)
	caps := MiniMaxCapabilities(model)
	emotion := r.Emotion

	cues := append([]Cue{}, r.Cues...)
	for _, t := range r.Tags {
		if k, ok := LegacyTagMap[t]; ok {
			cues = append(cues, Cue{At: 0, Tag: k})
		}
	}
	runes := []rune(r.Text)
	cues = NormalizeCues(cues, len(runes))
	inline := map[int][]string{}
	for _, c := range cues {
		t, _ := LookupPerfTag(c.Tag)
		if t.Kind == "sound" {
			if tag, ok := miniMaxSoundTags[t.Key]; ok && caps.InlineTags {
				inline[c.At] = append(inline[c.At], tag)
			}
			continue
		}
		if c.At != 0 { // MiniMax has no positional style control
			continue
		}
		switch t.Key {
		case "very slowly":
			req.Speed *= 0.8
		case "very fast":
			req.Speed *= 1.2
		default:
			if e, ok := miniMaxStyleEmotion[t.Key]; ok && (emotion == "" || emotion == "neutral") {
				emotion = e
			}
		}
	}
	req.Speed = math.Round(clampF(req.Speed, 0.5, 2)*100) / 100
	req.Emotion = miniMaxEmotion(emotion, model)

	readings := map[int]string{}
	for _, p := range r.Prons {
		if p.Offset >= 0 && p.Offset < len(runes) && string(runes[p.Offset]) == p.Char && strings.TrimSpace(p.Pinyin) != "" {
			var b strings.Builder
			for _, syl := range strings.Fields(NumericPinyin(p.Pinyin)) {
				b.WriteString("(" + syl + ")")
			}
			readings[p.Offset] = b.String()
		}
	}
	var b strings.Builder
	for i := 0; i <= len(runes); i++ {
		for _, tag := range inline[i] {
			b.WriteString(tag)
		}
		if i == len(runes) {
			break
		}
		ch := runes[i]
		if rd, ok := readings[i]; ok {
			b.WriteString(rd) // inline pronunciation replaces the character: "(chu3)"
			continue
		}
		switch {
		case ch == '(':
			b.WriteRune('（') // ASCII parentheses are markup for MiniMax (readings / interjections)
		case ch == ')':
			b.WriteRune('）')
		case ch == '\n' || ch == '\t':
			b.WriteRune(ch)
		case unicode.IsControl(ch) || ch == 0x200b || ch == 0xfeff:
			// invisible characters make the API reject the request when they exceed 10%
		default:
			b.WriteRune(ch)
		}
	}
	req.Text = strings.ReplaceAll(b.String(), "<#", "< #") // never let prose turn into pause markup
	if strings.TrimSpace(r.Text) == "" {
		return req, errors.New("待合成文本为空")
	}
	return req, nil
}

func (q MiniMaxRequest) voiceSetting(async bool) map[string]any {
	vs := map[string]any{"voice_id": q.VoiceID, "speed": q.Speed, "vol": q.Vol, "pitch": q.Pitch}
	if q.Emotion != "" {
		vs["emotion"] = q.Emotion
	}
	if async && q.Normalization != "" {
		vs["english_normalization"] = true
	}
	return vs
}

// SyncBody is the JSON body of POST /v1/t2a_v2.
func (q MiniMaxRequest) SyncBody() map[string]any {
	body := map[string]any{
		"model": q.Model, "text": q.Text, "stream": false, "output_format": "hex",
		"voice_setting": q.voiceSetting(false),
		"audio_setting": map[string]any{"sample_rate": q.SampleRate, "format": "wav", "channel": 1},
	}
	if q.LanguageBoost != "" {
		body["language_boost"] = q.LanguageBoost
	}
	if q.Normalization != "" {
		body["text_normalization_mode"] = q.Normalization
	}
	if q.Watermark {
		body["aigc_watermark"] = true
	}
	return body
}

// AsyncBody is the JSON body of POST /v1/t2a_async_v2.
func (q MiniMaxRequest) AsyncBody() map[string]any {
	body := map[string]any{
		"model": q.Model, "text": q.Text,
		"voice_setting": q.voiceSetting(true),
		"audio_setting": map[string]any{"audio_sample_rate": q.SampleRate, "format": "wav", "channel": 1},
	}
	if q.LanguageBoost != "" {
		body["language_boost"] = q.LanguageBoost
	}
	if q.Watermark {
		body["aigc_watermark"] = true
	}
	return body
}

// --- provider ---------------------------------------------------------------

type miniMax struct {
	acc    Account
	opt    MiniMaxOptions
	client *http.Client
	gate   *rateGate
}

func newMiniMax(acc Account) (Provider, error) {
	if acc.BaseURL == "" {
		acc.BaseURL = MiniMaxDefaultBaseURL
	}
	rpm := acc.RPM
	if rpm <= 0 {
		rpm = 20 // MiniMax T2A default for paid accounts (free accounts: 10)
	}
	sum := sha256.Sum256([]byte(acc.APIKey))
	return &miniMax{
		acc: acc, opt: miniMaxOptions(acc.Config),
		client: &http.Client{Timeout: 5 * time.Minute},
		gate:   gateFor("minimax|"+strings.TrimRight(acc.BaseURL, "/")+"|"+hex.EncodeToString(sum[:8]), rpm),
	}, nil
}

func init() { Register("minimax", newMiniMax) }

func (m *miniMax) Name() string { return "minimax" }

// Capabilities describes the platform as a whole (the best a model can do).
func (m *miniMax) Capabilities() Capabilities {
	c := MiniMaxCapabilities("speech-2.8-hd")
	c.Emotions = []string{"neutral", "happy", "sad", "angry", "fearful", "disgusted", "surprised", "whisper"}
	c.QuickClone, c.CloneExpiryDays, c.CloneExpiresUnused = true, 7, true
	c.CloneMinSeconds, c.CloneMaxSeconds, c.ServerDenoise = 10, 300, true
	return c
}

func (m *miniMax) VoiceCapabilities(model, _ string) Capabilities { return MiniMaxCapabilities(m.model(model)) }

func (m *miniMax) model(md string) string {
	if md == "" {
		md = m.acc.Model
	}
	if md == "" {
		md = "speech-2.8-hd"
	}
	return md
}

// Synthesize uses the sync API for normal lines and the async (long-text) API when configured or needed.
func (m *miniMax) Synthesize(ctx context.Context, r SynthesizeRequest) (*Audio, error) {
	if m.acc.APIKey == "" {
		return nil, errors.New("MiniMax 账号缺少 API Key")
	}
	model := m.model(r.Model)
	req, err := BuildMiniMaxRequest(model, r, m.opt)
	if err != nil {
		return nil, err
	}
	billing := miniMaxBillingChars(req.Text)
	if billing > 100000 {
		return nil, fmt.Errorf("单次合成文本过长（MiniMax 按汉字 2 字符计，最多 10 万字符，当前 %d）", billing)
	}
	async := m.opt.Mode == "async" || (m.opt.Mode == "auto" && utf8.RuneCountInString(req.Text) > 3000) || utf8.RuneCountInString(req.Text) >= 10000
	return withRetry(ctx, func() (*Audio, error) {
		if async {
			return m.asyncSynth(ctx, req)
		}
		return m.syncSynth(ctx, req)
	})
}

type miniMaxBase struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

func (m *miniMax) endpoint(p string, q url.Values) string {
	if m.opt.GroupID != "" {
		if q == nil {
			q = url.Values{}
		}
		q.Set("GroupId", m.opt.GroupID)
	}
	u := strings.TrimRight(m.acc.BaseURL, "/") + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (m *miniMax) do(ctx context.Context, method, p string, q url.Values, body any, limit int64) (*http.Response, []byte, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.endpoint(p, q), rd)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.acc.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, &RetryableError{fmt.Errorf("连接 MiniMax 语音服务失败：%w", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || errors.Is(err, io.ErrUnexpectedEOF) {
			return resp, nil, &RetryableError{fmt.Errorf("读取 MiniMax 返回数据失败：%w", err)}
		}
		return resp, nil, err
	}
	return resp, raw, nil
}

func (m *miniMax) syncSynth(ctx context.Context, q MiniMaxRequest) (*Audio, error) {
	if err := m.gate.Wait(ctx); err != nil {
		return nil, err
	}
	resp, raw, err := m.do(ctx, http.MethodPost, "/v1/t2a_v2", nil, q.SyncBody(), 400<<20)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data *struct {
			Audio  string `json:"audio"`
			Status int    `json:"status"`
		} `json:"data"`
		ExtraInfo struct {
			AudioSampleRate int    `json:"audio_sample_rate"`
			AudioFormat     string `json:"audio_format"`
		} `json:"extra_info"`
		TraceID  string      `json:"trace_id"`
		BaseResp miniMaxBase `json:"base_resp"`
	}
	if jerr := json.Unmarshal(raw, &out); jerr != nil && resp.StatusCode == http.StatusOK {
		return nil, &RetryableError{fmt.Errorf("MiniMax 返回了无法解析的数据（HTTP %d）", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK || out.BaseResp.StatusCode != 0 {
		m.cooldown(resp.StatusCode, out.BaseResp.StatusCode)
		return nil, miniMaxError(resp.StatusCode, out.BaseResp, q, firstNonBlank(out.TraceID, resp.Header.Get("Trace-Id")))
	}
	if out.Data == nil || out.Data.Audio == "" {
		return nil, &RetryableError{fmt.Errorf("MiniMax 未返回音频（trace_id=%s）", out.TraceID)}
	}
	data, err := hex.DecodeString(out.Data.Audio)
	if err != nil {
		return nil, fmt.Errorf("MiniMax 返回的音频无法解码：%w", err)
	}
	return miniMaxAudio(data, q.SampleRate), nil
}

// flexID accepts ids sent either as JSON numbers or strings.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		s = ""
	}
	*f = flexID(s)
	return nil
}

func (m *miniMax) asyncSynth(ctx context.Context, q MiniMaxRequest) (*Audio, error) {
	if err := m.gate.Wait(ctx); err != nil {
		return nil, err
	}
	resp, raw, err := m.do(ctx, http.MethodPost, "/v1/t2a_async_v2", nil, q.AsyncBody(), 1<<20)
	if err != nil {
		return nil, err
	}
	var created struct {
		TaskID   flexID      `json:"task_id"`
		FileID   flexID      `json:"file_id"`
		BaseResp miniMaxBase `json:"base_resp"`
	}
	_ = json.Unmarshal(raw, &created)
	if resp.StatusCode != http.StatusOK || created.BaseResp.StatusCode != 0 {
		m.cooldown(resp.StatusCode, created.BaseResp.StatusCode)
		return nil, miniMaxError(resp.StatusCode, created.BaseResp, q, resp.Header.Get("Trace-Id"))
	}
	if created.TaskID == "" {
		return nil, &RetryableError{errors.New("MiniMax 未返回异步任务编号")}
	}
	fileID, err := m.waitTask(ctx, string(created.TaskID), q)
	if err != nil {
		return nil, err
	}
	return m.download(ctx, fileID, q.SampleRate)
}

// waitTask polls the async task until it finishes (the result file id) or fails.
func (m *miniMax) waitTask(ctx context.Context, taskID string, q MiniMaxRequest) (string, error) {
	deadline := time.Now().Add(30 * time.Minute)
	delay := miniMaxPollDelay
	transient := 0
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay += delay / 2
		}
		resp, raw, err := m.do(ctx, http.MethodGet, "/v1/query/t2a_async_query_v2", url.Values{"task_id": {taskID}}, nil, 1<<20)
		if err != nil {
			var re *RetryableError
			if errors.As(err, &re) && transient < 5 {
				transient++
				continue
			}
			return "", err
		}
		var st struct {
			Status   string      `json:"status"`
			FileID   flexID      `json:"file_id"`
			BaseResp miniMaxBase `json:"base_resp"`
		}
		_ = json.Unmarshal(raw, &st)
		if resp.StatusCode != http.StatusOK || st.BaseResp.StatusCode != 0 {
			e := miniMaxError(resp.StatusCode, st.BaseResp, q, "")
			var re *RetryableError
			if errors.As(e, &re) && transient < 5 {
				transient++
				continue
			}
			return "", e
		}
		transient = 0
		switch strings.ToLower(st.Status) {
		case "success":
			if st.FileID == "" {
				return "", errors.New("MiniMax 异步任务已完成但没有返回文件")
			}
			return string(st.FileID), nil
		case "failed":
			return "", fmt.Errorf("MiniMax 异步合成任务失败（task_id=%s）%s", taskID, strings.TrimSpace(" "+st.BaseResp.StatusMsg))
		case "expired":
			return "", fmt.Errorf("MiniMax 异步合成任务已过期（task_id=%s），请重新生成", taskID)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("MiniMax 异步合成超过 30 分钟仍未完成（task_id=%s）", taskID)
		}
	}
}

func (m *miniMax) download(ctx context.Context, fileID string, rate int) (*Audio, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, raw, err := m.do(ctx, http.MethodGet, "/v1/files/retrieve_content", url.Values{"file_id": {fileID}}, nil, 500<<20)
		if err == nil {
			if resp.StatusCode == http.StatusOK && len(raw) > 0 && !looksLikeJSON(raw) {
				data, name, xerr := extractMiniMaxAudio(raw)
				if xerr != nil {
					return nil, xerr
				}
				a := miniMaxAudio(data, rate)
				if ext := strings.TrimPrefix(path.Ext(name), "."); ext != "" && ext != "wav" {
					a.Format = ext
				}
				return a, nil
			}
			var e struct {
				BaseResp miniMaxBase `json:"base_resp"`
			}
			_ = json.Unmarshal(raw, &e)
			err = miniMaxError(resp.StatusCode, e.BaseResp, MiniMaxRequest{}, "")
			var re *RetryableError
			if !errors.As(err, &re) && resp.StatusCode < 500 {
				return nil, fmt.Errorf("下载 MiniMax 合成结果失败：%w", err)
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return nil, fmt.Errorf("下载 MiniMax 合成结果失败：%v", lastErr)
}

func looksLikeJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{'
}

func miniMaxAudio(data []byte, rate int) *Audio {
	format := "wav"
	switch {
	case bytes.HasPrefix(data, []byte("RIFF")):
		data = audio.FixWAVSizes(data)
	case bytes.HasPrefix(data, []byte("fLaC")):
		format = "flac"
	case bytes.HasPrefix(data, []byte("ID3")) || (len(data) > 1 && data[0] == 0xFF && data[1]&0xE0 == 0xE0):
		format = "mp3"
	case bytes.HasPrefix(data, []byte("OggS")):
		format = "ogg"
	}
	return &Audio{Data: data, Format: format, SampleRate: rate}
}

var audioExts = map[string]int{".wav": 0, ".flac": 1, ".mp3": 2, ".opus": 3, ".ogg": 3, ".pcm": 4}

// extractMiniMaxAudio pulls the audio out of an async result (tar / tar.gz / zip archive, or bare audio).
func extractMiniMaxAudio(b []byte) ([]byte, string, error) {
	type entry struct {
		name string
		data []byte
	}
	var found []entry
	add := func(name string, r io.Reader) error {
		base := path.Base(name)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		if _, ok := audioExts[strings.ToLower(path.Ext(base))]; !ok {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(r, 500<<20))
		if err != nil {
			return err
		}
		found = append(found, entry{base, data})
		return nil
	}
	if bytes.HasPrefix(b, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, "", fmt.Errorf("MiniMax 合成结果无法解压：%w", err)
		}
		un, err := io.ReadAll(io.LimitReader(zr, 1<<30))
		if err != nil {
			return nil, "", fmt.Errorf("MiniMax 合成结果无法解压：%w", err)
		}
		b = un
	}
	switch {
	case len(b) > 262 && string(b[257:262]) == "ustar":
		tr := tar.NewReader(bytes.NewReader(b))
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", fmt.Errorf("MiniMax 合成结果解包失败：%w", err)
			}
			if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
				if err := add(h.Name, tr); err != nil {
					return nil, "", err
				}
			}
		}
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return nil, "", fmt.Errorf("MiniMax 合成结果解包失败：%w", err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, "", err
			}
			err = add(f.Name, rc)
			rc.Close()
			if err != nil {
				return nil, "", err
			}
		}
	default:
		return b, "audio.wav", nil // bare audio file
	}
	if len(found) == 0 {
		return nil, "", errors.New("MiniMax 合成结果中没有找到音频文件")
	}
	sort.SliceStable(found, func(i, j int) bool {
		return audioExts[strings.ToLower(path.Ext(found[i].name))] < audioExts[strings.ToLower(path.Ext(found[j].name))]
	})
	return found[0].data, found[0].name, nil
}

// --- errors, retries, rate limiting ---------------------------------------------

func miniMaxError(status int, br miniMaxBase, q MiniMaxRequest, trace string) error {
	code, msg := br.StatusCode, strings.TrimSpace(br.StatusMsg)
	suffix := ""
	if trace != "" {
		suffix = "，trace_id=" + trace
	}
	retry := func(s string) error { return &RetryableError{fmt.Errorf("%s（%s%s）", s, firstNonBlank(msg, strconv.Itoa(code)), suffix)} }
	switch code {
	case 1004, 2049:
		return errors.New("MiniMax API Key 无效或已失效，请在管理端检查账号配置")
	case 1008:
		return errors.New("MiniMax 账户余额不足，请充值后重试")
	case 2053, 2067:
		return errors.New("MiniMax 账户的积分或套餐额度已用完，请充值或升级套餐后重试")
	case 1002, 1039, 2045, 2046, 2056, 2062:
		return retry("MiniMax 请求过于频繁，已自动放慢重试")
	case 1041, 1045, 2047:
		return retry("MiniMax 并发数超限，已自动排队重试")
	case 1000, 1001, 1024, 1033, 2064, 2066:
		return retry("MiniMax 语音服务暂时不可用")
	case 1026:
		return errors.New("这段文本触发了 MiniMax 的内容安全审核，请修改后重试")
	case 1027:
		return errors.New("合成结果触发了 MiniMax 的内容安全审核，请调整文本后重试")
	case 1042:
		return errors.New("文本中不可见字符或非法字符超过 10%，请检查是否混入了特殊字符")
	case 2014:
		return errors.New("待合成文本为空")
	case 2022, 2061, 2068:
		return fmt.Errorf("MiniMax 账号无权使用模型 %s（或当前套餐不支持），请在管理端更换模型", firstNonBlank(q.Model, "（未知）"))
	case 2069:
		return fmt.Errorf("MiniMax 模型 %s 已下线，请在管理端更换为在售模型", firstNonBlank(q.Model, "（未知）"))
	case 2054, 20132:
		return fmt.Errorf("音色「%s」在 MiniMax 上不存在，请换一个音色", q.VoiceID)
	case 2042:
		return fmt.Errorf("没有权限使用音色「%s」（它属于其他 MiniMax 账号）", q.VoiceID)
	case 2043:
		return fmt.Errorf("音色「%s」不能用于模型 %s，请换一个音色或模型", q.VoiceID, q.Model)
	case 2013:
		return fmt.Errorf("MiniMax 请求参数错误：%s", firstNonBlank(msg, "未知参数问题"))
	case 1013:
		return retry("MiniMax 服务内部错误")
	// voice cloning
	case 2037:
		return errors.New("样本时长不符合 MiniMax 的要求：需要 10 秒到 5 分钟的清晰朗读")
	case 2038:
		return errors.New("MiniMax 账号还没有完成实名认证，暂时不能复刻声音。请管理员在 MiniMax 控制台完成个人或企业认证")
	case 2039:
		return errors.New("MiniMax 上已有同名音色 ID，请重试")
	case 2048:
		return errors.New("示例音频太长（MiniMax 要求短于 8 秒）")
	case 2051:
		return errors.New("样本音质没有通过 MiniMax 的检测，请换一段清晰、没有明显噪音和回声的录音")
	case 2052:
		return errors.New("MiniMax 账号的复刻音色名额已满，请删除不用的复刻音色或升级 MiniMax 套餐")
	case 1040:
		return errors.New("样本音频没有通过 MiniMax 的内容审核，请换一段正常朗读的录音")
	case 1043, 1044:
		return fmt.Errorf("样本与文本的比对没有通过 MiniMax 的校验：%s", firstNonBlank(msg, "相似度不足"))
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errors.New("MiniMax API Key 无效或没有权限，请在管理端检查账号配置")
	case status == http.StatusTooManyRequests:
		return retry("MiniMax 请求过于频繁，已自动放慢重试")
	case status >= 500:
		return retry(fmt.Sprintf("MiniMax 语音服务暂时不可用（HTTP %d）", status))
	}
	if code == 0 && status == http.StatusOK {
		code = -1
	}
	return fmt.Errorf("MiniMax 语音合成失败：%s（错误码 %d%s）", firstNonBlank(msg, "未知错误"), code, suffix)
}

// backoff between attempts after rate-limit / transient failures (variables so tests can shorten them)
var (
	miniMaxRetryDelays = []time.Duration{2 * time.Second, 5 * time.Second, 12 * time.Second, 25 * time.Second}
	miniMaxPollDelay   = 1500 * time.Millisecond
	miniMaxThrottle    = 10 * time.Second // pause for the whole account after the platform reports throttling
)

// withRetry retries rate-limit and transient failures with exponential backoff.
func withRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	delays := miniMaxRetryDelays
	for i := 0; ; i++ {
		a, err := fn()
		var re *RetryableError
		if err == nil || !errors.As(err, &re) || i >= len(delays) {
			return a, err
		}
		jitter := time.Duration(rand.Int63n(int64(delays[i]/4) + 1))
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-time.After(delays[i] + jitter):
		}
	}
}

func (m *miniMax) cooldown(status, code int) {
	switch code {
	case 1002, 1039, 2045, 2056, 2062:
		m.gate.Pause(miniMaxThrottle)
	default:
		if status == http.StatusTooManyRequests {
			m.gate.Pause(miniMaxThrottle)
		}
	}
}

// rateGate spaces requests of one account evenly so a whole chapter never trips the platform's RPM limit.
type rateGate struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

var gates sync.Map

func gateFor(key string, rpm int) *rateGate {
	iv := time.Minute / time.Duration(rpm)
	v, _ := gates.LoadOrStore(key, &rateGate{interval: iv})
	g := v.(*rateGate)
	g.mu.Lock()
	g.interval = iv // account settings may have changed
	g.mu.Unlock()
	return g
}

// Wait blocks until the caller may send its request.
func (g *rateGate) Wait(ctx context.Context) error {
	g.mu.Lock()
	now := time.Now()
	t := g.next
	if t.Before(now) {
		t = now
	}
	g.next = t.Add(g.interval)
	g.mu.Unlock()
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Pause pushes the next free slot out after the platform reported throttling.
func (g *rateGate) Pause(d time.Duration) {
	g.mu.Lock()
	if t := time.Now().Add(d); t.After(g.next) {
		g.next = t
	}
	g.mu.Unlock()
}

// --- voice listing ------------------------------------------------------------

// ListVoices merges the account's live system-voice list with the built-in metadata.
func (m *miniMax) ListVoices(ctx context.Context) ([]RemoteVoice, error) {
	if m.acc.APIKey == "" {
		return nil, errors.New("MiniMax 账号缺少 API Key")
	}
	if err := m.gate.Wait(ctx); err != nil {
		return nil, err
	}
	resp, raw, err := m.do(ctx, http.MethodPost, "/v1/get_voice", nil, map[string]any{"voice_type": "system"}, 20<<20)
	if err != nil {
		return nil, err
	}
	var out struct {
		SystemVoice []struct {
			VoiceID     string   `json:"voice_id"`
			VoiceName   string   `json:"voice_name"`
			Description []string `json:"description"`
		} `json:"system_voice"`
		BaseResp miniMaxBase `json:"base_resp"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.BaseResp.StatusCode != 0 {
		return nil, miniMaxError(resp.StatusCode, out.BaseResp, MiniMaxRequest{}, "")
	}
	models := append([]string{}, MiniMaxCatalogModels...)
	if md := m.acc.Model; md != "" && !containsStr(models, md) {
		models = append(models, md)
	}
	var list []RemoteVoice
	for _, sv := range out.SystemVoice {
		id := strings.TrimSpace(sv.VoiceID)
		if id == "" {
			continue
		}
		v := MiniMaxLookup(id)
		if v == nil {
			v = guessMiniMaxVoice(id, sv.VoiceName, sv.Description)
		}
		for _, md := range models {
			list = append(list, v.Remote(md))
		}
	}
	return list, nil
}

// guessMiniMaxVoice fills in metadata for system voices newer than the built-in list.
func guessMiniMaxVoice(id, name string, desc []string) *MiniMaxVoice {
	v := &MiniMaxVoice{ID: id, Name: firstNonBlank(strings.TrimSpace(name), id), Gender: "unknown", AgeGroup: "adult", Language: "zh-CN", LangLabel: "普通话", Traits: strings.Join(desc, "，")}
	low := strings.ToLower(id + " " + name + " " + strings.Join(desc, " "))
	hasHan := false
	for _, r := range name {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			break
		}
	}
	prefixes := map[string][2]string{
		"english": {"en-US", "英语"}, "japanese": {"ja-JP", "日语"}, "korean": {"ko-KR", "韩语"}, "spanish": {"es", "西班牙语"},
		"portuguese": {"pt", "葡萄牙语"}, "french": {"fr", "法语"}, "german": {"de", "德语"}, "russian": {"ru", "俄语"},
		"italian": {"it", "意大利语"}, "indonesian": {"id-ID", "印尼语"}, "arabic": {"ar", "阿拉伯语"}, "turkish": {"tr", "土耳其语"},
		"cantonese": {"zh-HK", "粤语"}, "thai": {"th", "泰语"}, "vietnamese": {"vi", "越南语"}, "hindi": {"hi", "印地语"},
	}
	for p, l := range prefixes {
		if strings.HasPrefix(strings.ToLower(id), p) {
			v.Language, v.LangLabel = l[0], l[1]
		}
	}
	if !hasHan && v.Language == "zh-CN" {
		v.Language, v.LangLabel = "en-US", "英语"
	}
	switch {
	case strings.Contains(low, "female") || strings.Contains(low, "woman") || strings.Contains(low, "girl") || strings.Contains(low, "lady") || strings.ContainsAny(name, "女姐妹娘婶奶"):
		v.Gender = "female"
	case strings.Contains(low, "male") || strings.Contains(low, "man") || strings.Contains(low, "boy") || strings.ContainsAny(name, "男哥弟叔爷"):
		v.Gender = "male"
	}
	if v.Language != "zh-CN" && v.LangLabel != "" && !strings.Contains(v.Name, "（") {
		v.Name += "（" + v.LangLabel + "）"
	}
	v.Tags = []string{v.LangLabel}
	return v
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- voice cloning (quick clone) ------------------------------------------------

// MiniMaxCloneVoiceID derives a platform voice id from our voice id: 8-256 chars, starts with a letter,
// letters / digits / - / _ only, not ending with - or _.
func MiniMaxCloneVoiceID(seed string, attempt int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(seed) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	core := b.String()
	if len(core) > 48 {
		core = core[:48]
	}
	if len(core) < 8 {
		sum := sha256.Sum256([]byte(seed))
		core += hex.EncodeToString(sum[:8])
	}
	id := "vb" + core
	if attempt > 0 {
		id += fmt.Sprintf("r%d", attempt)
	}
	return id
}

var cloneSensitive = map[int]string{1: "严重违规", 2: "色情", 3: "广告", 4: "违禁", 5: "谩骂", 6: "暴恐", 7: "其他"}

// CloneVoice uploads the merged sample and creates a quick clone. MiniMax returns at once; the clone is deleted
// by the platform if it is not used for synthesis within 7 days, and its first synthesis is billed.
func (m *miniMax) CloneVoice(ctx context.Context, r CloneRequest) (*CloneResult, error) {
	if m.acc.APIKey == "" {
		return nil, errors.New("MiniMax 账号缺少 API Key")
	}
	if r.Tier == "pro" {
		return nil, errors.New("MiniMax 只提供快速复刻，请选择「快速克隆」")
	}
	sample := r.MergedPath
	if sample == "" && len(r.SamplePaths) > 0 {
		sample = r.SamplePaths[0]
	}
	if sample == "" {
		return nil, errors.New("没有可用的声音样本")
	}
	if err := checkMiniMaxCloneSample(sample); err != nil {
		return nil, err
	}
	fileID, err := withRetry(ctx, func() (string, error) { return m.uploadFile(ctx, sample, "voice_clone") })
	if err != nil {
		return nil, err
	}
	fid, err := strconv.ParseInt(fileID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("MiniMax 返回了无效的文件编号：%s", fileID)
	}
	model := m.model("")
	for attempt := 0; attempt < 3; attempt++ {
		voiceID := MiniMaxCloneVoiceID(firstNonBlank(r.Seed, r.Name, fileID), attempt)
		body := map[string]any{"file_id": fid, "voice_id": voiceID, "need_noise_reduction": r.Denoise, "need_volume_normalization": true}
		if t := strings.TrimSpace(r.PreviewText); t != "" {
			body["text"], body["model"] = t, model
			if boost := miniMaxLanguageBoost(r.Language, "", m.opt.LanguageBoost); boost != "" {
				body["language_boost"] = boost
			}
			if m.opt.Watermark {
				body["aigc_watermark"] = true
			}
		}
		type cloneResp struct {
			InputSensitive *struct {
				Type int `json:"type"`
			} `json:"input_sensitive"`
			DemoAudio string      `json:"demo_audio"`
			BaseResp  miniMaxBase `json:"base_resp"`
		}
		out, err := withRetry(ctx, func() (cloneResp, error) {
			var cr cloneResp
			if err := m.gate.Wait(ctx); err != nil {
				return cr, err
			}
			resp, raw, err := m.do(ctx, http.MethodPost, "/v1/voice_clone", nil, body, 4<<20)
			if err != nil {
				return cr, err
			}
			_ = json.Unmarshal(raw, &cr)
			if resp.StatusCode != http.StatusOK || cr.BaseResp.StatusCode != 0 {
				m.cooldown(resp.StatusCode, cr.BaseResp.StatusCode)
				if cr.BaseResp.StatusCode == 2039 {
					return cr, errDuplicateVoiceID
				}
				return cr, miniMaxError(resp.StatusCode, cr.BaseResp, MiniMaxRequest{Model: model, VoiceID: voiceID}, resp.Header.Get("Trace-Id"))
			}
			return cr, nil
		})
		if errors.Is(err, errDuplicateVoiceID) {
			continue // the id is taken (e.g. an earlier attempt succeeded remotely): use the next one
		}
		if err != nil {
			return nil, err
		}
		if out.InputSensitive != nil && out.InputSensitive.Type != 0 {
			_ = m.DeleteVoice(ctx, voiceID)
			return nil, fmt.Errorf("样本音频没有通过 MiniMax 的内容审核（%s），请换一段正常朗读的录音", firstNonBlank(cloneSensitive[out.InputSensitive.Type], "其他"))
		}
		res := &CloneResult{ProviderVoiceID: voiceID, Status: "ready", ExpiresInDays: 7, PriceTier: MiniMaxPriceTier(model),
			Message: "MiniMax 复刻完成。7 天内用它生成一次配音即可长期保留，否则 MiniMax 会自动删除"}
		if out.DemoAudio != "" {
			if data, err := m.fetchPublic(ctx, out.DemoAudio); err == nil {
				res.Preview = miniMaxAudio(data, 32000)
			}
		}
		return res, nil
	}
	return nil, errors.New("MiniMax 上的音色 ID 一直重复，请稍后重试")
}

var errDuplicateVoiceID = errors.New("duplicate voice id")

// CloneStatus: MiniMax clones are ready as soon as the clone call returns.
func (m *miniMax) CloneStatus(_ context.Context, id string) (*CloneResult, error) {
	return &CloneResult{ProviderVoiceID: id, Status: "ready"}, nil
}

// DeleteVoice removes a cloned voice on MiniMax; a voice that no longer exists counts as deleted.
func (m *miniMax) DeleteVoice(ctx context.Context, id string) error {
	if m.acc.APIKey == "" || id == "" {
		return nil
	}
	if err := m.gate.Wait(ctx); err != nil {
		return err
	}
	resp, raw, err := m.do(ctx, http.MethodPost, "/v1/delete_voice", nil, map[string]any{"voice_type": "voice_cloning", "voice_id": id}, 1<<20)
	if err != nil {
		return err
	}
	var out struct {
		BaseResp miniMaxBase `json:"base_resp"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode == http.StatusOK && (out.BaseResp.StatusCode == 0 || out.BaseResp.StatusCode == 2054 || out.BaseResp.StatusCode == 20132) {
		return nil
	}
	return miniMaxError(resp.StatusCode, out.BaseResp, MiniMaxRequest{VoiceID: id}, "")
}

// checkMiniMaxCloneSample enforces the platform's sample rules before uploading.
func checkMiniMaxCloneSample(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("读取声音样本失败：%w", err)
	}
	if st.Size() > 20<<20 {
		return fmt.Errorf("声音样本超过 20 MB（MiniMax 上限），当前 %.1f MB", float64(st.Size())/(1<<20))
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".wav" && ext != ".mp3" && ext != ".m4a" {
		return fmt.Errorf("MiniMax 只接受 wav、mp3、m4a 格式的样本（当前 %s）", ext)
	}
	if ext == ".wav" {
		if pcm, err := audio.ReadWAVFile(p); err == nil {
			sec := float64(pcm.DurationMs()) / 1000
			if sec < 10 {
				return fmt.Errorf("声音样本太短：MiniMax 要求至少 10 秒，当前约 %.0f 秒，请再录一段", sec)
			}
			if sec > 300 {
				return fmt.Errorf("声音样本超过 5 分钟（MiniMax 上限），当前约 %.0f 秒", sec)
			}
		}
	}
	return nil
}

// uploadFile sends a local file to POST /v1/files/upload and returns its file id.
func (m *miniMax) uploadFile(ctx context.Context, p, purpose string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("purpose", purpose)
	part, err := w.CreateFormFile("file", filepath.Base(p))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	w.Close()
	if err := m.gate.Wait(ctx); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint("/v1/files/upload", nil), bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.acc.APIKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &RetryableError{fmt.Errorf("上传样本到 MiniMax 失败：%w", err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		File struct {
			FileID flexID `json:"file_id"`
		} `json:"file"`
		BaseResp miniMaxBase `json:"base_resp"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.BaseResp.StatusCode != 0 {
		m.cooldown(resp.StatusCode, out.BaseResp.StatusCode)
		return "", miniMaxError(resp.StatusCode, out.BaseResp, MiniMaxRequest{}, resp.Header.Get("Trace-Id"))
	}
	if out.File.FileID == "" {
		return "", errors.New("MiniMax 没有返回上传文件的编号")
	}
	return string(out.File.FileID), nil
}

// fetchPublic downloads a result link returned by MiniMax (e.g. the clone preview).
func (m *miniMax) fetchPublic(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}
