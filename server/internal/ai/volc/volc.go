// Package volc holds what the Volcengine speech APIs (豆包语音: recording-file recognition, audio generation)
// share: authentication headers, request ids and readable error messages.
package volc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBase = "https://openspeech.bytedance.com"

// Auth sets the credentials. Keys from the new console use a single X-Api-Key header; the old console issues an
// App ID plus an Access Token instead (appID set), sent as X-Api-App-Id / X-Api-App-Key with X-Api-Access-Key.
func Auth(h http.Header, key, appID string) {
	key, appID = strings.TrimSpace(key), strings.TrimSpace(appID)
	if appID != "" {
		h.Set("X-Api-App-Id", appID)
		h.Set("X-Api-App-Key", appID)
		h.Set("X-Api-Access-Key", key)
		return
	}
	h.Set("X-Api-Key", key)
}

// AppID reads the optional old-console App ID from an account's configuration.
func AppID(cfg map[string]any) string {
	s, _ := cfg["appId"].(string)
	return strings.TrimSpace(s)
}

// Base trims a configured base URL, falling back to the official address.
func Base(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	if u == "" {
		return DefaultBase
	}
	return u
}

// RequestID returns a random UUID (v4).
func RequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Local reports whether a base URL points at this machine or a private network (a self-hosted gateway or a
// test double), which can also reach this server's own private address.
func Local(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return PrivateHost(u.Hostname())
}

// PrivateHost reports loopback, private and link-local hosts, and names that only resolve inside a network.
func PrivateHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

// Body is the JSON error body of the speech APIs: {"code": 45001001, "message": "..."}; some gateways nest it
// under "header" or "error".
type Body struct {
	Code    json.Number `json:"code"`
	Message string      `json:"message"`
	Header  struct {
		Code    json.Number `json:"code"`
		Message string      `json:"message"`
	} `json:"header"`
	Error any `json:"error"`
}

// Parse extracts the code and message from an error response body.
func Parse(body []byte) (code, msg string) {
	var b Body
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.UseNumber()
	if d.Decode(&b) == nil {
		code, msg = b.Code.String(), b.Message
		if code == "" {
			code, msg = b.Header.Code.String(), b.Header.Message
		}
		if msg == "" {
			switch e := b.Error.(type) {
			case string:
				msg = e
			case map[string]any:
				msg = fmt.Sprint(e["message"])
			}
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if r := []rune(msg); len(r) > 200 {
			msg = string(r[:200])
		}
	}
	return code, msg
}

// Plain turns an HTTP status and a platform code into a sentence a beginner can act on. what names the service
// ("音频生成", "录音文件识别").
func Plain(what string, status int, code, msg string) string {
	low := strings.ToLower(msg)
	switch {
	case status == 401 || status == 403 || code == "45000010" || strings.Contains(low, "invalid api key") || strings.Contains(low, "unauthorized") || strings.Contains(low, "auth"):
		if strings.Contains(low, "resource") && (strings.Contains(low, "not granted") || strings.Contains(low, "not activated") || strings.Contains(low, "permission")) {
			return "这个火山引擎账号还没有开通" + what + "服务，请在火山引擎控制台开通后重试"
		}
		return "火山引擎密钥无效或没有" + what + "权限，请检查 API Key（旧版控制台账号还需要在账号配置里填写 App ID）"
	case strings.Contains(low, "quota") && strings.Contains(low, "concurrency"):
		return "火山引擎" + what + "超出并发上限，请稍后重试，或在控制台增购并发"
	case status == 429 || strings.Contains(low, "quota") || strings.Contains(low, "rate limit") || strings.Contains(low, "too many"):
		return "火山引擎" + what + "调用太频繁或额度不足，请稍后重试，或在控制台查看用量"
	case code == "45001115" || strings.Contains(low, "speaker") && strings.Contains(low, "not found"):
		return "火山引擎找不到这个音色，请确认音色 ID 正确并已在控制台开通"
	case code == "45001116":
		return "描述文字太长了，火山引擎音频生成最多支持 3000 个字符"
	case code == "45001117" || code == "45001001" && strings.Contains(low, "audio reference"):
		return "参考音频太长或太大：每段最长 30 秒、不超过 10 MB"
	case code == "45001125" || strings.Contains(low, "safetyfilter") || strings.Contains(low, "audit"):
		return "内容没有通过火山引擎的安全审核，请调整文字或参考音频后重试"
	case code == "45001104" || code == "55001311" || strings.Contains(low, "voiceprint"):
		return "参考音频没有通过火山引擎的声纹检测（可能与受保护的声音相似），请换一段录音"
	case code == "45001131" || code == "45001132" || code == "45000006":
		return "火山引擎下载不到音频，请确认本站地址（VB_PUBLIC_URL）可以从公网访问"
	case strings.Contains(low, "durationoutofrange"):
		return "要生成的音频超过 2 分钟，请缩短文字或描述"
	case code == "40402002" || strings.Contains(low, "readabletext"):
		return "文字里可以朗读的内容太少，请检查输入"
	case code == "45000002" || code == "20000003":
		return "音频里没有识别到人声"
	case code == "45000151":
		return "火山引擎读不了这个音频格式，请转换成 wav 或 mp3 后重试"
	case code == "45000131":
		return "半小时内提交给火山引擎识别的音频总时长超过了上限，请稍后再试"
	case code == "55001010" || code == "55001011":
		return "火山引擎识别任务处理或排队时间过长，已经停止，请稍后重试"
	case code == "45000130" || code == "45000132":
		return "音频太大或太长：单个文件不超过 512 MB、5 小时"
	case code == "55000031" || strings.HasPrefix(code, "55") || status >= 500:
		return "火山引擎" + what + "服务繁忙或暂时出错，请稍后重试"
	case status == 400 || strings.HasPrefix(code, "45") || strings.HasPrefix(code, "40"):
		return "火山引擎拒绝了这次" + what + "请求的参数"
	}
	return "火山引擎" + what + "失败"
}

// Error is a failed call with the platform's own wording kept for support.
type Error struct {
	Status           int
	Code, Msg, Plain string
	LogID            string
}

func (e *Error) Error() string {
	detail := strings.TrimSpace(e.Msg)
	if e.Code != "" {
		detail = e.Code + " " + detail
	}
	if e.LogID != "" {
		detail += "，logid " + e.LogID
	}
	return fmt.Sprintf("%s（HTTP %d：%s）", e.Plain, e.Status, detail)
}

// Retryable reports busy and server-side failures worth trying again.
func (e *Error) Retryable() bool {
	return e.Status == 429 || e.Status >= 500 || e.Code == "55000031" || strings.HasPrefix(e.Code, "55")
}

// Fail builds an Error from a response.
func Fail(what string, status int, body []byte, logID string) *Error {
	code, msg := Parse(body)
	return &Error{Status: status, Code: code, Msg: msg, Plain: Plain(what, status, code, msg), LogID: logID}
}

// ---------------- audio generation ----------------

type volcCreateReply struct {
	Code             json.Number `json:"code"`
	Message          string      `json:"message"`
	Audio            string      `json:"audio"`
	Data             string      `json:"data"`
	Duration         float64     `json:"duration"`
	OriginalDuration float64     `json:"original_duration"`
	URL              string      `json:"url"`
}

// AudioClient sends one generation request and returns the decoded audio (shared with sound effects).
type AudioClient struct {
	Base, Key, AppID, Model string
	Client                  *http.Client
	RetryWait               time.Duration
}

// Create posts the request, retrying busy answers, and returns the audio bytes and billed seconds.
func (c AudioClient) Create(ctx context.Context, body map[string]any) ([]byte, float64, error) {
	if c.Key == "" {
		return nil, 0, errors.New("火山引擎账号缺少 API Key")
	}
	body["model"] = c.Model
	raw, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			case <-time.After(time.Duration(attempt) * c.RetryWait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/v3/tts/create", bytes.NewReader(raw))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Api-Request-Id", RequestID())
		Auth(req.Header, c.Key, c.AppID)
		resp, err := c.Client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("连接火山引擎音频生成失败：%w", err)
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		logID := resp.Header.Get("X-Tt-Logid")
		if rerr != nil {
			lastErr = fmt.Errorf("读取火山引擎返回的音频失败：%w", rerr)
			continue
		}
		var out volcCreateReply
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		_ = d.Decode(&out)
		code := out.Code.String()
		if resp.StatusCode != http.StatusOK || (code != "" && code != "0" && code != "20000000") {
			status := resp.StatusCode
			if status == http.StatusOK {
				status = 400
				if strings.HasPrefix(code, "5") {
					status = 500
				}
			}
			e := Fail("音频生成", status, data, logID)
			if e.Retryable() {
				lastErr = e
				continue
			}
			return nil, 0, e
		}
		b64 := out.Audio
		if b64 == "" {
			b64 = out.Data
		}
		var audioBytes []byte
		if b64 != "" {
			if audioBytes, err = base64.StdEncoding.DecodeString(b64); err != nil {
				return nil, 0, fmt.Errorf("火山引擎返回的音频无法解码：%v", err)
			}
		} else if out.URL != "" {
			if audioBytes, err = c.download(ctx, out.URL); err != nil {
				return nil, 0, err
			}
		} else {
			return nil, 0, fmt.Errorf("火山引擎没有返回音频（logid %s）", logID)
		}
		secs := out.OriginalDuration
		if secs == 0 {
			secs = out.Duration
		}
		return audioBytes, secs, nil
	}
	return nil, 0, lastErr
}

func (c AudioClient) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载火山引擎生成的音频失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载火山引擎生成的音频失败：HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// Check sends a request with an empty prompt: the platform authenticates the key before it validates the
// prompt, so a parameter error proves the key works and nothing is generated or billed.
func (c AudioClient) Check(ctx context.Context) (string, error) {
	_, _, err := c.Create(ctx, map[string]any{"text_prompt": ""})
	var e *Error
	if err == nil {
		return "火山引擎音频生成连接正常，模型 " + c.Model, nil
	}
	if errors.As(err, &e) && e.Status == 400 && !strings.Contains(e.Plain, "密钥") && !strings.Contains(e.Plain, "开通") {
		return "火山引擎音频生成密钥有效，模型 " + c.Model + "（检测时没有生成音频，不产生费用）", nil
	}
	return "", err
}
