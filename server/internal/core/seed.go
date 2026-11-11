package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"voicebook/internal/audio"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
	"voicebook/internal/secure"
)

// Seed creates baseline data (idempotent). Heavy audio generation runs in the background.
func (a *App) Seed() error {
	a.seedPlans()
	a.seedPlanDefaults()
	a.seedPricing()
	a.seedWorkflows()
	a.seedPrompts()
	a.seedProviders()
	a.seedArticles()
	a.syncHelpDocs()
	a.seedLegal()
	a.seedLexicon()
	a.seedEval()
	a.seedSensitive()
	if err := a.seedAdmin(); err != nil {
		return err
	}
	a.migrateMemberships() // needs the admin account

	a.seeding.Store(true)
	go func() {
		defer a.seeding.Store(false)
		ctx := context.Background()
		// with several instances on PostgreSQL one seeds at a time; the others then find everything present
		dbx.Exclusive(ctx, a.DB, dbx.LockSeed+1, func() error {
			a.seedVoices(ctx)
			a.seedQwenCatalog()
			a.seedMiniMaxCatalog()
			a.seedAssets(ctx)
			a.syncLibraryAssets()
			a.seedTemplates()
			return nil
		})
	}()
	return nil
}

func (a *App) seedAdmin() error {
	var n int64
	a.DB.Model(&model.User{}).Where(dbx.Like(a.DB, "admin_roles"), "%"+model.AdminSuper+"%").Count(&n)
	if n > 0 {
		return nil
	}
	email := NormalizeEmail(a.Cfg.AdminEmail)
	var u model.User
	if a.DB.First(&u, "email = ?", email).Error == nil {
		return a.DB.Model(&u).Update("admin_roles", model.Strings(model.AdminSuper)).Error
	}
	hash, err := secure.HashPassword(a.Cfg.AdminPassword)
	if err != nil {
		return err
	}
	u = model.User{Email: &email, PasswordHash: hash, Nickname: "平台管理员", Status: model.UserActive, EmailVerified: true, AdminRoles: model.Strings(model.AdminSuper)}
	u.Preferences.V = model.UserPreferences{Theme: "system", Locale: "zh-CN", Notify: map[string]bool{}, Onboarded: true}
	u.CV.V = model.CVProfile{Tags: []string{}, Works: []string{}, DemoIDs: []string{}}
	if err := a.DB.Create(&u).Error; err != nil {
		return err
	}
	st, err := a.createStudioTx(a.DB, &u, "官方演示工作室", "personal", a.defaultPlan(a.DB))
	if err != nil {
		return err
	}
	a.DB.Model(&u).Update("personal_studio", st.ID)
	a.DB.Model(st).Update("features", model.Strings(model.FeatAlignment, model.FeatReview, model.FeatProduction, model.FeatPayroll, model.FeatBYOK, model.FeatOpenAPI, model.FeatBatch, model.FeatProClone, model.FeatDistribute))
	a.Grant(st.ID, 1_000_000, "grant", nil, "平台管理员初始积分", "")
	slog.Info("created super admin", "email", email)
	return nil
}

func (a *App) seedPlans() {
	var n int64
	a.DB.Model(&model.Plan{}).Count(&n)
	if n > 0 {
		return
	}
	plans := []model.Plan{
		{Code: "free", Name: "免费版", Tagline: "体验 AI 多播有声书", PriceMonthly: 0, MonthlyCredits: 10_000, Seats: 1, StorageGB: 2, MaxBooks: 3, CloneVoices: 1, Features: model.Strings(), Listed: true, SortOrder: 1},
		{Code: "personal", Name: "个人版", Tagline: "个人创作者与配音演员", PriceMonthly: 6900, MonthlyCredits: 150_000, Seats: 1, StorageGB: 30, MaxBooks: 0, CloneVoices: 5, HighQuality: true, Commercial: true, Features: model.Strings(model.FeatAlignment, model.FeatReview), Listed: true, SortOrder: 2},
		{Code: "team", Name: "团队版", Tagline: "有声工作室与剧组", PriceMonthly: 29900, MonthlyCredits: 800_000, Seats: 10, StorageGB: 200, MaxBooks: 0, CloneVoices: 30, HighQuality: true, Commercial: true, Features: model.Strings(model.FeatAlignment, model.FeatReview, model.FeatProduction, model.FeatPayroll, model.FeatBYOK, model.FeatProClone, model.FeatDistribute), Listed: true, SortOrder: 3},
		{Code: "enterprise", Name: "企业版", Tagline: "版权方与平台批量有声化", PriceMonthly: 0, MonthlyCredits: 5_000_000, Seats: 100, StorageGB: 2000, MaxBooks: 0, CloneVoices: 200, HighQuality: true, Commercial: true, Features: model.Strings(model.FeatAlignment, model.FeatReview, model.FeatProduction, model.FeatPayroll, model.FeatBYOK, model.FeatOpenAPI, model.FeatBatch, model.FeatPrivate, model.FeatProClone, model.FeatDistribute), Listed: true, SortOrder: 4},
	}
	for i := range plans {
		a.DB.Create(&plans[i])
	}
}

func (a *App) seedPricing() {
	var n int64
	a.DB.Model(&model.PricingRule{}).Count(&n)
	if n > 0 {
		return
	}
	rules := []model.PricingRule{
		{Feature: FeatScript, Tier: "*", Unit: "per_1k_chars", Credits: 2, Name: "智能画本"},
		{Feature: FeatCharacter, Tier: "*", Unit: "per_1k_chars", Credits: 1, Name: "角色分析"},
		{Feature: FeatLexicon, Tier: "*", Unit: "per_1k_chars", Credits: 1, Name: "发音词条提取"},
		{Feature: FeatCasting, Tier: "*", Unit: "per_1k_chars", Credits: 1, Name: "AI 选角"},
		{Feature: FeatModerate, Tier: "*", Unit: "per_1k_chars", Credits: 0, Name: "内容审核"},
		{Feature: FeatTTS, Tier: model.TierEconomy, Unit: "per_1k_chars", Credits: 5, Name: "AI 配音 · 经济音色"},
		{Feature: FeatTTS, Tier: model.TierStandard, Unit: "per_1k_chars", Credits: 10, Name: "AI 配音 · 标准音色"},
		{Feature: FeatTTS, Tier: model.TierPremium, Unit: "per_1k_chars", Credits: 20, Name: "AI 配音 · 精品音色"},
		{Feature: FeatTTS, Tier: "*", Unit: "per_1k_chars", Credits: 10, Name: "AI 配音"},
		{Feature: FeatQuick, Tier: "*", Unit: "per_1k_chars", Credits: 10, Name: "快速配音"},
		{Feature: FeatPreview, Tier: "*", Unit: "per_call", Credits: 1, FreeDaily: 30, Name: "试听"},
		{Feature: FeatDesign, Tier: "*", Unit: "per_call", Credits: 50, Name: "AI 设计音色"},
		{Feature: FeatClone, Tier: "quick", Unit: "per_call", Credits: 200, Name: "快速克隆"},
		{Feature: FeatClone, Tier: "pro", Unit: "per_call", Credits: 2000, Name: "专业克隆"},
		{Feature: FeatSFX, Tier: "*", Unit: "per_call", Credits: 20, Name: "AI 音效"},
		{Feature: FeatMusic, Tier: "*", Unit: "per_call", Credits: 50, Name: "AI 配乐"},
		{Feature: FeatImage, Tier: "*", Unit: "per_call", Credits: 20, Name: "AI 封面"},
		{Feature: FeatAlign, Tier: "*", Unit: "per_minute", Credits: 10, Name: "智能对轨"},
		{Feature: FeatReview, Tier: "*", Unit: "per_minute", Credits: 5, Name: "智能审听"},
	}
	for i := range rules {
		a.DB.Create(&rules[i])
	}
}

func (a *App) seedWorkflows() {
	var n int64
	a.DB.Model(&model.WorkflowTemplate{}).Where("scope = ?", model.ScopeSystem).Count(&n)
	if n > 0 {
		return
	}
	for _, w := range DefaultWorkflows {
		w.Scope = model.ScopeSystem
		a.DB.Create(&w)
	}
}

func (a *App) seedPrompts() {
	for _, p := range DefaultPrompts {
		var n int64
		a.DB.Model(&model.PromptTemplate{}).Where("key = ?", p.Key).Count(&n)
		if n == 0 {
			a.DB.Create(&model.PromptTemplate{Key: p.Key, Name: p.Name, Version: 1, Content: p.Content, Active: true, Rollout: 100, Note: "系统默认"})
		}
	}
}

func (a *App) seedProviders() {
	ensure := func(kind, provider, name string) {
		var n int64
		a.DB.Model(&model.ProviderAccount{}).Where("kind = ? AND provider = ? AND scope = ?", kind, provider, "platform").Count(&n)
		if n == 0 {
			acc := model.ProviderAccount{Kind: kind, Provider: provider, Scope: "platform", Name: name, Enabled: true, Priority: 1, Concurrency: 4, RPM: 600, Config: model.EmptyMap(), HealthStatus: "ok"}
			acc.Models.V = []model.ProviderModel{}
			a.DB.Create(&acc)
		}
	}
	ensure(model.KindTTS, "placeholder", "内置演示引擎")
	ensure(model.KindImage, "placeholder", "内置抽象封面生成器")
	ensure(model.KindMusic, "placeholder", "内置配乐生成器")
	ensure(model.KindSFX, "placeholder", "内置音效生成器")
	// bootstrap the LLM / ASR accounts from environment variables
	if a.Cfg.LLMAPIKey != "" || strings.Contains(a.Cfg.LLMBaseURL, "localhost") {
		var n int64
		a.DB.Model(&model.ProviderAccount{}).Where("kind = ?", model.KindLLM).Count(&n)
		if n == 0 {
			enc, _ := secure.Encrypt(a.Cfg.SecretKey, a.Cfg.LLMAPIKey)
			acc := model.ProviderAccount{Kind: model.KindLLM, Provider: a.Cfg.LLMProtocol, Scope: "platform", Name: "默认大模型（环境变量）", BaseURL: a.Cfg.LLMBaseURL, Model: a.Cfg.LLMModel,
				CredentialsEnc: enc, KeyLast4: secure.Last4(a.Cfg.LLMAPIKey), Enabled: true, Priority: 10, Concurrency: 4, RPM: 120, Config: model.EmptyMap()}
			acc.Models.V = []model.ProviderModel{}
			a.DB.Create(&acc)
		}
	}
	if a.Cfg.ASRAPIKey != "" {
		var n int64
		a.DB.Model(&model.ProviderAccount{}).Where("kind = ?", model.KindASR).Count(&n)
		if n == 0 {
			enc, _ := secure.Encrypt(a.Cfg.SecretKey, a.Cfg.ASRAPIKey)
			acc := model.ProviderAccount{Kind: model.KindASR, Provider: "openai_compat", Scope: "platform", Name: "默认语音识别（环境变量）", BaseURL: a.Cfg.ASRBaseURL, Model: a.Cfg.ASRModel,
				CredentialsEnc: enc, KeyLast4: secure.Last4(a.Cfg.ASRAPIKey), Enabled: true, Priority: 10, Concurrency: 2, Config: model.EmptyMap()}
			acc.Models.V = []model.ProviderModel{}
			a.DB.Create(&acc)
		}
	}
}

type voiceSeed struct {
	name, gender, age, tier, desc string
	tags                          []string
	crowd, rec                    bool
	lang                          string
}

var officialVoices = []voiceSeed{
	{"沉稳旁白·男", "male", "adult", model.TierPremium, "字正腔圆、沉稳耐听的男声旁白，适合长篇叙事", []string{"旁白", "播音腔", "沉稳"}, false, true, ""},
	{"知性旁白·女", "female", "adult", model.TierPremium, "温润知性的女声旁白，娓娓道来", []string{"旁白", "知性", "温润"}, false, true, ""},
	{"清朗少年", "male", "teen", model.TierStandard, "清亮有朝气的少年音", []string{"少年感", "清亮", "活泼"}, false, true, ""},
	{"温婉少女", "female", "teen", model.TierStandard, "温柔甜美的少女音", []string{"甜美", "温柔", "少女"}, false, true, ""},
	{"阳光青年", "male", "young", model.TierStandard, "阳光爽朗的青年男声", []string{"阳光", "爽朗"}, true, false, ""},
	{"元气少女", "female", "young", model.TierStandard, "活泼元气的年轻女声", []string{"活泼", "元气"}, true, false, ""},
	{"磁性男声", "male", "young", model.TierPremium, "低沉磁性、带压迫感的男声，适合霸总与男主", []string{"磁性", "低沉", "霸总"}, false, true, ""},
	{"清冷御姐", "female", "young", model.TierPremium, "清冷干练的御姐音", []string{"清冷", "御姐", "冷峻"}, false, true, ""},
	{"儒雅公子", "male", "young", model.TierStandard, "温和儒雅的古风公子", []string{"儒雅", "温和", "古风"}, false, false, ""},
	{"灵动仙子", "female", "young", model.TierStandard, "空灵飘逸的古风女声", []string{"空灵", "古风", "温柔"}, false, false, ""},
	{"沧桑大叔", "male", "middle", model.TierStandard, "略带沙哑的中年男声，饱经风霜", []string{"沙哑", "沧桑", "沉稳"}, true, false, ""},
	{"干练职场女", "female", "adult", model.TierStandard, "利落干练的职场女性", []string{"干练", "知性"}, true, false, ""},
	{"慈祥老爷爷", "male", "senior", model.TierStandard, "慈祥浑厚的老年男声", []string{"慈祥", "浑厚"}, true, false, ""},
	{"和蔼老奶奶", "female", "senior", model.TierStandard, "和蔼温暖的老年女声", []string{"和蔼", "温暖"}, true, false, ""},
	{"调皮男童", "male", "child", model.TierEconomy, "调皮可爱的男孩", []string{"童声", "调皮"}, true, false, ""},
	{"软萌女童", "female", "child", model.TierEconomy, "软萌的小女孩", []string{"童声", "软萌"}, true, false, ""},
	{"冷面杀手", "male", "adult", model.TierPremium, "冷峻低沉、话少的反派男声", []string{"冷峻", "低沉", "反派"}, false, false, ""},
	{"腹黑反派", "male", "middle", model.TierStandard, "阴郁腹黑的中年反派", []string{"阴郁", "反派", "磁性"}, false, false, ""},
	{"市井大妈", "female", "middle", model.TierEconomy, "爽朗热心的市井大妈", []string{"爽朗", "市井"}, true, false, ""},
	{"憨厚壮汉", "male", "adult", model.TierEconomy, "憨厚粗犷的壮汉", []string{"浑厚", "憨厚"}, true, false, ""},
	{"新闻播报·男", "male", "adult", model.TierStandard, "标准新闻播报腔", []string{"播音腔", "字正腔圆"}, false, false, ""},
	{"温暖电台·女", "female", "young", model.TierStandard, "治愈系电台女声", []string{"电台", "温暖", "治愈"}, false, false, ""},
	{"经济旁白·男", "male", "adult", model.TierEconomy, "高性价比的男声旁白，适合大批量有声化", []string{"旁白", "经济"}, false, false, ""},
	{"经济旁白·女", "female", "adult", model.TierEconomy, "高性价比的女声旁白，适合大批量有声化", []string{"旁白", "经济"}, false, false, ""},
	{"English Narrator", "male", "adult", model.TierStandard, "Warm English male narrator", []string{"英文", "旁白"}, false, false, "en-US"},
}

func (a *App) seedVoices(ctx context.Context) {
	var acc model.ProviderAccount
	if a.DB.First(&acc, "kind = ? AND provider = ?", model.KindTTS, "placeholder").Error != nil {
		return
	}
	for i, s := range officialVoices {
		var n int64
		a.DB.Model(&model.Voice{}).Where("scope = ? AND name = ?", model.ScopeSystem, s.name).Count(&n)
		if n > 0 {
			continue
		}
		lang := firstNonEmpty(s.lang, "zh-CN")
		v := model.Voice{Scope: model.ScopeSystem, Name: s.name, Origin: model.VoicePreset, Provider: "placeholder", ProviderAccountID: acc.ID, ProviderVoiceID: fmt.Sprintf("ph-%02d", i+1),
			Language: lang, Gender: s.gender, AgeGroup: s.age, Tags: model.Strings(s.tags...), Description: s.desc, PriceTier: s.tier, Status: model.VoiceReady, Listed: true,
			Recommended: s.rec, SortOrder: i, CrowdPool: s.crowd, Emotions: model.Strings("neutral", "happy", "sad", "angry", "fearful", "surprised", "gentle", "whisper", "shout"), SampleText: "你好，很高兴认识你。这是我的声音，希望能为你的故事增添色彩。"}
		if lang == "en-US" {
			v.SampleText = "Hello there. Let me tell you a story that begins on a quiet winter night."
		}
		v.Defaults.V = model.VoiceDefaults{Speed: 1}
		if err := a.DB.Create(&v).Error; err != nil {
			continue
		}
		p := audio.Babble(v.SampleText, audio.VoiceStyle{Gender: v.Gender, AgeGroup: v.AgeGroup, Seed: audio.Seed(v.ID, v.ProviderVoiceID), Speed: 1})
		var buf bytes.Buffer
		audio.EncodeWAV16(&buf, p)
		if as, err := a.SaveAssetBytes(ctx, AssetInput{Scope: model.ScopeSystem, Kind: model.AssetPreview, Name: v.Name + " 试听", Ext: "wav", Origin: "system"}, buf.Bytes()); err == nil {
			a.DB.Model(&v).Update("sample_asset_id", as.ID)
		}
	}
}

type assetSeed struct {
	name, kind, prompt string
	dur                int
	tags               []string
}

var officialSfx = []assetSeed{
	{"雨声（中雨）", model.AssetAmbience, "雨声", 10000, []string{"雨", "天气", "环境"}},
	{"雷声", model.AssetSFX, "雷声", 6000, []string{"雷", "天气"}},
	{"呼啸的风", model.AssetAmbience, "风声", 10000, []string{"风", "天气", "环境"}},
	{"敲门声", model.AssetSFX, "敲门声", 2000, []string{"敲门", "门"}},
	{"开门（吱呀）", model.AssetSFX, "开门 吱呀", 2600, []string{"门", "开门"}},
	{"脚步声", model.AssetSFX, "脚步声", 5000, []string{"脚步", "走路"}},
	{"心跳", model.AssetSFX, "心跳声", 5000, []string{"心跳", "紧张"}},
	{"钟声", model.AssetSFX, "钟声", 5000, []string{"钟", "寺庙"}},
	{"钟表滴答", model.AssetAmbience, "钟表滴答", 8000, []string{"钟表", "时间", "环境"}},
	{"篝火噼啪", model.AssetAmbience, "篝火", 10000, []string{"火", "夜晚", "环境"}},
	{"溪流", model.AssetAmbience, "溪流 流水", 10000, []string{"水", "自然", "环境"}},
	{"海浪", model.AssetAmbience, "海浪", 12000, []string{"海", "浪", "环境"}},
	{"清晨鸟鸣", model.AssetAmbience, "鸟鸣", 10000, []string{"鸟", "清晨", "自然"}},
	{"电话铃声", model.AssetSFX, "电话铃声", 6000, []string{"电话", "铃声"}},
	{"兵器相击", model.AssetSFX, "兵器 剑", 2500, []string{"剑", "打斗", "武侠"}},
	{"爆炸", model.AssetSFX, "爆炸", 4000, []string{"爆炸", "战斗"}},
	{"马蹄声", model.AssetSFX, "马蹄", 5000, []string{"马", "古代"}},
	{"人群嘈杂", model.AssetAmbience, "人群 嘈杂", 10000, []string{"人群", "集市", "环境"}},
	{"掌声", model.AssetSFX, "掌声", 5000, []string{"掌声", "鼓掌"}},
	{"转场（嗖）", model.AssetSFX, "嗖 转场", 1500, []string{"转场", "嗖"}},
}

var officialBGM = []assetSeed{
	{"温柔夜曲", model.AssetBGM, "温柔 舒缓", 64000, []string{"舒缓", "温柔", "言情", "可循环"}},
	{"晨光小调", model.AssetBGM, "轻快 日常", 48000, []string{"轻快", "日常", "儿童", "可循环"}},
	{"离别", model.AssetBGM, "伤感 离别", 64000, []string{"伤感", "抒情", "可循环"}},
	{"暗夜追踪", model.AssetBGM, "悬疑 紧张", 56000, []string{"悬疑", "紧张", "推理", "可循环"}},
	{"山河远阔", model.AssetBGM, "史诗 激昂", 56000, []string{"史诗", "热血", "历史", "可循环"}},
	{"云深不知处", model.AssetBGM, "神秘 古风", 60000, []string{"神秘", "古风", "仙侠", "可循环"}},
}

func (a *App) seedAssets(ctx context.Context) {
	exists := func(name string) bool {
		var n int64
		a.DB.Model(&model.Asset{}).Where("scope = ? AND name = ? AND kind IN ?", model.ScopeSystem, name, []string{model.AssetSFX, model.AssetAmbience, model.AssetBGM}).Count(&n)
		return n > 0
	}
	save := func(s assetSeed, p *audio.PCM, meta map[string]any) {
		var buf bytes.Buffer
		audio.EncodeWAV16(&buf, p)
		a.SaveAssetBytes(ctx, AssetInput{Scope: model.ScopeSystem, Kind: s.kind, Name: s.name, Ext: "wav", Origin: "system", License: "commercial", Tags: s.tags, Listed: true, Description: "平台内置素材（程序化生成，可免费商用）", Meta: meta}, buf.Bytes())
	}
	for _, s := range officialSfx {
		if exists(s.name) {
			continue
		}
		p, kind := audio.GenerateSFX(s.prompt, s.dur, audio.Seed(s.name))
		save(s, p, map[string]any{"kind": kind})
	}
	for _, s := range officialBGM {
		if exists(s.name) {
			continue
		}
		p, mood := audio.GenerateMusic(s.prompt, s.dur, audio.Seed(s.name))
		save(s, p, map[string]any{"mood": mood})
	}
	slog.Info("official audio library ready")
}

func (a *App) seedTemplates() {
	var n int64
	a.DB.Model(&model.AudioTemplate{}).Where("scope = ?", model.ScopeSystem).Count(&n)
	if n > 0 {
		return
	}
	var bgm model.Asset
	a.DB.Where("scope = ? AND kind = ? AND name = ?", model.ScopeSystem, model.AssetBGM, "温柔夜曲").First(&bgm)
	a.DB.Create(&model.AudioTemplate{Scope: model.ScopeSystem, Kind: "intro", Name: "标准片头", MusicAssetID: bgm.ID, Text: "{书名}，{章节名}。", MusicVolume: -10, LeadMs: 2500, TailMs: 1500})
	a.DB.Create(&model.AudioTemplate{Scope: model.ScopeSystem, Kind: "outro", Name: "标准片尾", MusicAssetID: bgm.ID, Text: "本集到这里就结束了，感谢收听，我们下集再见。", MusicVolume: -10, LeadMs: 1200, TailMs: 3000})
	a.DB.Create(&model.AudioTemplate{Scope: model.ScopeSystem, Kind: "outro", Name: "片尾 + AI 声明", MusicAssetID: bgm.ID, Text: "本集到这里就结束了，感谢收听。", MusicVolume: -10, LeadMs: 1200, TailMs: 3000, Disclaimer: true})
}

// helpV1 is the first release's built-in help text, kept so syncHelpDocs can tell untouched articles from edited ones.
var helpV1 = []model.Article{
	{Kind: "help", Category: "入门", Title: "三步做出第一本有声书", Summary: "上传书稿、选角、一键生成", Featured: true, SortOrder: 1, Body: "## 1. 上传书稿\n在「图书」页点击 **新建图书**，上传 txt / docx / epub / pdf，系统会自动识别章节。\n\n## 2. 分析角色并选角\n进入图书后点击 **分析角色**，AI 会找出书中会说话的角色并写好人物小传；再点 **一键选角**，为每个角色挑选合适的音色。\n\n## 3. 生成与合成\n打开任意章节的 **制作台**：生成画本 → 生成配音 → 合成成品。也可以在图书概览中使用 **一键成书**，全自动完成。"},
	{Kind: "help", Category: "核心概念", Title: "什么是画本？", Summary: "画本是有声书的演播脚本", SortOrder: 2, Body: "画本把章节拆成一句句的**片段**，并为每句标注：\n\n- **说话人**：旁白还是某个角色\n- **情绪**：平静、开心、悲伤……\n- **注音**：多音字、生僻字的正确读音\n- **停顿**：与上一句之间留白多久\n\nAI 生成画本时不会改动原文，只做标注。你可以在编辑器里随时修改，按数字键 1–9 可以快速切换说话人。"},
	{Kind: "help", Category: "核心概念", Title: "选角与音色", Summary: "为角色挑选最合适的声音", SortOrder: 3, Body: "每个角色都可以绑定一个音色。点击角色卡片上的 **选角**，系统会根据角色小传推荐候选音色，并用该角色在书中的真实台词试听。\n\n找不到合适的？试试 **AI 设计音色**：用一句话描述你想要的声音即可。"},
	{Kind: "help", Category: "真人制作", Title: "真人录音与智能对轨", Summary: "AI 与真人混合制作", SortOrder: 4, Body: "把角色的配音方式设为 **真人** 并指派给配音演员（CV），CV 会在「我的台词」中看到自己的台词，可以逐句在线录音。\n\n如果 CV 习惯整章连续录制，上传整段录音后使用 **智能对轨**，系统会自动把录音切开并对应到每一句台词。"},
	{Kind: "help", Category: "质量", Title: "智能审听", Summary: "自动找出漏读、错读和音质问题", SortOrder: 5, Body: "智能审听会检查每句的音量、底噪、爆音和语速；如果平台配置了语音识别，还会逐字比对文稿，标出 **漏读、多读、错读**。发现的问题可以一键转为工单，指派给对应的成员返修。"},
	{Kind: "help", Category: "交付", Title: "导出与发布", Summary: "MP3 / M4B / 分轨 / 字幕", SortOrder: 6, Body: "在图书的 **成品** 页可以导出 MP3、WAV、M4A，或整本书的 M4B（带章节标记）；还能附带字幕、分角色分轨、演播稿和台词本。\n\n发布前，系统会检查元数据、技术指标与 AI 标识是否达标。"},
	{Kind: "help", Category: "账户", Title: "积分如何计算？", Summary: "按字数与次数计量", SortOrder: 7, Body: "AI 功能按积分计量：画本与配音按字数计算，设计音色、克隆、音效按次数计算，对轨与审听按音频时长计算。\n\n- 相同内容重复生成会**命中缓存，不扣积分**\n- 每句话有 **2 次免费重生成**（内容和音色不变时）\n- 每天有一定次数的免费试听"},
	{Kind: "help", Category: "管理员", Title: "配置 AI 模型", Summary: "OpenAI / Claude 协议均可接入", SortOrder: 8, Body: "管理员在 **管理后台 → AI 供应商** 中添加大模型账号。支持三种协议：\n\n1. **OpenAI Chat Completions**：兼容 DeepSeek、通义千问、Kimi、GLM、Ollama 等\n2. **OpenAI Responses**\n3. **Anthropic Claude Messages**\n\n填写接口地址、密钥与模型名后点击 **测试连接** 即可。"},
}

func (a *App) seedArticles() {
	var n int64
	a.DB.Model(&model.Article{}).Count(&n)
	if n > 0 {
		return
	}
	a.syncHelpDocs()
	a.DB.Create(&model.Article{Kind: "changelog", Title: "v1.0 正式发布", Version: "1.0.0", Published: true, Body: "- 智能拆章、角色分析与 AI 画本\n- AI 选角、AI 设计音色、声音克隆\n- AI 多播配音、真人录音与智能对轨\n- 多轨后期、智能审听、工单与生产管理\n- 一键成书、开放 API、多平台分发"})
}

func (a *App) seedLexicon() {
	var n int64
	a.DB.Model(&model.LexiconEntry{}).Where("scope = ?", "platform").Count(&n)
	if n > 0 {
		return
	}
	entries := [][3]string{
		{"银行", "yín háng", "行：银行读 háng"}, {"重新", "chóng xīn", ""}, {"长大", "zhǎng dà", ""}, {"还是", "hái shì", ""},
		{"了解", "liǎo jiě", ""}, {"睡觉", "shuì jiào", ""}, {"便宜", "pián yi", "价格低"}, {"处理", "chǔ lǐ", ""},
		{"调查", "diào chá", ""}, {"音乐", "yīn yuè", ""}, {"会计", "kuài jì", ""}, {"一会儿", "yí huìr", ""},
		{"似的", "shì de", ""}, {"差不多", "chà bu duō", ""}, {"给予", "jǐ yǔ", ""}, {"着急", "zháo jí", ""},
		{"恶心", "ě xin", ""}, {"露面", "lòu miàn", ""}, {"挣扎", "zhēng zhá", ""}, {"压轴", "yā zhòu", ""},
	}
	for _, e := range entries {
		a.DB.Create(&model.LexiconEntry{Scope: "platform", Term: e[0], Pinyin: e[1], Note: e[2], Status: "active"})
	}
}

func (a *App) seedEval() {
	var n int64
	a.DB.Model(&model.EvalSet{}).Count(&n)
	if n > 0 {
		return
	}
	set := model.EvalSet{Name: "说话人归属 · 基础样例", Kind: "script_annotation", LastRun: model.EmptyMap()}
	set.History.V = []map[string]any{}
	set.Items.V = []model.EvalItem{{
		Text:       "夜色渐深，她推开窗。\n“你来做什么？”林黛玉冷冷地问。\n宝玉笑道：“我来看看妹妹。”他顿了顿，又说：“外面下雨了。”",
		Characters: []model.EvalCharacter{{Name: "林黛玉", Aliases: []string{"黛玉"}, Gender: "female"}, {Name: "贾宝玉", Aliases: []string{"宝玉"}, Gender: "male"}},
		Expected: []model.EvalLabel{
			{Index: 0, Speaker: "旁白", Type: "narration"},
			{Index: 1, Speaker: "林黛玉", Type: "dialogue"},
			{Index: 2, Speaker: "旁白", Type: "narration"},
			{Index: 4, Speaker: "贾宝玉", Type: "dialogue"},
			{Index: 6, Speaker: "贾宝玉", Type: "dialogue"},
		},
	}}
	a.DB.Create(&set)
}

func (a *App) seedSensitive() {
	var n int64
	a.DB.Model(&model.SensitiveWord{}).Count(&n)
	if n == 0 {
		a.DB.Create(&model.SensitiveWord{Word: "示例敏感词", Level: "review", Category: "示例"})
	}
}

var _ = time.Second
