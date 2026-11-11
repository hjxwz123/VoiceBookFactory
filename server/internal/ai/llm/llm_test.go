package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var schema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{"reply": map[string]any{"type": "string"}},
	"required":             []string{"reply"},
	"additionalProperties": false,
}

func TestOpenAIChat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		io.WriteString(w, `{"model":"deepseek-chat","choices":[{"message":{"content":"`+"```json\\n{\\\"reply\\\":\\\"ok\\\"}\\n```"+`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`)
	}))
	defer srv.Close()
	c, err := New(Config{Protocol: "openai_chat", BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Reply string }
	resp, err := ChatJSON(context.Background(), c, Request{System: "sys", Messages: []Message{{Role: "user", Content: "hi"}}, Schema: schema}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Reply != "ok" || resp.InputTokens != 12 {
		t.Fatalf("bad parse %+v %+v", out, resp)
	}
	rf := got["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("default json mode should be json_object, got %v", rf)
	}
	msgs := got["messages"].([]any)
	if !strings.Contains(msgs[0].(map[string]any)["content"].(string), "JSON Schema") {
		t.Fatal("schema instruction missing from system prompt")
	}
}

func TestOpenAIResponses(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		io.WriteString(w, `{"model":"gpt-x","status":"completed","output":[{"type":"reasoning","content":[]},{"type":"message","content":[{"type":"output_text","text":"{\"reply\":\"hello\"}"}]}],"usage":{"input_tokens":7,"output_tokens":3}}`)
	}))
	defer srv.Close()
	c, _ := New(Config{Protocol: "openai_responses", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "gpt-x", Effort: "low"})
	var out struct{ Reply string }
	if _, err := ChatJSON(context.Background(), c, Request{System: "be brief", Messages: []Message{{Role: "user", Content: "hi"}}, Schema: schema, SchemaName: "r"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Reply != "hello" {
		t.Fatalf("bad parse %+v", out)
	}
	format := got["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "r" {
		t.Fatalf("bad text.format %v", format)
	}
	if got["instructions"] != "be brief" || got["reasoning"].(map[string]any)["effort"] != "low" {
		t.Fatalf("bad instructions/reasoning %v %v", got["instructions"], got["reasoning"])
	}
}

func TestAnthropic(t *testing.T) {
	var got map[string]any
	var headers http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		headers = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"{\"reply\":\"你好\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":20,"output_tokens":6}}`)
	}))
	defer srv.Close()
	c, _ := New(Config{Protocol: "anthropic", BaseURL: srv.URL, APIKey: "sk-ant", Model: DefaultClaudeModel, Effort: "medium", Fallbacks: true})
	var out struct{ Reply string }
	resp, err := ChatJSON(context.Background(), c, Request{System: "sys", Messages: []Message{{Role: "user", Content: "hi"}}, Schema: schema}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Reply != "你好" || resp.OutputTokens != 6 || resp.Model != "claude-opus-5-5" {
		t.Fatalf("bad parse %+v %+v", out, resp)
	}
	auth := headers.Get("X-Api-Key") == "sk-ant" || headers.Get("Authorization") == "Bearer sk-ant"
	if !auth || headers.Get("Anthropic-Version") == "" {
		t.Fatalf("missing auth/version headers: %v", headers)
	}
	oc := got["output_config"].(map[string]any)
	if oc["effort"] != "medium" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("bad output_config %v", oc)
	}
	if _, ok := got["temperature"]; ok {
		t.Fatal("temperature must not be sent to Claude")
	}
	if _, ok := got["fallbacks"]; ok {
		t.Fatal("fallbacks must only be sent to the first-party API")
	}
	sys := got["system"].([]any)[0].(map[string]any)
	if sys["text"] != "sys" {
		t.Fatalf("bad system %v", got["system"])
	}
}

func TestRefusalAndRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"","refusal":"no"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	c, _ := New(Config{Protocol: "openai_chat", BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "拒绝") || calls != 2 {
		t.Fatalf("expected retry then refusal, calls=%d err=%v", calls, err)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		"```json\n{\"a\":1}\n```":          `{"a":1}`,
		"好的，结果如下：{\"a\":{\"b\":\"}\"}} 完成": `{"a":{"b":"}"}}`,
		"[1,2]": "[1,2]",
	}
	for in, want := range cases {
		if got := ExtractJSON(in); got != want {
			t.Fatalf("ExtractJSON(%q)=%q want %q", in, got, want)
		}
	}
}
