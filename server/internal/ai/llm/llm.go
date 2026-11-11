// Package llm provides real LLM clients for three wire protocols:
// OpenAI Chat Completions, OpenAI Responses, and Anthropic Claude Messages.
// Administrators configure accounts (protocol, base URL, key, model) in the console.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

type Request struct {
	System     string
	Messages   []Message
	Schema     map[string]any // when set, the response must be a JSON object matching it
	SchemaName string
	MaxTokens  int
	Effort     string // low | medium | high (Claude effort / OpenAI reasoning effort)
}

type Response struct {
	Text         string
	Model        string
	InputTokens  int
	OutputTokens int
	StopReason   string
	LatencyMs    int64
}

type Client interface {
	Chat(ctx context.Context, req Request) (*Response, error)
	Protocol() string
	Model() string
}

type Config struct {
	Protocol    string
	BaseURL     string
	APIKey      string
	Model       string
	JSONMode    string // json_schema | json_object | prompt
	Temperature *float64
	MaxTokens   int
	Effort      string
	Timeout     time.Duration
	Headers     map[string]string
	// OpenAI chat: send max_completion_tokens instead of max_tokens (reasoning models).
	MaxCompletionTokens bool
	// Anthropic: opt into server-side refusal fallbacks (first-party API only).
	Fallbacks bool
}

// FromMap builds a Config from an account's free-form config map.
func FromMap(protocol, baseURL, apiKey, model string, m map[string]any) Config {
	c := Config{Protocol: protocol, BaseURL: baseURL, APIKey: apiKey, Model: model, Fallbacks: true}
	if v, ok := m["jsonMode"].(string); ok {
		c.JSONMode = v
	}
	if v, ok := num(m["temperature"]); ok {
		c.Temperature = &v
	}
	if v, ok := num(m["maxTokens"]); ok {
		c.MaxTokens = int(v)
	}
	if v, ok := m["effort"].(string); ok {
		c.Effort = v
	}
	if v, ok := num(m["timeoutSec"]); ok && v > 0 {
		c.Timeout = time.Duration(v) * time.Second
	}
	if v, ok := m["maxCompletionTokens"].(bool); ok {
		c.MaxCompletionTokens = v
	}
	if v, ok := m["fallbacks"].(bool); ok {
		c.Fallbacks = v
	}
	if h, ok := m["headers"].(map[string]any); ok {
		c.Headers = map[string]string{}
		for k, v := range h {
			c.Headers[k] = fmt.Sprint(v)
		}
	}
	return c
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func New(c Config) (Client, error) {
	if c.Model == "" {
		return nil, errors.New("未配置模型名称")
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Minute
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 16000
	}
	switch c.Protocol {
	case "openai_chat", "":
		if c.JSONMode == "" {
			c.JSONMode = "json_object"
		}
		return &openAIChat{cfg: c, http: &http.Client{Timeout: c.Timeout}}, nil
	case "openai_responses":
		if c.JSONMode == "" {
			c.JSONMode = "json_schema"
		}
		return &openAIResponses{cfg: c, http: &http.Client{Timeout: c.Timeout}}, nil
	case "anthropic":
		return newAnthropic(c), nil
	}
	return nil, fmt.Errorf("不支持的 LLM 协议：%s", c.Protocol)
}

// APIError is a provider HTTP error.
type APIError struct {
	Status     int
	Message    string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("LLM 接口错误 (HTTP %d)：%s", e.Status, e.Message)
}

func retryable(status int) bool {
	return status == 408 || status == 409 || status == 429 || status >= 500
}

// withRetry retries transient failures with exponential backoff.
func withRetry(ctx context.Context, attempts int, fn func() (*Response, error)) (*Response, error) {
	var last error
	for i := 0; i < attempts; i++ {
		resp, err := fn()
		if err == nil {
			return resp, nil
		}
		last = err
		var ae *APIError
		if errors.As(err, &ae) && !ae.Retryable {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		wait := time.Duration(1<<i) * 2 * time.Second
		if ae != nil && ae.RetryAfter > 0 {
			wait = ae.RetryAfter
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, last
}

func parseRetryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			return time.Duration(s) * time.Second
		}
	}
	return 0
}

// SchemaInstruction renders a JSON schema as an instruction for providers without native schema support.
func SchemaInstruction(schema map[string]any) string {
	b, _ := json.MarshalIndent(schema, "", "  ")
	return "\n\n【输出格式】只输出一个 JSON 对象，不要输出任何解释或 Markdown 代码块。JSON 必须符合以下 JSON Schema：\n" + string(b)
}

// ExtractJSON finds the outermost JSON object/array in model output (tolerates code fences / prose).
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	open, close := byte('{'), byte('}')
	if s[start] == '[' {
		open, close = '[', ']'
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

// ChatJSON performs a JSON-producing call and decodes into out. On a parse failure it asks the
// model once to repair its output.
func ChatJSON(ctx context.Context, c Client, req Request, out any) (*Response, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	raw := ExtractJSON(resp.Text)
	if err := json.Unmarshal([]byte(raw), out); err == nil {
		return resp, nil
	} else {
		repair := req
		repair.Messages = append(append([]Message{}, req.Messages...),
			Message{Role: "assistant", Content: truncate(resp.Text, 6000)},
			Message{Role: "user", Content: "上面的输出不是合法 JSON（" + err.Error() + "）。请只输出修正后的完整 JSON 对象。"},
		)
		resp2, err2 := c.Chat(ctx, repair)
		if err2 != nil {
			return nil, fmt.Errorf("模型输出无法解析为 JSON：%v", err)
		}
		resp2.InputTokens += resp.InputTokens
		resp2.OutputTokens += resp.OutputTokens
		if err := json.Unmarshal([]byte(ExtractJSON(resp2.Text)), out); err != nil {
			return nil, fmt.Errorf("模型输出无法解析为 JSON：%v", err)
		}
		return resp2, nil
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
