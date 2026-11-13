package core

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"

	"voicebook/internal/model"
)

//go:embed docs/open-api.md
var openAPIGuide string

// OpenAPIDocSlug identifies the Open API guide among the help articles (/openapi/v1/docs opens it).
const OpenAPIDocSlug = "open-api"

// managedDocs are help articles kept in step with the built-in text by a content hash stored in
// Version: while an article still matches the hash it was written with, a newer built-in text
// replaces it; once an administrator edits it, it is left alone.
var managedDocs = []helpDoc{
	{Category: "开放平台", Title: "开放接口（Open API）文档", Summary: "用程序导入书稿、发起一键成书、下载成品，以及 Webhook 通知", Body: openAPIGuide, Slug: OpenAPIDocSlug},
}

func docHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:32]
}

func (a *App) syncManagedDocs() {
	for i, d := range managedDocs {
		var cur model.Article
		if a.DB.Where("kind = ? AND slug = ?", "help", d.Slug).Limit(1).Find(&cur).Error != nil || cur.ID == "" {
			a.DB.Create(&model.Article{Kind: "help", Slug: d.Slug, Category: d.Category, Title: d.Title, Summary: d.Summary, Body: d.Body, Version: docHash(d.Body), SortOrder: 100 + i, Published: true})
			continue
		}
		if cur.Version == docHash(cur.Body) && cur.Body != d.Body {
			a.DB.Model(&cur).Updates(map[string]any{"title": d.Title, "summary": d.Summary, "body": d.Body, "category": d.Category, "version": docHash(d.Body)})
		}
	}
}

// helpDoc is one built-in help-center article. Replaces lists earlier built-in titles it supersedes:
// such an article is rewritten in place, but only while its body is still the untouched built-in
// text (an administrator's edits are never overwritten).
type helpDoc struct {
	Category, Title, Summary, Body string
	Slug                           string // managed docs only
	Featured                       bool
	Replaces                       []string
}

// helpDocs follows the professional audiobook workflow: prepare → voice → proof-listen → pickups → deliver.
var helpDocs = []helpDoc{
	{Category: "入门", Title: "做出第一本有声书", Summary: "从导入书稿到导出成品的完整流程", Featured: true, Replaces: []string{"三步做出第一本有声书"}, Body: `## 1. 新建图书，导入书稿
在「图书」点击 **新建图书**，上传 txt / docx / epub / pdf 或直接粘贴正文，系统会自动识别章节，确认后创建。书稿须是你的原创、已取得有声改编授权，或已进入公有领域。

## 2. 分析角色并选角
进入图书的「角色与选角」，点击 **分析角色**，AI 会找出书中会说话的角色并写好人物小传；再点 **一键选角**，为每个角色挑选声音。不满意可以点 **更换**，用这个角色在书中的真实台词试听。

## 3. 设置读音
在「读音」页让 AI 找出人名、地名和多音字，逐个确认读音后采用。先定好读音再配音，能少很多返工。

## 4. 生成画本和配音
在「章节」页点击 **生成画本**（AI 标注每句是谁说的、什么情绪，不改动原文），再点 **生成配音**。打开任意章节进入 **制作台**，可以逐句试听、修改、重新生成。

## 5. 审听与返工
在制作台点 **连续播放**，对照文字边听边按 **M** 标记有问题的句子；右侧「审听」里一键重新生成待返工的句子，复听没问题后关闭。整章没问题就点 **审听通过**。

## 6. 合成与交付
点击 **合成** 生成整章成品（自动加停顿、背景音乐、片头片尾并统一响度）。在「导出与发布」页先看 **交付前检查**，全部通过后导出 MP3 / M4B，或发布到平台。

> 想省事？用左侧的 **一键成书**：上传书稿、选好风格，剩下的步骤全部自动完成，之后仍然可以进制作台精修任何一句。`},
	{Category: "核心概念", Title: "什么是画本？", Summary: "画本是有声书的演播脚本", Body: `画本把章节拆成一句句的**片段**，并为每句标注：

- **说话人**：旁白还是某个角色
- **情绪与表演标签**：平静、开心、悲伤，叹气、轻笑……
- **读音**：多音字、生僻字的正确读法
- **停顿**：与上一句之间留白多久

AI 生成画本时不会改动原文，只做标注。在制作台里：

- 点说话人名字可以更换；选中句子后按数字键 **1–9**，直接换成说话人列表里的第 N 个
- 双击或按 **Enter** 修改文字，**Ctrl+Enter** 在下方添加一句，拖动左侧把手调整顺序
- 右键查看更多操作（拆分、合并、复制、删除），**Ctrl+Z** 撤销
- 底部的「全部快捷键」可以随时查看所有快捷键`},
	{Category: "核心概念", Title: "选角与音色", Summary: "为角色挑选最合适的声音", Body: `每个角色绑定一个声音。在「角色与选角」页点 **一键选角**，AI 会根据人物小传推荐；点角色卡片上的 **更换** 打开音色库，可以按性别、年龄、价位筛选，并用这个角色在书中的真实台词试听。

同一个声音可能有多个**模型版本**（例如 Qwen-Audio 3.1 和 CosyVoice v3）。新版本、Plus 版音质和表现力更好，价格也更高；在音色卡片上点模型名就能切换版本。

找不到合适的？在「音色库 → AI 设计音色」里用一句话描述你想要的声音，或者 **克隆声音**（克隆本人需要朗读验证句，克隆他人需要声音主人的授权书）。`},
	{Category: "核心概念", Title: "读音表：人名和多音字不再读错", Summary: "配音前先定好读音，少返工", Body: `有声书最常见的返工原因是读错字：人名、地名、多音字、生僻字。

1. 打开图书的 **读音** 页，点 **AI 找出易读错的词**
2. 逐个确认：每个字的读音从下拉框里选，不用自己输入声调；英文缩写也可以设置成别的读法
3. 点 **采用**（或 **全部采用**）

读音对本书所有章节生效。修改读音后，已经配好音的相关句子会标为「有改动」，在「生成配音 → 有改动的」里一键重新生成即可。

「工作室设置 → 读音词典」里的词条对工作室的所有图书生效，适合系列书里反复出现的名字。`},
	{Category: "真人制作", Title: "真人录音与智能对轨", Summary: "AI 与真人混合制作", Body: `把角色的配音方式设为 **真人** 并指派给配音演员（CV），CV 会在「我的台词」中看到自己的台词，可以逐句在线录音，也可以打开提词器模式。

如果 CV 习惯整章连续录制，上传整段录音后使用 **智能对轨**，系统会自动把录音切开并对应到每一句台词。没有指派真人的角色继续用 AI 配音，两者可以混在同一章里。`},
	{Category: "质量", Title: "审听与返工", Summary: "边听边标记，只重做有问题的句子", Replaces: []string{"智能审听"}, Body: `## 人工审听
打开章节制作台，点 **连续播放**（或在右侧「审听」里点「从头开始审听」），正在播放的句子会高亮并自动跟随。听到问题按 **M**，这句会被标为「待返工」，播放不会中断；标错了可以在提示里撤回。

## 智能审听
「审听」面板里的 **智能审听** 会检查音量、底噪、爆音和语速；平台配置了语音识别时，还会逐字比对原文，标出 **漏读、多读、错读**。

## 返工与复听
在右侧「片段」里可以选问题类型、补充说明。「审听」面板汇总本章所有待返工的句子，点 **重新生成** 一次处理完；重新生成后变为「待复核」，复听确认后关闭。整章没问题就点 **审听通过**。

## 工单
团队协作时，问题会以工单形式指派给对应的人。图书的「审听与工单」页能看到每章的审听状态和全部工单，也可以 **导出工单表**（Excel、WPS 可直接打开）交给主播或后期。`},
	{Category: "交付", Title: "交付前检查与导出", Summary: "MP3 / M4B / 分轨 / 字幕", Replaces: []string{"导出与发布"}, Body: `图书「导出与发布」页顶部是 **交付前检查**，按专业交付标准逐项核对：画本、逐句配音、成品、技术指标（响度、峰值、底噪）、审听、工单、书名作者、简介、封面、版权授权和 AI 标识。没通过的项目会写明原因，并提供「去处理」入口。

检查通过后点 **新建导出**：可以导出 MP3、WAV、M4A，或整本书的 M4B（带章节标记）；还能附带字幕、分角色分轨、演播稿和台词本。绑定发布渠道后也可以直接发布。

发行前请确认书稿、声音、配乐和音效都已获得授权。`},
	{Category: "版权", Title: "版权须知", Summary: "书稿、配乐、音效和声音的版权由你自行确认", Body: `本平台只提供制作工具，**不对任何内容的版权作保证**，因版权产生的纠纷和损失由使用者自行承担。

- **书稿**：须为你的原创、已取得有声改编和发行授权，或已进入公有领域。
- **平台配乐、音效**：标注的来源和许可（如 CC0、公有领域）来自原网站，仅供参考，平台不保证准确。商业发行或签约作品建议使用自行购买正版授权的素材。
- **你上传的素材**：须确认你拥有合法使用权。
- **声音克隆**：只能克隆本人的声音，或已取得声音主人书面授权的声音。
- **AI 生成内容**：发布到平台时按平台要求标注 AI 生成。

详细条款见 [《版权与素材使用声明》](/legal/copyright) 和 [《AI 生成内容与声音使用规范》](/legal/ai)。`},
	{Category: "账户", Title: "积分如何计算？", Summary: "按字数与次数计量", Body: `AI 功能按积分计量：画本与配音按字数计算，设计音色、克隆、音效按次数计算，对轨与审听按音频时长计算。

- 把鼠标移到顶栏的积分上，可以看到当前积分大约够做多少字、多少小时的有声书
- 「生成配音」和「一键成书」在开始前都会显示预计消耗
- 相同内容重复生成会**命中缓存，不扣积分**
- 每句话有 **2 次免费重新生成**（内容和音色不变时）
- 每天有一定次数的免费试听`},
	{Category: "管理员", Title: "配置 AI 模型", Summary: "大模型、语音、图像账号怎么接", Body: `管理员在 **管理后台 → 模型与供应商** 中添加 AI 账号，每类服务一个页签。

**大语言模型**（角色分析、画本、选角）支持三种协议：

1. **OpenAI Chat Completions**：兼容 DeepSeek、通义千问、Kimi、GLM、Ollama 等
2. **OpenAI Responses**
3. **Anthropic Claude Messages**

**语音合成**：已接入千问和 MiniMax。

**图像生成**（AI 封面、角色头像）：已接入 OpenAI GPT Image 和 Google Nano Banana。没有接入时由内置生成器画抽象图，不能在封面上写书名。

- **OpenAI GPT Image**：默认模型 gpt-image-2.6-flare。使用前要先在 OpenAI 后台完成组织验证（Organization Verification）。
- **Google Nano Banana**：默认模型 gemini-nano-banana-2.1，密钥在 Google AI Studio 创建。服务器所在地区要在 Gemini API 的支持范围内，否则请填中转地址。

选好预设、填写密钥后点 **测试**。图像账号的测试只核对密钥和模型，不会生成图片，也不产生费用。每张图按「积分定价」里的 AI 封面价格向用户扣积分。`},
}

// helpSuperseded keeps built-in bodies that later releases replaced, so untouched copies still upgrade.
var helpSuperseded = map[string][]string{
	"配置 AI 模型": {`管理员在 **管理后台 → 模型与供应商** 中添加大模型账号。支持三种协议：

1. **OpenAI Chat Completions**：兼容 DeepSeek、通义千问、Kimi、GLM、Ollama 等
2. **OpenAI Responses**
3. **Anthropic Claude Messages**

填写接口地址、密钥与模型名后点击 **测试连接** 即可。语音合成在同一页的「语音合成」里添加账号。`},
}

// syncHelpDocs adds new built-in help articles and refreshes superseded ones that nobody edited.
func (a *App) syncHelpDocs() {
	defer a.syncManagedDocs()
	builtin := map[string]string{} // title -> untouched built-in body from earlier releases
	for _, h := range helpV1 {
		builtin[h.Title] = h.Body
	}
	for i, d := range helpDocs {
		var cur model.Article
		if a.DB.Where("kind = ? AND title = ?", "help", d.Title).First(&cur).Error == nil {
			untouched := cur.Body == builtin[d.Title] && builtin[d.Title] != ""
			for _, b := range helpSuperseded[d.Title] {
				untouched = untouched || cur.Body == b
			}
			if untouched && cur.Body != d.Body {
				a.DB.Model(&cur).Updates(map[string]any{"body": d.Body, "summary": d.Summary, "category": d.Category, "sort_order": i + 1})
			}
			continue
		}
		replaced := false
		for _, t := range d.Replaces {
			var prev model.Article
			if a.DB.Where("kind = ? AND title = ?", "help", t).First(&prev).Error == nil {
				if prev.Body == builtin[t] {
					a.DB.Model(&prev).Updates(map[string]any{"title": d.Title, "summary": d.Summary, "body": d.Body, "category": d.Category, "featured": d.Featured, "sort_order": i + 1})
				}
				replaced = true // an edited predecessor is left alone, and no duplicate is added
				break
			}
		}
		if !replaced {
			a.DB.Create(&model.Article{Kind: "help", Category: d.Category, Title: d.Title, Summary: d.Summary, Body: d.Body, Featured: d.Featured, SortOrder: i + 1, Published: true})
		}
	}
}
