package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"voicebook/internal/core"
)

func TestUsersSeePlainErrorsAdminsSeeDetail(t *testing.T) {
	logged := map[string]string{}
	errorLog = func(r *http.Request, status int, public, detail string) string {
		logged["R1"] = detail
		return "R1"
	}
	t.Cleanup(func() { errorLog = nil })
	raw := "大模型调用失败（DeepSeek）：openai: 401 invalid api key"
	call := func(path string, fn func(*http.Request) (any, error)) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h(fn)(w, httptest.NewRequest("GET", path, nil))
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v %s", path, err, w.Body.String())
		}
		return out
	}
	// a failed request: plain words and a reference for users, the full error logged
	body := call("/api/v1/x", func(*http.Request) (any, error) { return nil, fmt.Errorf("画本生成失败：%w", errors.New(raw)) })
	msg := body["error"].(map[string]any)["message"].(string)
	if strings.Contains(msg, "DeepSeek") || strings.Contains(msg, "401") || !strings.Contains(msg, "编号 R1") || !strings.HasPrefix(msg, "画本生成失败：") {
		t.Fatalf("user saw %q", msg)
	}
	if !strings.Contains(logged["R1"], "invalid api key") {
		t.Fatalf("detail not logged: %v", logged)
	}
	// messages written for users pass as they are, without a reference
	body = call("/api/v1/x", func(*http.Request) (any, error) {
		return nil, core.PaymentRequired("积分不足：本次需要 3 积分", nil)
	})
	if got := body["error"].(map[string]any)["message"]; got != "积分不足：本次需要 3 积分" {
		t.Fatalf("user saw %q", got)
	}
	// stored errors inside data are rewritten for users, numbers kept exactly
	data := func(*http.Request) (any, error) {
		return map[string]any{"items": []any{map[string]any{"error": raw, "credits": int64(9007199254740993)}}, "lastError": "ffmpeg 处理失败：exit status 1"}, nil
	}
	body = call("/api/v1/jobs", data)
	item := body["items"].([]any)[0].(map[string]any)
	if strings.Contains(item["error"].(string), "DeepSeek") || strings.Contains(body["lastError"].(string), "exit status") {
		t.Fatalf("user saw %v", body)
	}
	w := httptest.NewRecorder()
	h(data)(w, httptest.NewRequest("GET", "/api/v1/jobs", nil))
	if !strings.Contains(w.Body.String(), "9007199254740993") {
		t.Fatalf("number changed: %s", w.Body.String())
	}
	// administrators get everything
	body = call("/api/admin/v1/jobs", data)
	if body["items"].([]any)[0].(map[string]any)["error"] != raw {
		t.Fatalf("admin saw %v", body)
	}
}
