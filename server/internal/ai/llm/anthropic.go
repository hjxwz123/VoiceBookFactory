package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// DefaultClaudeModel is used when an Anthropic account is created without a model.
const DefaultClaudeModel = "claude-opus-5-5"

type anthropicClient struct {
	cfg    Config
	client anthropic.Client
}

func newAnthropic(c Config) *anthropicClient {
	opts := []option.RequestOption{
		option.WithAPIKey(c.APIKey),
		option.WithMaxRetries(3),
		option.WithRequestTimeout(c.Timeout),
	}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(c.BaseURL, "/")+"/"))
	}
	for k, v := range c.Headers {
		opts = append(opts, option.WithHeader(k, v))
	}
	return &anthropicClient{cfg: c, client: anthropic.NewClient(opts...)}
}

func (c *anthropicClient) Protocol() string { return "anthropic" }
func (c *anthropicClient) Model() string    { return c.cfg.Model }

// supportsServerFallback reports whether the model accepts `fallbacks: "default"` on the first-party API.
func (c *anthropicClient) supportsServerFallback() bool {
	if !c.cfg.Fallbacks {
		return false
	}
	if c.cfg.BaseURL != "" && !strings.Contains(c.cfg.BaseURL, "api.anthropic.com") {
		return false
	}
	m := c.cfg.Model
	return strings.HasPrefix(m, "claude-opus-5") || strings.HasPrefix(m, "claude-sonnet-5-5") || strings.HasPrefix(m, "claude-fable-5")
}

func (c *anthropicClient) Chat(ctx context.Context, r Request) (*Response, error) {
	maxTok := r.MaxTokens
	if maxTok == 0 {
		maxTok = c.cfg.MaxTokens
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.cfg.Model),
		MaxTokens: int64(maxTok),
	}
	if r.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: r.System}}
	}
	for _, m := range r.Messages {
		if m.Role == "assistant" {
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		} else {
			params.Messages = append(params.Messages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	effort := r.Effort
	if effort == "" {
		effort = c.cfg.Effort
	}
	if effort != "" {
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(effort)
	}
	if r.Schema != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: r.Schema}
	}
	var reqOpts []option.RequestOption
	if c.supportsServerFallback() {
		// Server-side refusal fallback: Anthropic re-serves a declined request on its recommended model.
		reqOpts = append(reqOpts,
			option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
			option.WithJSONSet("fallbacks", "default"),
		)
	}
	start := time.Now()
	msg, err := c.client.Messages.New(ctx, params, reqOpts...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return nil, &APIError{Status: apiErr.StatusCode, Message: apiErr.Error(), Retryable: retryable(apiErr.StatusCode)}
		}
		return nil, &APIError{Status: 0, Message: err.Error(), Retryable: true}
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return nil, &APIError{Status: 200, Message: "模型基于安全策略拒绝了该请求"}
	}
	var sb strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}
	if sb.Len() == 0 {
		return nil, &APIError{Status: 200, Message: "模型未返回文本（stop_reason=" + string(msg.StopReason) + "）", Retryable: msg.StopReason != anthropic.StopReasonMaxTokens}
	}
	return &Response{
		Text: sb.String(), Model: string(msg.Model), StopReason: string(msg.StopReason),
		InputTokens: int(msg.Usage.InputTokens), OutputTokens: int(msg.Usage.OutputTokens),
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}
