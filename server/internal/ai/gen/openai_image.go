package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// OpenAI GPT Image, through the Image API (POST /v1/images/generations).
// Docs: https://developers.openai.com/api/docs/guides/image-generation
//
// Account config (all optional):
//   quality      auto (default) | low | medium | high
//   resolution   "" = about 1K (works on every GPT Image model) | "2k" (gpt-image-2 and later)
//   moderation   auto (default) | low
//   outputFormat jpeg (default) | png | webp
//   timeoutSec   default 300

const DefaultOpenAIImageModel = "gpt-image-2.6-flare"

type openAIImage struct {
	a  Account
	hc *http.Client
}

func newOpenAIImage(a Account) *openAIImage { return &openAIImage{a: a, hc: httpClient(a.Config)} }

func (o *openAIImage) Name() string { return "openai_image" }

func (o *openAIImage) base() string {
	if b := strings.TrimRight(strings.TrimSpace(o.a.BaseURL), "/"); b != "" {
		return b
	}
	return "https://api.openai.com/v1"
}

func (o *openAIImage) model() string {
	if o.a.Model != "" {
		return o.a.Model
	}
	return DefaultOpenAIImageModel
}

func (o *openAIImage) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + o.a.APIKey}
}

// customSizes reports whether the model accepts any WIDTHxHEIGHT (gpt-image-2 and later);
// gpt-image-1.x only knows the three fixed sizes.
func customSizes(model string) bool {
	return strings.HasPrefix(model, "gpt-image-") && !strings.HasPrefix(model, "gpt-image-1")
}

// openAISize picks a size every model accepts, or a sharper one when the account asks for 2K.
// 2K sizes keep both edges multiples of 16 and the ratio within 1:3..3:1, as the API requires.
func openAISize(model, resolution, aspect string) string {
	if strings.EqualFold(resolution, "2k") && customSizes(model) {
		if s, ok := map[string]string{
			"1:1": "2048x2048", "3:4": "1536x2048", "2:3": "1344x2016", "4:5": "1632x2048", "9:16": "1152x2048",
			"4:3": "2048x1536", "3:2": "2016x1344", "5:4": "2048x1632", "16:9": "2560x1440", "21:9": "2688x1152",
		}[aspect]; ok {
			return s
		}
	}
	var w, h int
	fmt.Sscanf(aspect, "%d:%d", &w, &h)
	switch {
	case w > 0 && h > 0 && h > w:
		return "1024x1536"
	case w > 0 && h > 0 && w > h:
		return "1536x1024"
	}
	return "1024x1024"
}

func (o *openAIImage) GenerateImage(ctx context.Context, r ImageRequest) ([]File, error) {
	n := r.Count
	if n <= 0 {
		n = 1
	}
	if n > 10 {
		n = 10
	}
	m := o.model()
	size := openAISize(m, cfgStr(o.a.Config, "resolution"), aspectOf(r))
	prompt := r.Prompt
	if r.Negative != "" {
		prompt += "\n避免出现：" + r.Negative
	}
	format := cfgStr(o.a.Config, "outputFormat")
	if format == "" {
		format = "jpeg"
	}
	body := map[string]any{"model": m, "prompt": prompt, "n": n, "size": size, "output_format": format}
	if format != "png" {
		body["output_compression"] = 92
	}
	if q := cfgStr(o.a.Config, "quality"); q != "" && q != "auto" {
		body["quality"] = q
	}
	if mod := cfgStr(o.a.Config, "moderation"); mod != "" && mod != "auto" {
		body["moderation"] = mod
	}
	// OpenAI asks for a stable, hashed end-user id to trace abuse; compatible relays may reject unknown fields
	if r.User != "" && strings.Contains(o.base(), "api.openai.com") {
		body["safety_identifier"] = r.User
	}
	type result struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
		OutputFormat string `json:"output_format"`
		Quality      string `json:"quality"`
		Size         string `json:"size"`
		Usage        *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	out, err := withRetry(ctx, func() (*result, error) {
		st, data, err := doHTTP(ctx, o.hc, http.MethodPost, o.base()+"/images/generations", o.headers(), body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &APIError{Vendor: "OpenAI", Message: "连不上接口地址，请检查网络、代理或接口地址是否正确", Raw: clip(err.Error(), 200), Retryable: true}
		}
		if st >= 300 {
			return nil, openAIError(st, data, m)
		}
		var res result
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, &APIError{Vendor: "OpenAI", Status: st, Message: "返回内容无法解析，接口地址可能不是 OpenAI 兼容的图片接口", Raw: clip(string(data), 200)}
		}
		return &res, nil
	})
	if err != nil {
		return nil, err
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "webp": "image/webp"}[firstNonEmpty(out.OutputFormat, format)]
	var files []File
	for _, d := range out.Data {
		var f File
		switch {
		case d.B64 != "":
			if f, err = imageFile(d.B64, mime); err != nil {
				return nil, err
			}
		case d.URL != "": // some compatible relays answer with a link instead of base64
			if f, err = fetchImage(ctx, o.hc, d.URL, nil); err != nil {
				return nil, err
			}
		default:
			continue
		}
		f.Meta = map[string]any{"vendor": "openai", "model": m, "size": firstNonEmpty(out.Size, size)}
		if out.Quality != "" {
			f.Meta["quality"] = out.Quality
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, &APIError{Vendor: "OpenAI", Message: "接口返回成功，但没有图片数据"}
	}
	return files, nil
}

// Check confirms the key works and the model is visible to it, without drawing anything.
func (o *openAIImage) Check(ctx context.Context) (string, error) {
	m := o.model()
	st, data, err := doHTTP(ctx, o.hc, http.MethodGet, o.base()+"/models/"+m, o.headers(), nil)
	if err != nil {
		return "", &APIError{Vendor: "OpenAI", Message: "连不上接口地址，请检查网络、代理或接口地址是否正确", Raw: clip(err.Error(), 200)}
	}
	switch {
	case st < 300:
		return fmt.Sprintf("连接正常：密钥有效，可以使用 %s。测试不会生成图片，也不产生费用", m), nil
	case st == 404 || st == 405:
		// some relays have no per-model endpoint; fall back to the list
		st2, data2, err2 := doHTTP(ctx, o.hc, http.MethodGet, o.base()+"/models", o.headers(), nil)
		if err2 == nil && st2 < 300 {
			var list struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(data2, &list) == nil && len(list.Data) > 0 {
				for _, x := range list.Data {
					if x.ID == m {
						return fmt.Sprintf("连接正常：密钥有效，可以使用 %s。测试不会生成图片，也不产生费用", m), nil
					}
				}
				return "", &APIError{Vendor: "OpenAI", Status: 404, Message: fmt.Sprintf("密钥有效，但这个账号的模型列表里没有「%s」，请检查模型名", m)}
			}
		}
		if st2 == 401 || st2 == 403 {
			return "", openAIError(st2, data2, m)
		}
		return "", openAIError(st, data, m)
	}
	return "", openAIError(st, data, m)
}

func openAIError(status int, data []byte, model string) *APIError {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	raw := strings.TrimSpace(string(data))
	code := ""
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		raw = e.Error.Message
		if s, ok := e.Error.Code.(string); ok {
			code = s
		}
		if code == "" {
			code = e.Error.Type
		}
	}
	low := strings.ToLower(raw + " " + code)
	ae := &APIError{Vendor: "OpenAI", Status: status, Code: code, Raw: clip(raw, 200)}
	switch {
	case strings.Contains(low, "moderation") || strings.Contains(low, "content_policy") || strings.Contains(low, "safety system"):
		ae.Message = "画面描述没有通过 OpenAI 的安全审核，请换一种说法再试"
	case status == 401:
		ae.Message = "API Key 无效或已过期，请在后台「模型与供应商」里更新"
	case status == 403 && strings.Contains(low, "verif"):
		ae.Message = "OpenAI 要求先完成组织验证才能使用 GPT Image 模型：请在 OpenAI 后台 Settings → Organization → General 里完成验证，通过后约 15 分钟生效"
	case status == 403 && (strings.Contains(low, "country") || strings.Contains(low, "region") || strings.Contains(low, "territory")):
		ae.Message = "服务器所在地区不在 OpenAI 的服务范围内，请把服务部署到支持的地区，或改用中转接口地址"
	case status == 403:
		ae.Message = fmt.Sprintf("这个密钥没有权限使用「%s」", model)
	case status == 404:
		ae.Message = fmt.Sprintf("找不到模型「%s」，请检查模型名，或确认账号有权使用", model)
	case status == 429 && (strings.Contains(low, "insufficient_quota") || strings.Contains(low, "billing")):
		ae.Message = "OpenAI 账户余额或额度不足，请到 OpenAI 后台充值"
	case status == 429:
		ae.Message, ae.Retryable = "请求太频繁，自动重试后仍被限流，请稍后再试或调低并发", true
	case status >= 500:
		ae.Message, ae.Retryable = "OpenAI 服务暂时不可用，已自动重试，请稍后再试", true
	default:
		ae.Message = "请求被拒绝"
	}
	return ae
}

func fetchImage(ctx context.Context, hc *http.Client, url string, headers map[string]string) (File, error) {
	st, data, err := doHTTP(ctx, hc, http.MethodGet, url, headers, nil)
	if err != nil || st >= 300 {
		return File{}, fmt.Errorf("下载生成的图片失败：%v（HTTP %d）", err, st)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return File{}, fmt.Errorf("下载到的内容不是图片（%s）", mime)
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}[mime]
	if ext == "" {
		ext = "png"
	}
	return File{Data: data, Mime: mime, Ext: ext}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
