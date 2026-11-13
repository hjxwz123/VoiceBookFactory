package core

import (
	"strings"
	"testing"
)

func TestPublicText(t *testing.T) {
	for raw, want := range map[string]string{
		// written for users: unchanged
		"积分不足：本次需要 3 积分，当前可用 0 积分":  "积分不足：本次需要 3 积分，当前可用 0 积分",
		"有 2 句配音失败，请进入制作台查看原因后重试":   "有 2 句配音失败，请进入制作台查看原因后重试",
		"「Chapter One」还没有画本，请先生成画本": "「Chapter One」还没有画本，请先生成画本",
		"该 API Key 没有 write 权限":     "该 API Key 没有 write 权限",
		"只有所有者或管理员可以管理 API Key":     "只有所有者或管理员可以管理 API Key",
		// technical detail cut at the first technical part, finished in plain words
		"permanent failure: 「第1章」画本生成失败：大模型调用失败（DeepSeek）：openai: 401 invalid api key":                                "「第1章」画本生成失败：大模型调用失败：服务暂时不可用，请稍后重试；如果一直失败，请联系客服",
		"大模型调用失败（DeepSeek）：Post \"https://api.example.com/v1/chat/completions\": context deadline exceeded":           "大模型调用失败：服务响应超时，请稍后重试",
		"qwen: 400 {\"code\":\"DataInspectionFailed\",\"message\":\"Input data may contain inappropriate content.\"}": "内容没有通过服务方的安全审核，请修改相关文字后重试",
		"ffmpeg 处理失败：exit status 1 Invalid data found when processing input":                                          "音频处理失败，请稍后重试",
		"minimax: 1002 rate limit exceeded": "当前使用的人比较多，请稍后重试",
		// stored before 0.1.3, with an account name in Chinese
		"大模型调用失败（临时故障测试）：LLM 接口错误 (HTTP 0)：Post \"http://127.0.0.1:1/v1/chat/completions\": dial tcp 127.0.0.1:1: connect: connection refused": "大模型调用失败：服务暂时连接不上，请稍后重试",
		// marked at the source: whatever follows ｜详情： is detail, account names included
		"大模型调用失败｜详情：账号「临时故障测试」：LLM 接口错误 (HTTP 0)：Post \"http://127.0.0.1:1/v1/chat/completions\": dial tcp 127.0.0.1:1: connect: connection refused": "大模型调用失败：服务暂时连接不上，请稍后重试",
		"permanent failure: 「第2章」画本生成失败：大模型调用失败｜详情：账号「主力」：LLM 接口错误 (HTTP 429)：rate limited":                                                          "「第2章」画本生成失败：大模型调用失败：当前使用的人比较多，请稍后重试",
		"配音失败｜详情：账号「千问」：内容含有敏感词":                "配音失败：内容没有通过服务方的安全审核，请修改相关文字后重试",
		"permanent failure: 积分不足：开始制作至少需要 9 积分": "积分不足：开始制作至少需要 9 积分",
		"": "",
	} {
		if got := PublicText(raw); got != want {
			t.Errorf("%q\n got  %q\n want %q", raw, got, want)
		}
	}
	// the account name never survives
	if got := PublicText("大模型调用失败（DeepSeek）：EOF"); strings.Contains(got, "DeepSeek") {
		t.Fatalf("account name leaked: %q", got)
	}
}
