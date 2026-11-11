// Package ai holds the provider catalog shown in the admin console.
package ai

type Preset struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`               // llm | tts | asr | image | music | sfx
	Protocol    string   `json:"protocol,omitempty"` // llm wire protocol
	Provider    string   `json:"provider"`           // adapter id stored on the account
	BaseURL     string   `json:"baseUrl"`
	Models      []string `json:"models"`
	Implemented bool     `json:"implemented"`
	NeedsKey    bool     `json:"needsKey"`
	Docs        string   `json:"docs"`
	Note        string   `json:"note"`
	RPM         int      `json:"rpm,omitempty"`         // suggested requests-per-minute limit
	Concurrency int      `json:"concurrency,omitempty"` // suggested parallel requests
}

// Presets are starting points; every field stays editable in the console.
var Presets = []Preset{
	// ---- LLM: three wire protocols ----
	{Key: "anthropic", Name: "Anthropic Claude", Kind: "llm", Protocol: "anthropic", Provider: "anthropic", BaseURL: "https://api.anthropic.com", Models: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}, Implemented: true, NeedsKey: true, Docs: "https://platform.claude.com/docs", Note: "Claude Messages 协议（官方 SDK），支持结构化 JSON 输出与 effort 调节"},
	{Key: "openai_responses", Name: "OpenAI（Responses 协议）", Kind: "llm", Protocol: "openai_responses", Provider: "openai_responses", BaseURL: "https://api.openai.com/v1", Models: []string{}, Implemented: true, NeedsKey: true, Docs: "https://platform.openai.com/docs/api-reference/responses", Note: "POST /responses，支持 json_schema 结构化输出与 reasoning effort"},
	{Key: "openai_chat", Name: "OpenAI（Chat Completions 协议）", Kind: "llm", Protocol: "openai_chat", Provider: "openai_chat", BaseURL: "https://api.openai.com/v1", Models: []string{}, Implemented: true, NeedsKey: true, Docs: "https://platform.openai.com/docs/api-reference/chat", Note: "POST /chat/completions，兼容绝大多数国产大模型"},
	{Key: "deepseek", Name: "DeepSeek", Kind: "llm", Protocol: "openai_chat", Provider: "openai_chat", BaseURL: "https://api.deepseek.com", Models: []string{"deepseek-chat", "deepseek-reasoner"}, Implemented: true, NeedsKey: true, Docs: "https://api-docs.deepseek.com", Note: "Chat Completions 兼容协议，JSON 模式使用 json_object"},
	{Key: "qwen", Name: "通义千问（阿里云百炼）", Kind: "llm", Protocol: "openai_chat", Provider: "openai_chat", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Models: []string{"qwen-plus", "qwen-max", "qwen-turbo"}, Implemented: true, NeedsKey: true, Docs: "https://help.aliyun.com/zh/model-studio", Note: "百炼兼容模式"},
	{Key: "custom_llm", Name: "自定义 / 私有化部署", Kind: "llm", Protocol: "openai_chat", Provider: "openai_chat", BaseURL: "http://localhost:11434/v1", Models: []string{}, Implemented: true, NeedsKey: false, Note: "vLLM、Ollama 等任何兼容 OpenAI 协议的服务"},

	// ---- TTS ----
	{Key: "placeholder_tts", Name: "内置演示引擎", Kind: "tts", Provider: "placeholder", Implemented: true, Note: "生成与文本节奏一致的示意音，用于联调全流程；接入真实平台后替换"},
	{Key: "qwen_tts", Name: "千问 AI 平台（Qwen-Audio-TTS / CosyVoice）", Kind: "tts", Provider: "qwen", BaseURL: "https://maas.qianwenaiapi.com", Models: []string{"qwen-audio-3.1-tts-flash", "qwen-audio-3.1-tts-plus", "qwen-audio-3.0-tts-flash", "qwen-audio-3.0-tts-plus", "cosyvoice-v3-flash", "cosyvoice-v3-plus", "cosyvoice-v2"}, Implemented: true, NeedsKey: true, Docs: "https://platform.qianwenai.com/docs/developer-guides/speech/tts", Note: "非实时语音合成（HTTP）。内置 1500+ 个系统音色与基础音色，音色与模型一一对应，合成时自动使用音色所属模型；支持情感/语气标签、自然语言指令、SSML 读音与多音字热修复。阿里云百炼（dashscope.aliyuncs.com）账号同样适用。支持声音复刻与声音设计（Qwen-Audio / CosyVoice，音色绑定所选模型、长期有效，在账号设置里选择模型）"},
	{Key: "minimax_tts", Name: "MiniMax 语音（Speech 2.8 / 2.6 / 02）", Kind: "tts", Provider: "minimax", BaseURL: "https://api.minimaxi.com", Models: []string{"speech-2.8-hd", "speech-2.8-turbo", "speech-2.6-hd", "speech-2.6-turbo", "speech-02-hd", "speech-02-turbo"}, Implemented: true, NeedsKey: true, Docs: "https://platform.minimaxi.com/docs/guides/speech-t2a-async", RPM: 20, Concurrency: 3, Note: "同步语音合成（T2A v2）与异步长文本合成。内置 327 个系统音色，每个音色在音色库中分 HD（精品）和 Turbo（标准）两档；支持情绪、语气词标签（2.8 系列）、多音字读音与 40 种语言。按官方限速自动排队（充值用户 20 次/分钟，免费用户请改为 10）。支持快速复刻（需在 MiniMax 完成实名认证；复刻音色 7 天内未使用会被删除，首次使用时 MiniMax 收取一次音色费用）。声音设计为预留接口"},
	{Key: "volcengine_tts", Name: "火山引擎（豆包语音）", Kind: "tts", Provider: "volcengine", BaseURL: "https://openspeech.bytedance.com", NeedsKey: true, Note: "预留接口"},
	{Key: "elevenlabs_tts", Name: "ElevenLabs", Kind: "tts", Provider: "elevenlabs", BaseURL: "https://api.elevenlabs.io", NeedsKey: true, Note: "预留接口：合成、音色设计、专业克隆、变声、多人对话"},
	{Key: "openai_tts", Name: "OpenAI TTS", Kind: "tts", Provider: "openai", BaseURL: "https://api.openai.com/v1", NeedsKey: true, Note: "预留接口"},
	{Key: "azure_tts", Name: "Azure 语音", Kind: "tts", Provider: "azure", NeedsKey: true, Note: "预留接口"},
	{Key: "selfhost_tts", Name: "自部署模型（CosyVoice / IndexTTS 等）", Kind: "tts", Provider: "selfhost", BaseURL: "http://localhost:9880", Note: "预留接口：私有化部署"},

	// ---- ASR ----
	{Key: "openai_asr", Name: "OpenAI 兼容语音识别（Whisper）", Kind: "asr", Provider: "openai_compat", BaseURL: "https://api.openai.com/v1", Models: []string{"whisper-1"}, Implemented: true, NeedsKey: true, Note: "智能对轨与智能审听使用，需支持逐词时间戳；兼容 Groq 与自建 Whisper 服务"},
	{Key: "aliyun_asr", Name: "阿里云语音识别", Kind: "asr", Provider: "aliyun", NeedsKey: true, Note: "预留接口"},
	{Key: "volcengine_asr", Name: "火山引擎语音识别", Kind: "asr", Provider: "volcengine", NeedsKey: true, Note: "预留接口"},

	// ---- Image (covers) ----
	{Key: "openai_image", Name: "OpenAI GPT Image", Kind: "image", Provider: "openai_image", BaseURL: "https://api.openai.com/v1", Models: []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2", "gpt-image-1.5", "gpt-image-1-mini"}, Implemented: true, NeedsKey: true, Docs: "https://developers.openai.com/api/docs/guides/image-generation", RPM: 20, Concurrency: 2, Note: "Image API（POST /images/generations），可直接在封面上写书名。Flare 速度快、价格低，适合日常；Sunburst 细节更好。OpenAI 要求先完成组织验证（Organization Verification）才能调用 GPT Image。兼容 OpenAI 图片接口的中转服务也可以用这个预设"},
	{Key: "gemini_image", Name: "Google Nano Banana（Gemini 图像）", Kind: "image", Provider: "gemini_image", BaseURL: "https://generativelanguage.googleapis.com", Models: []string{"gemini-nano-banana-2.1", "gemini-3-pro-image", "gemini-3.1-flash-image", "gemini-3.1-flash-lite-image"}, Implemented: true, NeedsKey: true, Docs: "https://ai.google.dev/gemini-api/docs/image-generation", RPM: 20, Concurrency: 2, Note: "Gemini Interactions API。Nano Banana 2.1 是官方推荐的主力模型；Pro 适合精细的封面排版；2 Lite 最快最便宜。密钥在 Google AI Studio 创建，所有图片带不可见的 SynthID 水印。服务器需部署在 Gemini 支持的地区"},
	{Key: "placeholder_image", Name: "内置抽象封面生成器", Kind: "image", Provider: "placeholder", Implemented: true, Note: "根据描述生成抽象渐变艺术图，不写文字，用于没有接入图片模型时占位"},
	{Key: "tongyi_wanxiang", Name: "通义万相", Kind: "image", Provider: "tongyi_wanxiang", NeedsKey: true, Note: "预留接口"},
	{Key: "doubao_seedream", Name: "豆包 Seedream", Kind: "image", Provider: "doubao_seedream", NeedsKey: true, Note: "预留接口"},
	{Key: "stability", Name: "Stability AI", Kind: "image", Provider: "stability", NeedsKey: true, Note: "预留接口"},

	// ---- Music (BGM) ----
	{Key: "placeholder_music", Name: "内置配乐生成器", Kind: "music", Provider: "placeholder", Implemented: true, Note: "按情绪程序化生成可循环配乐"},
	{Key: "elevenlabs_music", Name: "ElevenLabs Music", Kind: "music", Provider: "elevenlabs_music", NeedsKey: true, Note: "预留接口"},
	{Key: "suno", Name: "Suno（New API / One API 中转）", Kind: "music", Provider: "suno", Models: []string{"chirp-v4", "chirp-v3-5", "chirp-auk", "chirp-bluejay", "chirp-crow"}, Implemented: true, NeedsKey: true, Docs: "https://doc.newapi.pro/en/api/suno-music", RPM: 10, Concurrency: 2, Note: "Suno 官方目前没有公开的 API（官方开发者平台仍在合作伙伴阶段），这里对接的是 New API / One API 等中转服务的 Suno 渠道（/suno/submit/music 与 /suno/fetch 任务协议）。接口地址填写中转服务的根地址（不要带 /v1），API Key 填中转服务的令牌。每次生成得到两首，可选纯音乐或带人声的歌曲，也可以填写歌词和风格；时长由 Suno 决定，通常 2–4 分钟，一般 1–3 分钟出结果。模型名以中转服务支持的为准：chirp-v4 为 v4，chirp-auk 为 v4.5，chirp-bluejay 为 v4.5+，chirp-crow 为 v5。中转服务并非 Suno 官方渠道，生成内容的商用授权以 Suno 及服务方的条款为准，请运营方自行确认"},
	{Key: "minimax_music", Name: "MiniMax 音乐", Kind: "music", Provider: "minimax_music", NeedsKey: true, Note: "预留接口"},

	// ---- SFX ----
	{Key: "placeholder_sfx", Name: "内置音效生成器", Kind: "sfx", Provider: "placeholder", Implemented: true, Note: "按关键词程序化合成雨声、脚步、敲门等音效"},
	{Key: "elevenlabs_sfx", Name: "ElevenLabs Sound Effects", Kind: "sfx", Provider: "elevenlabs_sfx", NeedsKey: true, Note: "预留接口"},
	{Key: "stability_audio", Name: "Stable Audio", Kind: "sfx", Provider: "stability_audio", NeedsKey: true, Note: "预留接口"},
}
