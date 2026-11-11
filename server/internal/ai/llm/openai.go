package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func defaultBase(base string) string {
	if base == "" {
		return "https://api.openai.com/v1"
	}
	return strings.TrimRight(base, "/")
}

func doJSON(ctx context.Context, hc *http.Client, url, key string, headers map[string]string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &APIError{Status: 0, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		msg := string(data)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil {
			if e.Error.Message != "" {
				msg = e.Error.Message
			} else if e.Message != "" {
				msg = e.Message
			}
		}
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return &APIError{Status: resp.StatusCode, Message: msg, Retryable: retryable(resp.StatusCode), RetryAfter: parseRetryAfter(resp.Header)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 LLM 响应失败：%v", err)
	}
	return nil
}

// ---------- OpenAI Chat Completions (also DeepSeek, Qwen/百炼, Moonshot, GLM, Ollama ...) ----------

type openAIChat struct {
	cfg  Config
	http *http.Client
}

func (c *openAIChat) Protocol() string { return "openai_chat" }
func (c *openAIChat) Model() string    { return c.cfg.Model }

func (c *openAIChat) Chat(ctx context.Context, r Request) (*Response, error) {
	system := r.System
	body := map[string]any{"model": c.cfg.Model}
	if r.Schema != nil {
		switch c.cfg.JSONMode {
		case "json_schema":
			name := r.SchemaName
			if name == "" {
				name = "result"
			}
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "schema": r.Schema, "strict": true}}
		case "json_object":
			body["response_format"] = map[string]any{"type": "json_object"}
			system += SchemaInstruction(r.Schema)
		default:
			system += SchemaInstruction(r.Schema)
		}
	}
	msgs := []map[string]string{}
	if system != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": system})
	}
	for _, m := range r.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body["messages"] = msgs
	maxTok := r.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}
	if c.cfg.MaxCompletionTokens {
		body["max_completion_tokens"] = maxTok
	} else {
		body["max_tokens"] = maxTok
	}
	if c.cfg.Temperature != nil {
		body["temperature"] = *c.cfg.Temperature
	}
	effort := r.Effort
	if effort == "" {
		effort = c.cfg.Effort
	}
	if effort != "" && c.cfg.MaxCompletionTokens {
		body["reasoning_effort"] = effort
	}
	return withRetry(ctx, 3, func() (*Response, error) {
		start := time.Now()
		var out struct {
			Model   string `json:"model"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := doJSON(ctx, c.http, defaultBase(c.cfg.BaseURL)+"/chat/completions", c.cfg.APIKey, c.cfg.Headers, body, &out); err != nil {
			return nil, err
		}
		if len(out.Choices) == 0 {
			return nil, &APIError{Status: 200, Message: "模型未返回内容", Retryable: true}
		}
		ch := out.Choices[0]
		if ch.Message.Refusal != "" {
			return nil, &APIError{Status: 200, Message: "模型拒绝了该请求：" + ch.Message.Refusal}
		}
		return &Response{
			Text: ch.Message.Content, Model: out.Model, StopReason: ch.FinishReason,
			InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
			LatencyMs: time.Since(start).Milliseconds(),
		}, nil
	})
}

// ---------- OpenAI Responses API ----------

type openAIResponses struct {
	cfg  Config
	http *http.Client
}

func (c *openAIResponses) Protocol() string { return "openai_responses" }
func (c *openAIResponses) Model() string    { return c.cfg.Model }

func (c *openAIResponses) Chat(ctx context.Context, r Request) (*Response, error) {
	instructions := r.System
	body := map[string]any{"model": c.cfg.Model, "store": false}
	if r.Schema != nil {
		switch c.cfg.JSONMode {
		case "json_schema":
			name := r.SchemaName
			if name == "" {
				name = "result"
			}
			body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": name, "schema": r.Schema, "strict": true}}
		case "json_object":
			body["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
			instructions += SchemaInstruction(r.Schema)
		default:
			instructions += SchemaInstruction(r.Schema)
		}
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	input := []map[string]any{}
	for _, m := range r.Messages {
		typ := "input_text"
		if m.Role == "assistant" {
			typ = "output_text"
		}
		input = append(input, map[string]any{"role": m.Role, "content": []map[string]any{{"type": typ, "text": m.Content}}})
	}
	body["input"] = input
	maxTok := r.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}
	body["max_output_tokens"] = maxTok
	if c.cfg.Temperature != nil {
		body["temperature"] = *c.cfg.Temperature
	}
	effort := r.Effort
	if effort == "" {
		effort = c.cfg.Effort
	}
	if effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	return withRetry(ctx, 3, func() (*Response, error) {
		start := time.Now()
		var out struct {
			Model             string `json:"model"`
			Status            string `json:"status"`
			IncompleteDetails *struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
			Output []struct {
				Type    string `json:"type"`
				Content []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Refusal string `json:"refusal"`
				} `json:"content"`
			} `json:"output"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := doJSON(ctx, c.http, defaultBase(c.cfg.BaseURL)+"/responses", c.cfg.APIKey, c.cfg.Headers, body, &out); err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, item := range out.Output {
			if item.Type != "message" {
				continue
			}
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					sb.WriteString(part.Text)
				case "refusal":
					return nil, &APIError{Status: 200, Message: "模型拒绝了该请求：" + part.Refusal}
				}
			}
		}
		stop := out.Status
		if out.IncompleteDetails != nil && out.IncompleteDetails.Reason != "" {
			stop = out.IncompleteDetails.Reason
		}
		if sb.Len() == 0 {
			return nil, &APIError{Status: 200, Message: "模型未返回文本（状态：" + stop + "）", Retryable: stop != "content_filter"}
		}
		return &Response{
			Text: sb.String(), Model: out.Model, StopReason: stop,
			InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens,
			LatencyMs: time.Since(start).Milliseconds(),
		}, nil
	})
}
