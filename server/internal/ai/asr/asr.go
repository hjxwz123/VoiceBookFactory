// Package asr provides speech recognition with word timestamps, used by smart alignment and
// smart review. The OpenAI-compatible adapter (/audio/transcriptions, verbose_json) is real and
// works with OpenAI, Groq and self-hosted Whisper servers; other platforms are reserved stubs.
package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrNotImplemented = errors.New("该平台的语音识别适配器为预留接口，尚未实现")

type Word struct {
	Text    string `json:"text"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

type Segment struct {
	Text    string `json:"text"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

type Result struct {
	Text     string    `json:"text"`
	Words    []Word    `json:"words"`
	Segments []Segment `json:"segments"`
	Language string    `json:"language"`
}

type Provider interface {
	Name() string
	MaxBytes() int64
	Transcribe(ctx context.Context, path, language string) (*Result, error)
}

type Account struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Config   map[string]any
}

func New(a Account) (Provider, error) {
	switch a.Provider {
	case "openai_compat", "openai", "groq", "whisper":
		base := strings.TrimRight(a.BaseURL, "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		model := a.Model
		if model == "" {
			model = "whisper-1"
		}
		return &openAICompat{base: base, key: a.APIKey, model: model, http: &http.Client{Timeout: 10 * time.Minute}}, nil
	case "aliyun", "volcengine", "tencent", "xfyun":
		return stub(a.Provider), nil
	}
	return nil, fmt.Errorf("未知的语音识别平台：%s", a.Provider)
}

type stub string

func (s stub) Name() string    { return string(s) }
func (s stub) MaxBytes() int64 { return 0 }
func (s stub) Transcribe(context.Context, string, string) (*Result, error) {
	return nil, ErrNotImplemented
}

type openAICompat struct {
	base, key, model string
	http             *http.Client
}

func (o *openAICompat) Name() string    { return "openai_compat" }
func (o *openAICompat) MaxBytes() int64 { return 24 << 20 }

func (o *openAICompat) Transcribe(ctx context.Context, path, language string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	mw.WriteField("model", o.model)
	mw.WriteField("response_format", "verbose_json")
	mw.WriteField("timestamp_granularities[]", "word")
	mw.WriteField("timestamp_granularities[]", "segment")
	if language != "" {
		mw.WriteField("language", strings.Split(language, "-")[0])
	}
	mw.Close()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/audio/transcriptions", bytes.NewReader(body.Bytes()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if o.key != "" {
			req.Header.Set("Authorization", "Bearer "+o.key)
		}
		resp, err := o.http.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("语音识别接口错误 HTTP %d: %s", resp.StatusCode, truncate(string(data)))
			time.Sleep(time.Duration(attempt+1) * 3 * time.Second)
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("语音识别接口错误 HTTP %d: %s", resp.StatusCode, truncate(string(data)))
		}
		var out struct {
			Text     string `json:"text"`
			Language string `json:"language"`
			Words    []struct {
				Word  string  `json:"word"`
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"words"`
			Segments []struct {
				Text  string  `json:"text"`
				Start float64 `json:"start"`
				End   float64 `json:"end"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("无法解析语音识别结果：%v", err)
		}
		r := &Result{Text: out.Text, Language: out.Language}
		for _, w := range out.Words {
			r.Words = append(r.Words, Word{Text: w.Word, StartMs: int64(w.Start * 1000), EndMs: int64(w.End * 1000)})
		}
		for _, s := range out.Segments {
			r.Segments = append(r.Segments, Segment{Text: s.Text, StartMs: int64(s.Start * 1000), EndMs: int64(s.End * 1000)})
		}
		if len(r.Words) == 0 {
			r.Words = wordsFromSegments(r.Segments)
		}
		return r, nil
	}
	return nil, lastErr
}

// wordsFromSegments spreads segment timings evenly over characters when a server has no word timestamps.
func wordsFromSegments(segs []Segment) []Word {
	var out []Word
	for _, s := range segs {
		runes := []rune(strings.TrimSpace(s.Text))
		if len(runes) == 0 {
			continue
		}
		step := float64(s.EndMs-s.StartMs) / float64(len(runes))
		for i, r := range runes {
			out = append(out, Word{Text: string(r), StartMs: s.StartMs + int64(float64(i)*step), EndMs: s.StartMs + int64(float64(i+1)*step)})
		}
	}
	return out
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
