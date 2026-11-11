package gen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Checker is implemented by providers that can verify the key and model without generating
// (and paying for) an image. The admin "测试" button prefers it.
type Checker interface {
	Check(ctx context.Context) (string, error)
}

// APIError is a failed call to an image API, carrying a message an operator or user can act on.
type APIError struct {
	Vendor    string // OpenAI | Gemini
	Status    int
	Code      string
	Message   string // friendly Chinese explanation
	Raw       string // upstream message, kept for the operator
	Retryable bool
}

func (e *APIError) Error() string {
	s := e.Vendor + " 图片生成失败：" + e.Message
	if e.Raw != "" && !strings.Contains(e.Message, e.Raw) {
		s += "（" + e.Raw + "）"
	}
	return s
}

func cfgStr(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func cfgInt(m map[string]any, k string, def int) int {
	switch v := m[k].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func httpClient(cfg map[string]any) *http.Client {
	// image models think before they draw; a 2K picture can take well over a minute
	return &http.Client{Timeout: time.Duration(cfgInt(cfg, "timeoutSec", 300)) * time.Second}
}

// doHTTP sends one request and returns the status and body. Network failures come back as status 0.
func doHTTP(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	// a 4K PNG in base64 is ~30 MB; leave headroom for several images in one response
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	return resp.StatusCode, data, err
}

// retryUnit scales the retry backoff; tests shrink it.
var retryUnit = time.Second

// withRetry retries transient failures (network, rate limit, 5xx) twice with backoff.
// Image APIs do not bill failed requests, so a retry never pays twice.
func withRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			wait := time.Duration(math.Pow(3, float64(attempt))) * retryUnit // 3s, 9s
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(wait):
			}
		}
		v, err := fn()
		if err == nil {
			return v, nil
		}
		last = err
		if ae, ok := err.(*APIError); !ok || !ae.Retryable {
			return zero, err
		}
	}
	return zero, last
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// imageFile decodes base64 image data and works out its type from the bytes.
func imageFile(b64, mime string) (File, error) {
	if i := strings.Index(b64, ";base64,"); i >= 0 { // tolerate data: URLs from relays
		b64 = b64[i+8:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(b64); err != nil {
			return File{}, fmt.Errorf("图片数据解码失败：%v", err)
		}
	}
	if sniff := http.DetectContentType(data); strings.HasPrefix(sniff, "image/") {
		mime = sniff
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}[mime]
	if ext == "" {
		mime, ext = "image/png", "png"
	}
	return File{Data: data, Mime: mime, Ext: ext}, nil
}

// aspectOf reduces a requested size to the closest supported aspect-ratio label.
func aspectOf(r ImageRequest) string {
	if r.Aspect != "" {
		return r.Aspect
	}
	if r.Width <= 0 || r.Height <= 0 {
		return "1:1"
	}
	want := float64(r.Width) / float64(r.Height)
	best, diff := "1:1", math.MaxFloat64
	for _, a := range []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"} {
		var w, h float64
		fmt.Sscanf(a, "%f:%f", &w, &h)
		if d := math.Abs(math.Log(want) - math.Log(w/h)); d < diff {
			best, diff = a, d
		}
	}
	return best
}
