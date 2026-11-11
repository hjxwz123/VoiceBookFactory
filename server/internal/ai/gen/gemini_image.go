package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Google Nano Banana (Gemini image models).
// Docs: https://ai.google.dev/gemini-api/docs/image-generation
//
// The default protocol is the Interactions API (POST /v1beta/interactions), which the current
// docs use for every example. "generate_content" keeps the older models/{m}:generateContent call
// for relays that have not caught up.
//
// Account config (all optional):
//   protocol      interactions (default) | generate_content
//   imageSize     1K (default) | 2K | 4K | 512 (512 only on gemini-3.1-flash-image)
//   thinkingLevel "" (model default) | minimal | medium | high   (Interactions only)
//   apiRevision   default 2026-05-20
//   timeoutSec    default 300

const (
	DefaultGeminiImageModel = "gemini-nano-banana-2.1"
	geminiAPIRevision       = "2026-05-20"
)

type geminiImage struct {
	a  Account
	hc *http.Client
}

func newGeminiImage(a Account) *geminiImage { return &geminiImage{a: a, hc: httpClient(a.Config)} }

func (g *geminiImage) Name() string { return "gemini_image" }

func (g *geminiImage) model() string {
	m := strings.TrimPrefix(strings.TrimSpace(g.a.Model), "models/")
	if m == "" {
		return DefaultGeminiImageModel
	}
	return m
}

// endpoint joins the account's base URL with an API version, unless the base already names one.
func (g *geminiImage) endpoint(version, path string) string {
	b := strings.TrimRight(strings.TrimSpace(g.a.BaseURL), "/")
	if b == "" {
		b = "https://generativelanguage.googleapis.com"
	}
	for _, v := range []string{"/v1beta", "/v1alpha", "/v1"} {
		if strings.HasSuffix(b, v) {
			return b + path
		}
	}
	return b + "/" + version + path
}

func (g *geminiImage) headers() map[string]string {
	rev := cfgStr(g.a.Config, "apiRevision")
	if rev == "" {
		rev = geminiAPIRevision
	}
	// never send Authorization here: Google reads it as an OAuth token and rejects the call
	return map[string]string{"x-goog-api-key": g.a.APIKey, "Api-Revision": rev}
}

func (g *geminiImage) imageSize() string {
	s := strings.ToUpper(cfgStr(g.a.Config, "imageSize"))
	switch s {
	case "1K", "2K", "4K", "512":
		return s
	}
	return "1K"
}

// GenerateImage asks for one picture per call (the models do not reliably honour "give me N")
// and runs up to three calls at once. Partial success returns the images that did arrive.
func (g *geminiImage) GenerateImage(ctx context.Context, r ImageRequest) ([]File, error) {
	n := r.Count
	if n <= 0 {
		n = 1
	}
	prompt := r.Prompt
	if r.Negative != "" {
		prompt += "\n避免出现：" + r.Negative
	}
	aspect := aspectOf(r)
	one := g.interaction
	if cfgStr(g.a.Config, "protocol") == "generate_content" {
		one = g.generateContent
	}
	files := make([]File, n)
	errs := make([]error, n)
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			files[i], errs[i] = withRetry(ctx, func() (File, error) { return one(ctx, prompt, aspect) })
		}(i)
	}
	wg.Wait()
	var out []File
	var firstErr error
	for i := range files {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		out = append(out, files[i])
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

type ixContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	URI      string `json:"uri"`
	MimeType string `json:"mime_type"`
}

func (g *geminiImage) interaction(ctx context.Context, prompt, aspect string) (File, error) {
	m := g.model()
	format := map[string]any{"type": "image", "mime_type": "image/jpeg", "aspect_ratio": aspect, "image_size": g.imageSize()}
	if strings.HasPrefix(m, "gemini-2.5") { // the legacy model has a single fixed resolution
		delete(format, "image_size")
	}
	// store=false: covers and portraits are one-shot, so nothing needs to stay on Google's side
	body := map[string]any{"model": m, "input": prompt, "response_format": format, "store": false}
	if t := cfgStr(g.a.Config, "thinkingLevel"); t != "" {
		body["generation_config"] = map[string]any{"thinking_level": t}
	}
	st, data, err := doHTTP(ctx, g.hc, http.MethodPost, g.endpoint("v1beta", "/interactions"), g.headers(), body)
	if err == nil && st == 400 && strings.Contains(strings.ToLower(string(data)), "store") {
		delete(body, "store") // a revision that does not know the flag
		st, data, err = doHTTP(ctx, g.hc, http.MethodPost, g.endpoint("v1beta", "/interactions"), g.headers(), body)
	}
	if err != nil {
		if ctx.Err() != nil {
			return File{}, ctx.Err()
		}
		return File{}, &APIError{Vendor: "Gemini", Message: "连不上接口地址，请检查网络、代理或接口地址是否正确", Raw: clip(err.Error(), 200), Retryable: true}
	}
	if st >= 300 {
		return File{}, geminiError(st, data, m)
	}
	var res struct {
		Status string `json:"status"`
		Steps  []struct {
			Type    string      `json:"type"`
			Content []ixContent `json:"content"`
		} `json:"steps"`
		Outputs []ixContent `json:"outputs"` // older preview revisions
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return File{}, &APIError{Vendor: "Gemini", Status: st, Message: "返回内容无法解析，接口地址可能不是 Gemini 接口", Raw: clip(string(data), 200)}
	}
	var blocks []ixContent
	for _, s := range res.Steps {
		if s.Type == "" || s.Type == "model_output" {
			blocks = append(blocks, s.Content...)
		}
	}
	blocks = append(blocks, res.Outputs...)
	var text []string
	for _, c := range blocks {
		switch {
		case c.Type == "image" && c.Data != "":
			f, err := imageFile(c.Data, firstNonEmpty(c.MimeType, "image/jpeg"))
			if err != nil {
				return File{}, err
			}
			f.Meta = map[string]any{"vendor": "gemini", "model": m, "aspect": aspect, "imageSize": format["image_size"]}
			return f, nil
		case c.Type == "image" && c.URI != "":
			h := map[string]string{}
			if strings.Contains(c.URI, "googleapis.com") {
				h = g.headers()
			}
			f, err := fetchImage(ctx, g.hc, c.URI, h)
			if err != nil {
				return File{}, err
			}
			f.Meta = map[string]any{"vendor": "gemini", "model": m, "aspect": aspect}
			return f, nil
		case c.Type == "text" && strings.TrimSpace(c.Text) != "":
			text = append(text, c.Text)
		}
	}
	return File{}, noImage(res.Status, strings.Join(text, " "))
}

func (g *geminiImage) generateContent(ctx context.Context, prompt, aspect string) (File, error) {
	m := g.model()
	imgCfg := map[string]any{"aspectRatio": aspect}
	if !strings.HasPrefix(m, "gemini-2.5") {
		imgCfg["imageSize"] = g.imageSize()
	}
	body := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": prompt}}}},
		"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}, "imageConfig": imgCfg},
	}
	st, data, err := doHTTP(ctx, g.hc, http.MethodPost, g.endpoint("v1", "/models/"+m+":generateContent"), g.headers(), body)
	if err != nil {
		if ctx.Err() != nil {
			return File{}, ctx.Err()
		}
		return File{}, &APIError{Vendor: "Gemini", Message: "连不上接口地址，请检查网络、代理或接口地址是否正确", Raw: clip(err.Error(), 200), Retryable: true}
	}
	if st >= 300 {
		return File{}, geminiError(st, data, m)
	}
	type inline struct {
		MimeType  string `json:"mimeType"`
		MimeType2 string `json:"mime_type"`
		Data      string `json:"data"`
	}
	var res struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
			Content      struct {
				Parts []struct {
					Text        string  `json:"text"`
					Thought     bool    `json:"thought"`
					InlineData  *inline `json:"inlineData"`
					InlineData2 *inline `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return File{}, &APIError{Vendor: "Gemini", Status: st, Message: "返回内容无法解析，接口地址可能不是 Gemini 接口", Raw: clip(string(data), 200)}
	}
	if res.PromptFeedback.BlockReason != "" {
		return File{}, &APIError{Vendor: "Gemini", Code: res.PromptFeedback.BlockReason, Message: "画面描述没有通过 Google 的安全审核，请换一种说法再试", Raw: res.PromptFeedback.BlockReason}
	}
	var text []string
	reason := ""
	for _, c := range res.Candidates {
		reason = c.FinishReason
		for _, p := range c.Content.Parts {
			d := p.InlineData
			if d == nil {
				d = p.InlineData2
			}
			if d != nil && d.Data != "" && !p.Thought {
				f, err := imageFile(d.Data, firstNonEmpty(d.MimeType, d.MimeType2, "image/png"))
				if err != nil {
					return File{}, err
				}
				f.Meta = map[string]any{"vendor": "gemini", "model": m, "aspect": aspect}
				return f, nil
			}
			if p.Text != "" && !p.Thought {
				text = append(text, p.Text)
			}
		}
	}
	if strings.Contains(reason, "SAFETY") || reason == "PROHIBITED_CONTENT" || reason == "BLOCKLIST" {
		return File{}, &APIError{Vendor: "Gemini", Code: reason, Message: "生成的画面被 Google 的安全策略拦截，请调整描述后再试", Raw: reason}
	}
	return File{}, noImage(reason, strings.Join(text, " "))
}

func noImage(status, text string) *APIError {
	ae := &APIError{Vendor: "Gemini", Code: status, Message: "模型没有返回图片，请调整描述后再试"}
	if t := clip(text, 160); t != "" {
		ae.Message = "模型没有返回图片，它的回复是：" + t
	}
	return ae
}

// Check confirms the key and model through models.get, which is free.
func (g *geminiImage) Check(ctx context.Context) (string, error) {
	m := g.model()
	st, data, err := doHTTP(ctx, g.hc, http.MethodGet, g.endpoint("v1beta", "/models/"+m), g.headers(), nil)
	if err != nil {
		return "", &APIError{Vendor: "Gemini", Message: "连不上接口地址，请检查网络、代理或接口地址是否正确", Raw: clip(err.Error(), 200)}
	}
	if st >= 300 {
		return "", geminiError(st, data, m)
	}
	var info struct {
		DisplayName string `json:"displayName"`
	}
	json.Unmarshal(data, &info)
	name := m
	if info.DisplayName != "" {
		name = info.DisplayName + "（" + m + "）"
	}
	return fmt.Sprintf("连接正常：密钥有效，可以使用 %s。测试不会生成图片，也不产生费用", name), nil
}

func geminiError(status int, data []byte, model string) *APIError {
	var e struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	raw := strings.TrimSpace(string(data))
	code := ""
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		raw, code = e.Error.Message, e.Error.Status
	}
	low := strings.ToLower(raw)
	ae := &APIError{Vendor: "Gemini", Status: status, Code: code, Raw: clip(raw, 200)}
	switch {
	case strings.Contains(low, "api key not valid") || strings.Contains(low, "api_key_invalid") || status == 401:
		ae.Message = "API Key 无效，请在 Google AI Studio 重新生成后到后台「模型与供应商」里更新"
	case strings.Contains(low, "location is not supported") || strings.Contains(low, "not available in your country"):
		ae.Message = "服务器所在地区无法使用 Gemini API，请把服务部署到支持的地区，或改用中转接口地址"
	case status == 400 && (strings.Contains(low, "safety") || strings.Contains(low, "prohibited")):
		ae.Message = "画面描述没有通过 Google 的安全审核，请换一种说法再试"
	case status == 403:
		ae.Message = "密钥没有权限：请确认密钥所属的 Google Cloud 项目已启用 Gemini API，并已开通结算"
	case status == 404:
		ae.Message = fmt.Sprintf("找不到模型「%s」，请检查模型名", model)
	case status == 429:
		ae.Message, ae.Retryable = "请求太频繁或额度已用完，已自动重试；免费额度用完时需要在 Google AI Studio 开通结算", true
	case status >= 500:
		ae.Message, ae.Retryable = "Gemini 服务暂时不可用，已自动重试，请稍后再试", true
	default:
		ae.Message = "请求被拒绝"
	}
	return ae
}
