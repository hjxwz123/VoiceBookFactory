package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// ElevenLabs Sound Effects (text to sound effects), official API:
//
//	POST {base}/v1/sound-generation?output_format=mp3_44100_128
//	xi-api-key: <key>
//	{"text": "...", "model_id": "eleven_text_to_sound_v3", "duration_seconds": 4, "prompt_influence": 0.3, "loop": false}
//
// The response body is the audio. Durations run 0.5–30 s; without one the model picks a length that
// suits the prompt. Accounts in a data-residency region use their own base URL (api.eu.residency…).

const (
	ElevenSFXDefaultBase  = "https://api.elevenlabs.io"
	ElevenSFXDefaultModel = "eleven_text_to_sound_v3"
)

type elevenSFX struct {
	base, key, model, format string
	influence                *float64
	client                   *http.Client
}

func newElevenSFX(a Account) *elevenSFX {
	e := &elevenSFX{base: strings.TrimRight(strings.TrimSpace(a.BaseURL), "/"), key: strings.TrimSpace(a.APIKey), model: strings.TrimSpace(a.Model), format: "mp3_44100_128", client: &http.Client{Timeout: 120 * time.Second}}
	if e.base == "" {
		e.base = ElevenSFXDefaultBase
	}
	if e.model == "" {
		e.model = ElevenSFXDefaultModel
	}
	if v, ok := a.Config["outputFormat"].(string); ok && strings.HasPrefix(v, "mp3_") {
		e.format = v
	}
	switch v := a.Config["promptInfluence"].(type) {
	case float64:
		if v >= 0 && v <= 1 {
			e.influence = &v
		}
	}
	return e
}

func (e *elevenSFX) Name() string { return "elevenlabs_sfx" }

// elevenError is how the API reports failures: {"detail": {"status": "...", "message": "..."}}, or for
// invalid parameters {"detail": [{"loc": [...], "msg": "...", "type": "..."}]}.
type elevenError struct {
	Detail json.RawMessage `json:"detail"`
}

func elevenFailure(status int, body []byte) error {
	var e elevenError
	_ = json.Unmarshal(body, &e)
	var d struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	var list []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(e.Detail, &d) != nil && json.Unmarshal(e.Detail, &list) == nil && len(list) > 0 {
		d.Message = list[0].Msg
		if len(list[0].Loc) > 0 {
			d.Message = fmt.Sprint(list[0].Loc[len(list[0].Loc)-1]) + ": " + d.Message
		}
	}
	raw := strings.TrimSpace(d.Message)
	if raw == "" {
		raw = strings.TrimSpace(string(body))
		if len(raw) > 300 {
			raw = raw[:300]
		}
	}
	plain := ""
	switch {
	case d.Status == "invalid_api_key" || (status == 401 && d.Status == ""):
		plain = "ElevenLabs 密钥无效，请检查后重新填写"
	case d.Status == "missing_permissions":
		plain = "这个 ElevenLabs 密钥没有开通「Sound Effects」权限，请在 ElevenLabs 后台给密钥勾选后重试"
	case d.Status == "quota_exceeded" || status == 402:
		plain = "ElevenLabs 账户的额度用完了，请充值或升级套餐后重试"
	case d.Status == "too_many_concurrent_requests" || d.Status == "system_busy" || status == 429:
		plain = "ElevenLabs 当前繁忙或超出了并发上限，请稍后重试"
	case status == 422 || status == 400:
		plain = "ElevenLabs 拒绝了这次请求的参数"
	case status >= 500:
		plain = "ElevenLabs 服务暂时出错，请稍后重试"
	default:
		plain = "ElevenLabs 音效生成失败"
	}
	return fmt.Errorf("%s（HTTP %d %s：%s）", plain, status, d.Status, raw)
}

func (e *elevenSFX) GenerateSFX(ctx context.Context, r SFXRequest) (*File, error) {
	if e.key == "" {
		return nil, errors.New("没有填写 ElevenLabs 密钥")
	}
	prompt := strings.TrimSpace(r.Prompt)
	if prompt == "" {
		return nil, errors.New("请描述想要的音效")
	}
	body := map[string]any{"text": prompt, "model_id": e.model}
	if r.DurationMs > 0 {
		body["duration_seconds"] = math.Round(math.Min(math.Max(float64(r.DurationMs)/1000, 0.5), 30)*10) / 10
	}
	if e.influence != nil {
		body["prompt_influence"] = *e.influence
	}
	if r.Loop {
		body["loop"] = true
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.base+"/v1/sound-generation?output_format="+e.format, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("xi-api-key", e.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")
	res, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 ElevenLabs 失败：%w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 ElevenLabs 返回的音频失败：%w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, elevenFailure(res.StatusCode, data)
	}
	if ct := res.Header.Get("Content-Type"); len(data) < 512 || strings.Contains(ct, "json") || strings.HasPrefix(ct, "text/") {
		return nil, fmt.Errorf("ElevenLabs 没有返回音频（%s，%d 字节）", ct, len(data))
	}
	meta := map[string]any{"provider": "elevenlabs_sfx", "model": e.model, "prompt": prompt}
	if v, ok := body["duration_seconds"]; ok {
		meta["durationSeconds"] = v
	}
	if r.Loop {
		meta["loop"] = true
	}
	if id := res.Header.Get("request-id"); id != "" {
		meta["requestId"] = id
	}
	return &File{Data: data, Mime: "audio/mpeg", Ext: "mp3", Meta: meta}, nil
}

// Check verifies the key without generating anything (a generation is billed per second).
func (e *elevenSFX) Check(ctx context.Context) (string, error) {
	if e.key == "" {
		return "", errors.New("没有填写 ElevenLabs 密钥")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/v1/user/subscription", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("xi-api-key", e.key)
	res, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接 ElevenLabs 失败：%w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		err := elevenFailure(res.StatusCode, data)
		if strings.Contains(string(data), "missing_permissions") {
			// a key limited to sound effects cannot read the account, which proves it is a valid key
			return "密钥有效（这个密钥没有读取账户信息的权限，所以看不到剩余额度）", nil
		}
		return "", err
	}
	var sub struct {
		Tier           string `json:"tier"`
		CharacterCount int64  `json:"character_count"`
		CharacterLimit int64  `json:"character_limit"`
		Status         string `json:"status"`
	}
	_ = json.Unmarshal(data, &sub)
	msg := "ElevenLabs 连接正常"
	if sub.Tier != "" {
		msg += "，套餐 " + sub.Tier
	}
	if sub.CharacterLimit > 0 {
		msg += fmt.Sprintf("，本期额度已用 %d / %d", sub.CharacterCount, sub.CharacterLimit)
	}
	return msg, nil
}
