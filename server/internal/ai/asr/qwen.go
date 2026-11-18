package asr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Qianwen (DashScope) short-audio recognition, official API:
//
//	POST {base}/api/v1/services/aigc/multimodal-generation/generation
//	Authorization: Bearer <key>
//	{"model": "qwen-audio-3.1-asr-flash",
//	 "input": {"messages": [{"role": "user", "content": [{"type": "input_audio", "input_audio": {"data": "data:audio/mpeg;base64,..."}}]}]},
//	 "parameters": {"format": "mp3", "language_hints": ["zh"]}}
//
// The reply carries the text and word timestamps (output.sentence.words). A request takes at most 5 minutes of
// audio, sent inline as a data URL, so no public link is needed; longer audio is cut into pieces first.

const (
	QwenASRDefaultBase  = "https://maas.qianwenaiapi.com"
	QwenASRDefaultModel = "qwen-audio-3.1-asr-flash"
	qwenASRMaxBytes     = 10 << 20
)

// QwenASRModels have word timestamps on the synchronous endpoint (qwen3-asr-flash does not).
var QwenASRModels = []string{"qwen-audio-3.1-asr-flash", "qwen-audio-3.0-asr-flash", "fun-asr-flash-2026-06-15"}

type qwenASR struct {
	base, key, model, workspace string
	http                        *http.Client
	retryWait                   time.Duration
}

func newQwen(a Account) *qwenASR {
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	for _, suffix := range []string{"/api/v1", "/compatible-mode/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if base == "" {
		base = QwenASRDefaultBase
	}
	model := strings.TrimSpace(a.Model)
	if model == "" || model == "whisper-1" {
		model = QwenASRDefaultModel
	}
	ws, _ := a.Config["workspace"].(string)
	return &qwenASR{base: base, key: strings.TrimSpace(a.APIKey), model: model, workspace: strings.TrimSpace(ws), http: &http.Client{Timeout: 5 * time.Minute}, retryWait: 2 * time.Second}
}

func (q *qwenASR) Name() string      { return "qwen" }
func (q *qwenASR) MaxBytes() int64   { return qwenASRMaxBytes }
func (q *qwenASR) MaxChunkMs() int64 { return 270_000 } // the service takes up to 5 minutes per request

var audioMime = map[string]string{
	"wav": "audio/wav", "mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac", "ogg": "audio/ogg", "opus": "audio/ogg",
	"flac": "audio/flac", "webm": "audio/webm", "amr": "audio/amr", "wma": "audio/x-ms-wma",
}

func audioFormat(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// qwenHints maps a book language to the service's language hint (empty: let the model decide).
func qwenHints(lang string) []string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "" {
		return nil
	}
	if l == "zh-hk" || l == "yue" || strings.HasPrefix(l, "yue") {
		return []string{"yue"}
	}
	base := strings.SplitN(l, "-", 2)[0]
	switch base {
	case "zh", "en", "ja", "ko", "de", "fr", "ru", "es", "pt", "it", "ar", "id", "th", "vi", "tr", "ms", "nl", "sv", "da", "fi", "no", "pl", "cs", "hu", "el", "ro", "hi", "fil", "tl":
		return []string{base}
	}
	return nil
}

type qwenASRReply struct {
	RequestID string `json:"request_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Output    struct {
		Text      string            `json:"text"`
		Sentence  *qwenASRSentence  `json:"sentence"`
		Sentences []qwenASRSentence `json:"sentences"`
		Choices   []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	} `json:"output"`
}

type qwenASRSentence struct {
	Text      string `json:"text"`
	BeginTime int64  `json:"begin_time"`
	EndTime   int64  `json:"end_time"`
	Words     []struct {
		Text        string `json:"text"`
		BeginTime   int64  `json:"begin_time"`
		EndTime     int64  `json:"end_time"`
		Punctuation string `json:"punctuation"`
	} `json:"words"`
}

func (q *qwenASR) post(ctx context.Context, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.base+"/api/v1/services/aigc/multimodal-generation/generation", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+q.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DashScope-SSE", "disable")
	if q.workspace != "" {
		req.Header.Set("X-DashScope-WorkSpace", q.workspace)
	}
	resp, err := q.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return resp.StatusCode, data, err
}

func (q *qwenASR) Transcribe(ctx context.Context, path, language string) (*Result, error) {
	if q.key == "" {
		return nil, errors.New("千问语音识别账号缺少 API Key")
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > qwenASRMaxBytes {
		return nil, fmt.Errorf("音频文件太大（%.1f MB），千问语音识别单次最多 10 MB、5 分钟", float64(st.Size())/(1<<20))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	format := audioFormat(path)
	mime := audioMime[format]
	if mime == "" {
		mime = "audio/" + format
	}
	params := map[string]any{"format": format}
	if hints := qwenHints(language); hints != nil {
		params["language_hints"] = hints
	}
	body, _ := json.Marshal(map[string]any{
		"model": q.model,
		"input": map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)}},
		}}}},
		"parameters": params,
	})
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * q.retryWait):
			}
		}
		status, data, err := q.post(ctx, body)
		if err != nil {
			lastErr = fmt.Errorf("连接千问语音识别失败：%w", err)
			continue
		}
		var out qwenASRReply
		_ = json.Unmarshal(data, &out)
		if status != http.StatusOK || out.Code != "" {
			e := qwenASRError(status, out.Code, out.Message, q.model, out.RequestID, data)
			if status == 429 || status >= 500 {
				lastErr = e
				continue
			}
			return nil, e
		}
		return out.result(), nil
	}
	return nil, lastErr
}

func (o *qwenASRReply) result() *Result {
	r := &Result{Text: strings.TrimSpace(o.Output.Text)}
	sents := o.Output.Sentences
	if len(sents) == 0 && o.Output.Sentence != nil {
		sents = []qwenASRSentence{*o.Output.Sentence}
	}
	var text strings.Builder
	for _, s := range sents {
		text.WriteString(s.Text)
		if s.EndTime > 0 || s.BeginTime > 0 {
			r.Segments = append(r.Segments, Segment{Text: s.Text, StartMs: s.BeginTime, EndMs: s.EndTime})
		}
		for _, w := range s.Words {
			r.Words = append(r.Words, Word{Text: w.Text + w.Punctuation, StartMs: w.BeginTime, EndMs: w.EndTime})
		}
	}
	if r.Text == "" {
		r.Text = text.String()
	}
	if r.Text == "" && len(o.Output.Choices) > 0 { // OpenAI-style reply (qwen3-asr-flash): text only
		var parts []struct {
			Text string `json:"text"`
		}
		c := o.Output.Choices[0].Message.Content
		if json.Unmarshal(c, &parts) == nil {
			for _, p := range parts {
				r.Text += p.Text
			}
		} else {
			_ = json.Unmarshal(c, &r.Text)
		}
	}
	if len(r.Words) == 0 {
		r.Words = wordsFromSegments(r.Segments)
	}
	return r
}

func qwenASRError(status int, code, msg, model, rid string, raw []byte) error {
	plain := ""
	switch {
	case code == "InvalidApiKey" || status == 401:
		plain = "千问语音识别的 API Key 无效，请检查后重新填写"
	case code == "Arrearage":
		plain = "千问账号已欠费，请充值后重试"
	case strings.Contains(code, "AccessDenied") || status == 403:
		plain = "这个千问账号没有开通模型 " + model + "，请在千问 AI 平台开通后重试"
	case strings.Contains(code, "ModelNotFound") || status == 404:
		plain = "千问 AI 平台没有找到模型 " + model + "，请检查账号配置里的模型名"
	case strings.Contains(code, "Throttling") || status == 429:
		plain = "千问语音识别调用太频繁，请稍后重试"
	case code == "DataInspectionFailed":
		plain = "音频内容没有通过千问的安全审核"
	case status >= 500 || code == "InternalError":
		plain = "千问语音识别服务暂时出错，请稍后重试"
	case status == 400 || code == "InvalidParameter":
		plain = "千问语音识别拒绝了这次请求（音频格式或参数不对）"
	default:
		plain = "千问语音识别失败"
	}
	if msg == "" {
		msg = truncate(strings.TrimSpace(string(raw)))
	}
	detail := strings.TrimSpace(code + " " + msg)
	if rid != "" {
		detail += "，request_id " + rid
	}
	return fmt.Errorf("%s（HTTP %d：%s）", plain, status, detail)
}

// Check verifies the key and model with a request that carries no audio: the service rejects it as invalid
// input after authenticating, so nothing is recognised or billed.
func (q *qwenASR) Check(ctx context.Context) (string, error) {
	if q.key == "" {
		return "", errors.New("千问语音识别账号缺少 API Key")
	}
	body, _ := json.Marshal(map[string]any{"model": q.model, "input": map[string]any{"messages": []any{}}, "parameters": map[string]any{"format": "wav"}})
	status, data, err := q.post(ctx, body)
	if err != nil {
		return "", fmt.Errorf("连接千问语音识别失败：%w", err)
	}
	var out qwenASRReply
	_ = json.Unmarshal(data, &out)
	switch {
	case status == http.StatusOK:
		return "千问语音识别连接正常，模型 " + q.model, nil
	case status == 400 && !strings.Contains(strings.ToLower(out.Message), "model"):
		return "千问语音识别密钥有效，模型 " + q.model + " 可用（检测时没有上传音频，不产生费用）", nil
	}
	return "", qwenASRError(status, out.Code, out.Message, q.model, out.RequestID, data)
}
