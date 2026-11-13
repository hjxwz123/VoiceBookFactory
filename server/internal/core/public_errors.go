package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"voicebook/internal/model"
)

// Users never see the technical side of a failure: provider responses, HTTP status codes, account
// names, file paths or ffmpeg output. The full message stays in the database (jobs, lines, renders)
// and in the error log, where administrators read it; what users get is cut at the first technical
// part and finished with a plain explanation. Messages written for users pass through unchanged.

const GenericFailure = "服务暂时出了点问题，请稍后重试；如果一直失败，请联系客服"

// DetailSep separates what users may read from the technical detail after it. Calls to outside
// services (language models, voices, generators, ffmpeg) put it between their plain description and
// the raw error: "大模型调用失败｜详情：账号「…」：…". The audio package writes the same literal.
const DetailSep = "｜详情："

// withDetail describes a failure in plain words, keeping the raw error after DetailSep.
func withDetail(public string, err error) error { return fmt.Errorf("%s%s%w", public, DetailSep, err) }

var technicalMarkers = []string{
	"http://", "https://", "{\"", "exit status", "status code", "dial tcp", "eof", "sqlstate", "sql:", "ffmpeg",
	"x509", "tls", "context deadline", "context canceled", "no such file", "connection refused", "connection reset",
	"broken pipe", "invalid character", "unexpected", "panic", "goroutine", "nil pointer", "openai", "anthropic",
	"dashscope", "minimax", "json", "stack", "deadline exceeded", "http",
	// provider and model names: which vendor runs a feature is the platform's business
	"qwen", "deepseek", "gemini", "claude", "gpt", "suno", "cosyvoice", "volc", "doubao", "azure", "aliyun",
}

var quoted = regexp.MustCompile(`「[^」]*」|《[^》]*》|“[^”]*”`)

// legacyAccount matches how messages stored before 0.1.3 named the model account: 大模型调用失败（账号名）.
var legacyAccount = regexp.MustCompile(`大模型调用失败（[^）]*）`)

// looksTechnical reports whether one part of an error message is technical detail rather than an
// explanation written for people. Names quoted in 「」《》“” do not count.
func looksTechnical(part string) bool {
	p := quoted.ReplaceAllString(part, "")
	low := strings.ToLower(p)
	for _, m := range technicalMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	ascii, cjk := 0, 0
	for _, r := range p {
		switch {
		case r < unicode.MaxASCII && unicode.IsLetter(r):
			ascii++
		case unicode.Is(unicode.Han, r):
			cjk++
		}
	}
	// mostly English: a provider's or library's message (a product term such as "API Key" is not enough)
	return ascii >= 12 && ascii*2 > cjk
}

// failureKind explains a technical failure in plain words, from its full message.
func failureKind(msg string) string {
	low := strings.ToLower(msg)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(low, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("timeout", "timed out", "deadline exceeded", "超时"):
		return "服务响应超时，请稍后重试"
	case has("429", "rate limit", "ratelimit", "too many requests", "throttl", "限流", "qps"):
		return "当前使用的人比较多，请稍后重试"
	case has("data_inspection", "inspection", "sensitive", "moderation", "inappropriate", "safety", "content_filter", "content policy", "审核", "违规", "敏感"):
		return "内容没有通过服务方的安全审核，请修改相关文字后重试"
	case has("401", "403", "unauthorized", "forbidden", "invalid api key", "api key", "quota", "insufficient", "arrear", "balance", "欠费", "余额"):
		return "服务暂时不可用，请稍后重试；如果一直失败，请联系客服"
	case has("ffmpeg", "音频解码", "decode", "audio"):
		return "音频处理失败，请稍后重试"
	case has("connection refused", "no such host", "dial tcp", "eof", "connection reset", "network is unreachable"):
		return "服务暂时连接不上，请稍后重试"
	}
	return GenericFailure
}

// splitParts cuts a message at its colons (an error wrapped in context reads "context：cause").
func splitParts(msg string) []string {
	msg = strings.ReplaceAll(msg, ": ", "：")
	var parts []string
	for _, p := range strings.Split(msg, "：") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// PublicText turns a stored error message into what users may see.
func PublicText(msg string) string {
	msg = strings.TrimSpace(msg)
	for _, prefix := range []string{"permanent failure: ", "permanent failure："} {
		for strings.HasPrefix(msg, prefix) {
			msg = strings.TrimSpace(strings.TrimPrefix(msg, prefix))
		}
	}
	if msg == "" {
		return ""
	}
	if i := strings.Index(msg, DetailSep); i >= 0 { // marked at the source: everything after is detail
		head := strings.TrimRight(strings.TrimSpace(msg[:i]), "：")
		if head == "" {
			return failureKind(msg[i:])
		}
		return PublicText(head) + "：" + failureKind(msg[i:])
	}
	msg = legacyAccount.ReplaceAllString(msg, "大模型调用失败")
	parts := splitParts(msg)
	for i, p := range parts {
		if looksTechnical(p) {
			keep := append(append([]string{}, parts[:i]...), failureKind(msg))
			return strings.Join(keep, "：")
		}
	}
	return msg
}

// LogError keeps a failure users were shown only in plain words, so administrators can look it up by
// the reference given to the user. It returns that reference.
func (a *App) LogError(userID, method, path string, status int, public, detail string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	ref := strings.ToUpper(hex.EncodeToString(b))
	a.DB.Create(&model.ErrorLog{Ref: ref, UserID: userID, Method: method, Path: truncateStr(path, 250), Status: status, Message: truncateStr(public, 480), Detail: truncateStr(detail, 3900)})
	return ref
}
