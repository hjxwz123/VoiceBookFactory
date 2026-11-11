package core

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/ai/asr"
	"voicebook/internal/ai/tts"
	"voicebook/internal/align"
	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// VoiceVisible reports whether a user may use/see a voice.
func (a *App) VoiceVisible(u *model.User, studioID string, v *model.Voice) bool {
	switch v.Scope {
	case model.ScopeSystem:
		return v.Listed || u.IsAdmin()
	case model.ScopeStudio:
		return v.StudioID == studioID
	case model.ScopeUser:
		if v.OwnerUserID == u.ID {
			return true
		}
		if v.Shared && v.StudioID == studioID {
			return true
		}
		var l model.VoiceListing
		return a.DB.First(&l, "voice_id = ? AND status = ?", v.ID, "listed").Error == nil
	}
	return false
}

func (a *App) CanManageVoice(x *Access, v *model.Voice) bool {
	switch v.Scope {
	case model.ScopeUser:
		return v.OwnerUserID == x.User.ID
	case model.ScopeStudio:
		return v.StudioID == x.StudioID && (x.IsManager() || x.Role == model.RoleProducer || v.CreatedBy == x.User.ID)
	}
	return false
}

func (a *App) VoiceView(v *model.Voice, userID string) map[string]any {
	m := map[string]any{
		"id": v.ID, "scope": v.Scope, "studioId": v.StudioID, "ownerUserId": v.OwnerUserID, "name": v.Name, "origin": v.Origin,
		"provider": v.Provider, "providerVoiceId": v.ProviderVoiceID, "model": v.Model, "language": v.Language, "gender": v.Gender,
		"ageGroup": v.AgeGroup, "tags": v.Tags.V, "description": v.Description, "designPrompt": v.DesignPrompt, "sampleText": v.SampleText,
		"defaults": v.Defaults.V, "emotions": v.Emotions.V, "priceTier": v.PriceTier, "status": v.Status, "statusNote": v.StatusNote,
		"expiresAt": v.ExpiresAt, "listed": v.Listed, "recommended": v.Recommended, "shared": v.Shared, "crowdPool": v.CrowdPool,
		"usageCount": v.UsageCount, "cloneTier": v.CloneTier, "createdAt": v.CreatedAt, "sampleUrl": a.VoiceSampleURL(v), "sortOrder": v.SortOrder,
		"modelLabel": tts.ModelLabel(v.Model),
	}
	m["capabilities"] = VoiceCaps(v)
	if userID != "" {
		var n int64
		a.DB.Model(&model.VoiceFavorite{}).Where("user_id = ? AND voice_id = ?", userID, v.ID).Count(&n)
		m["favorite"] = n > 0
	}
	var l model.VoiceListing
	if a.DB.First(&l, "voice_id = ?", v.ID).Error == nil {
		m["listing"] = l
	}
	return m
}

// VoiceSampleURL prefers a locally stored preview and falls back to the platform's official preview link.
func (a *App) VoiceSampleURL(v *model.Voice) string {
	if v.SampleAssetID != "" {
		if u := a.AssetURLByID(v.SampleAssetID); u != "" {
			return u
		}
	}
	return v.SampleURL
}

// VoiceCaps describes what a voice supports without needing account credentials.
func VoiceCaps(v *model.Voice) tts.Capabilities {
	if p, err := tts.New(tts.Account{Provider: v.Provider}); err == nil {
		return tts.CapsFor(p, v.Model, v.ProviderVoiceID)
	}
	return tts.Capabilities{}
}

// ---------- AI voice design ----------

func (a *App) StartVoiceDesign(x *Access, description, previewText, provider, characterID string) (*model.VoiceDesign, *model.Job, error) {
	description = strings.TrimSpace(description)
	if characterID != "" && description == "" {
		var c model.Character
		if a.DB.First(&c, "id = ?", characterID).Error == nil {
			if h, err := a.LLMFor(FeatDesign, "", x.StudioID); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				description, _ = a.DescribeVoice(ctx, h, c)
				cancel()
			}
			if description == "" {
				description = strings.TrimSpace(c.VoiceTraits + " " + c.Personality)
			}
		}
	}
	if len([]rune(description)) < 6 {
		return nil, nil, Bad("请用至少一句话描述想要的声音，例如：二十岁左右的女生，声音清亮温柔，语速偏慢")
	}
	if len([]rune(description)) > 1000 {
		return nil, nil, Bad("描述最多 1000 字")
	}
	d := &model.VoiceDesign{UserID: x.User.ID, StudioID: x.StudioID, Prompt: description, Provider: provider, PreviewText: previewText, Status: "running", Attributes: model.EmptyMap()}
	d.Candidates.V = []model.DesignCandidate{}
	if err := a.DB.Create(d).Error; err != nil {
		return nil, nil, err
	}
	j := &model.Job{Type: "voice_design", StudioID: x.StudioID, RefType: "voice_design", RefID: d.ID, Title: "AI 设计音色", CreatedBy: x.User.ID, MaxAttempts: 1}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, nil, err
	}
	a.DB.Model(d).Update("job_id", j.ID)
	return d, j, nil
}

func (a *App) designAccount(provider, studioID string, want func(tts.Capabilities) bool) (tts.Provider, *model.ProviderAccount, error) {
	var accs []model.ProviderAccount
	q := a.DB.Where("kind = ? AND enabled = ? AND (scope = 'platform' OR studio_id = ?)", model.KindTTS, true, studioID)
	if provider != "" {
		q = q.Where("provider = ?", provider)
	}
	q.Order("CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, priority desc").Find(&accs) // real platforms before the demo engine
	for i := range accs {
		p, err := tts.New(a.ttsAccount(&accs[i]))
		if err == nil && want(p.Capabilities()) {
			return p, &accs[i], nil
		}
	}
	return nil, nil, &Error{Status: 503, Code: "tts_not_configured", Msg: "没有支持该功能的语音平台账号"}
}

func (a *App) jobVoiceDesign(ctx context.Context, jc *jobs.Context) error {
	var d model.VoiceDesign
	if err := a.DB.First(&d, "id = ?", jc.Job.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	fail := func(err error) error {
		a.DB.Model(&d).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 250)})
		return jobs.Permanent(err)
	}
	jc.Progress(0.1, "正在理解你的声音描述")
	attrs := RuleVoiceAttributes(d.Prompt)
	if h, err := a.LLMFor(FeatDesign, "", d.StudioID); err == nil {
		if v, err := a.ParseVoiceAttributes(ctx, h, d.Prompt); err == nil {
			attrs = v
		}
	}
	p, acc, err := a.designAccount(d.Provider, d.StudioID, func(c tts.Capabilities) bool { return c.VoiceDesign })
	if err != nil {
		return fail(err)
	}
	jc.Progress(0.4, "正在生成 3 个候选声音（平台审核通过后显示）")
	cands, err := p.(tts.VoiceDesigner).DesignVoice(ctx, tts.DesignRequest{Description: d.Prompt, PreviewText: d.PreviewText, Gender: attrs.Gender, AgeGroup: attrs.AgeGroup, Count: 3, Seed: d.ID})
	if err != nil {
		return fail(err) // nothing was generated: nothing is charged
	}
	if _, err := a.ChargeCredits(Charge{StudioID: d.StudioID, UserID: d.UserID, JobID: jc.Job.ID, Feature: FeatDesign, Quantity: 1, Unit: "calls", Provider: acc.Provider, AccountID: acc.ID}); err != nil {
		if cl, ok := p.(tts.VoiceCloner); ok { // do not leave unpaid candidates on the platform
			for _, c := range cands {
				_ = cl.DeleteVoice(context.WithoutCancel(ctx), c.ProviderVoiceID)
			}
		}
		return fail(err)
	}
	var out []model.DesignCandidate
	for i, c := range cands {
		as, _, err := a.storeSpeech(ctx, d.StudioID, d.UserID, c.Preview, model.AssetPreview, map[string]any{"designId": d.ID})
		if err != nil {
			return fail(err)
		}
		out = append(out, model.DesignCandidate{ProviderVoiceID: c.ProviderVoiceID, PreviewAssetID: as.ID, Seed: i, Model: c.Model, PriceTier: c.PriceTier})
	}
	a.DB.Model(&d).Updates(map[string]any{"status": "ready", "provider": acc.Provider, "account_id": acc.ID, "candidates": model.JSON[[]model.DesignCandidate]{V: out},
		"attributes": model.Map{V: map[string]any{"gender": attrs.Gender, "ageGroup": attrs.AgeGroup, "tags": attrs.Tags, "name": attrs.Name}}})
	jc.Progress(1, "候选声音已生成，挑选一个保存吧")
	return nil
}

func (a *App) DesignView(d *model.VoiceDesign) map[string]any {
	var cands []map[string]any
	for i, c := range d.Candidates.V {
		cands = append(cands, map[string]any{"index": i, "providerVoiceId": c.ProviderVoiceID, "previewUrl": a.AssetURLByID(c.PreviewAssetID), "saved": c.Saved,
			"discarded": c.Discarded, "model": c.Model, "modelLabel": tts.ModelLabel(c.Model)})
	}
	return map[string]any{"id": d.ID, "prompt": d.Prompt, "status": d.Status, "error": d.Error, "provider": d.Provider, "attributes": d.Attributes.V, "candidates": cands, "jobId": d.JobID, "createdAt": d.CreatedAt}
}

func (a *App) SaveDesignedVoice(x *Access, d *model.VoiceDesign, idx int, name string, tags []string, scope string) (*model.Voice, error) {
	if d.UserID != x.User.ID {
		return nil, Forbidden("")
	}
	if d.Status != "ready" || idx < 0 || idx >= len(d.Candidates.V) {
		return nil, Bad("候选声音不存在")
	}
	c := d.Candidates.V[idx]
	if c.Saved {
		return nil, Bad("这个候选已经保存过了，可以在「我的」音色里找到")
	}
	if c.Discarded {
		return nil, Bad("这个候选超过 %d 小时没有保存，已从语音平台清理，请重新生成", int(designKeep.Hours()))
	}
	attrs := d.Attributes.V
	if name == "" {
		name, _ = attrs["name"].(string)
	}
	if len(tags) == 0 {
		if t, ok := attrs["tags"].([]any); ok {
			for _, x := range t {
				tags = append(tags, fmt.Sprint(x))
			}
		}
	}
	v := &model.Voice{Scope: model.ScopeUser, StudioID: x.StudioID, OwnerUserID: x.User.ID, Name: firstNonEmpty(name, "我的设计音色"), Origin: model.VoiceDesigned,
		Provider: d.Provider, ProviderAccountID: d.AccountID, ProviderVoiceID: c.ProviderVoiceID, Language: "zh-CN", Gender: fmt.Sprint(attrs["gender"]), AgeGroup: fmt.Sprint(attrs["ageGroup"]),
		Tags: model.Strings(tags...), Description: d.Prompt, DesignPrompt: d.Prompt, SampleAssetID: c.PreviewAssetID, PriceTier: firstNonEmpty(c.PriceTier, model.TierStandard), Status: model.VoiceReady, CreatedBy: x.User.ID, Emotions: model.Strings(),
		Model: c.Model}
	v.Defaults.V = model.VoiceDefaults{Speed: 1}
	if scope == model.ScopeStudio {
		if !(x.IsManager() || x.Role == model.RoleProducer) {
			return nil, Forbidden("只有管理员或制作人可以创建工作室音色")
		}
		v.Scope, v.OwnerUserID = model.ScopeStudio, ""
	}
	if err := a.DB.Create(v).Error; err != nil {
		return nil, err
	}
	d.Candidates.V[idx].Saved = true
	a.DB.Model(d).Update("candidates", d.Candidates)
	return v, nil
}

// designKeep is how long unsaved design candidates stay on the platform (they count against its voice quota).
const designKeep = 24 * time.Hour

// CleanupDesignCandidates removes candidates nobody saved from the platform once designKeep has passed.
func (a *App) CleanupDesignCandidates() {
	var designs []model.VoiceDesign
	a.DB.Where("status = ? AND created_at < ? AND provider <> ? AND provider <> ''", "ready", now().Add(-designKeep), "placeholder").Find(&designs)
	for i := range designs {
		d := &designs[i]
		pending := false
		for _, c := range d.Candidates.V {
			if !c.Saved && !c.Discarded && c.ProviderVoiceID != "" {
				pending = true
			}
		}
		if !pending {
			continue
		}
		var acc model.ProviderAccount
		if a.DB.First(&acc, "id = ?", d.AccountID).Error != nil {
			continue
		}
		p, err := tts.New(a.ttsAccount(&acc))
		if err != nil {
			continue
		}
		cl, ok := p.(tts.VoiceCloner)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		changed := false
		for k, c := range d.Candidates.V {
			if c.Saved || c.Discarded || c.ProviderVoiceID == "" {
				continue
			}
			if err := cl.DeleteVoice(ctx, c.ProviderVoiceID); err != nil {
				slog.Warn("cleanup design candidate", "design", d.ID, "voice", c.ProviderVoiceID, "err", err)
				continue
			}
			d.Candidates.V[k].Discarded, changed = true, true
		}
		cancel()
		if changed {
			a.DB.Model(d).Update("candidates", d.Candidates)
		}
	}
}

// DesignOption tells the design page which platform designs voices and its limits.
type DesignOption struct {
	Available  bool   `json:"available"`
	Provider   string `json:"provider"`
	Platform   string `json:"platform"`
	Demo       bool   `json:"demo"`
	ModelLabel string `json:"modelLabel,omitempty"`
	PromptMax  int    `json:"promptMax"`
	KeepHours  int    `json:"keepHours"` // unsaved candidates are removed after this many hours
}

func (a *App) DesignOptions(studioID string) DesignOption {
	_, acc, err := a.designAccount("", studioID, func(c tts.Capabilities) bool { return c.VoiceDesign })
	if err != nil {
		return DesignOption{}
	}
	o := DesignOption{Available: true, Provider: acc.Provider, Platform: firstNonEmpty(platformNames[acc.Provider], acc.Name), Demo: acc.Provider == "placeholder", PromptMax: 1000}
	if c := platformCaps(acc.Provider); c.DesignPromptMax > 0 {
		o.PromptMax = c.DesignPromptMax
	}
	if acc.Provider == "qwen" || acc.Provider == "aliyun" {
		o.ModelLabel = tts.ModelLabel(tts.QwenDesignModelFor(acc.Config.V))
		o.KeepHours = int(designKeep.Hours())
	}
	return o
}

// ---------- cloning ----------

var challengeSentences = []string{
	"我确认这是我本人的声音，同意用于创建个人音色。",
	"今天天气晴朗，我在安静的房间里录下这段确认语音。",
	"我授权平台使用我的声音样本训练专属音色。",
	"春风拂过窗台，我愿意为自己的声音负责。",
	"这段录音由我本人完成，仅用于创建我的声音。",
}

func ChallengeSentence() string { return challengeSentences[rand.Intn(len(challengeSentences))] }

type SampleCheck struct {
	DurationMs int64    `json:"durationMs"`
	RMSDB      float64  `json:"rmsDb"`
	PeakDB     float64  `json:"peakDb"`
	NoiseDB    float64  `json:"noiseDb"`
	Clipped    int      `json:"clipped"`
	SpeechMs   int64    `json:"speechMs"`
	OK         bool     `json:"ok"`
	Tips       []string `json:"tips"`
	Score      int      `json:"score"`
}

// CheckSample analyses a cloning sample and returns actionable tips.
func (a *App) CheckSample(ctx context.Context, path, tier string) (*SampleCheck, error) {
	pcm, err := a.FF.DecodePCM(ctx, path, 22050, 1, "")
	if err != nil {
		return nil, Bad("无法读取音频：%v", err)
	}
	st := audio.Analyze(pcm)
	c := &SampleCheck{DurationMs: st.DurationMs, RMSDB: st.RMSDB, PeakDB: st.PeakDB, NoiseDB: st.NoiseFloorDB, Clipped: st.ClippedSamples, SpeechMs: st.SpeechMs, OK: true, Score: 100}
	minMs, maxMs := int64(10_000), int64(120_000)
	if tier == "pro" {
		minMs, maxMs = 30*60_000, 4*3600_000
	}
	if st.SpeechMs < minMs {
		c.OK = false
		c.Score -= 40
		c.Tips = append(c.Tips, fmt.Sprintf("有效人声太短（%.0f 秒），至少需要 %.0f 秒", float64(st.SpeechMs)/1000, float64(minMs)/1000))
	}
	if tier != "pro" && st.DurationMs > maxMs {
		c.Tips = append(c.Tips, "快速克隆只需要 10 秒到 2 分钟的样本，超出部分会被截取")
	}
	if st.RMSDB < -35 {
		c.Score -= 20
		c.Tips = append(c.Tips, "音量偏小，请靠近麦克风或提高输入增益")
	}
	if st.ClippedSamples > 50 {
		c.Score -= 20
		c.Tips = append(c.Tips, "存在爆音（削波），请降低输入音量")
	}
	if st.NoiseFloorDB > -45 {
		c.Score -= 20
		c.Tips = append(c.Tips, "背景噪声较大，建议在安静环境重新录制，或勾选「自动降噪」")
	}
	if len(c.Tips) == 0 {
		c.Tips = []string{"样本质量良好"}
	}
	if c.Score < 0 {
		c.Score = 0
	}
	return c, nil
}

type CloneInput struct {
	Name        string
	Tier        string
	ConsentType string
	SpeakerName string
	Identity    string
	Statement   string
	Challenge   string
	Gender      string
	AgeGroup    string
	Provider    string
	Denoise     bool
	SampleIDs   []string
	EvidenceID  string
	DocumentID  string
	Scope       string
}

func (a *App) StartClone(ctx context.Context, x *Access, in CloneInput) (*model.Voice, error) {
	if in.Tier != "pro" {
		in.Tier = "quick"
	}
	if in.Tier == "pro" {
		if err := a.RequireFeature(x.StudioID, model.FeatProClone, "专业克隆"); err != nil {
			return nil, err
		}
	}
	if len(in.SampleIDs) == 0 {
		return nil, Bad("请上传或录制声音样本")
	}
	if err := a.checkCloneLimit(x); err != nil {
		return nil, err
	}
	if opt := a.CloneOptions(x.StudioID)[in.Tier]; !opt.Available {
		return nil, &Error{Status: 503, Code: "tts_not_configured", Msg: "平台还没有开通支持" + map[string]string{"quick": "快速克隆", "pro": "专业克隆"}[in.Tier] + "的语音账号，请联系管理员"}
	} else if opt.MinSeconds > 0 {
		var total int64
		for _, id := range in.SampleIDs {
			if _, as, err := a.AssetPath(id); err == nil {
				total += int64(as.DurationMs)
			}
		}
		if total > 0 && total < int64(opt.MinSeconds)*1000 {
			return nil, Bad("样本太短：%s至少需要 %d 秒的朗读，现在只有约 %d 秒，请再录一段", spaced(opt.Platform), opt.MinSeconds, (total+500)/1000)
		}
	}
	consent := model.VoiceConsent{Type: in.ConsentType, UserID: x.User.ID, StudioID: x.StudioID, SpeakerName: in.SpeakerName, IdentityInfo: maskIdentity(in.Identity), Statement: in.Statement, Challenge: in.Challenge, EvidenceAsset: in.EvidenceID, DocumentAsset: in.DocumentID}
	status := model.VoiceTraining
	switch in.ConsentType {
	case "self":
		if in.EvidenceID == "" || in.Challenge == "" {
			return nil, Bad("请朗读并录制验证句，以确认是本人声音")
		}
		consent.SpeakerName = firstNonEmpty(in.SpeakerName, x.User.Nickname)
		ok, note := a.verifyChallenge(ctx, x.StudioID, in.EvidenceID, in.Challenge)
		if ok {
			ok, note = a.verifyVoiceprint(ctx, x.StudioID, in.Provider, in.EvidenceID, in.SampleIDs[0], note)
		}
		if !ok {
			return nil, Bad("验证句录音未通过校验：%s。请清晰朗读屏幕上的句子后重试", note)
		}
		consent.Status = "approved"
		consent.ReviewNote = note
	case "other":
		if in.DocumentID == "" || strings.TrimSpace(in.SpeakerName) == "" {
			return nil, Bad("克隆他人声音需要填写声音主人姓名并上传授权书")
		}
		consent.Status = "pending"
		status = model.VoiceReviewing
	default:
		return nil, Bad("请选择声音授权方式")
	}
	v := &model.Voice{Scope: model.ScopeUser, StudioID: x.StudioID, OwnerUserID: x.User.ID, Name: firstNonEmpty(in.Name, consent.SpeakerName+"的声音"), Origin: model.VoiceCloned,
		Provider: in.Provider, Language: "zh-CN", Gender: firstNonEmpty(in.Gender, "unknown"), AgeGroup: firstNonEmpty(in.AgeGroup, "adult"), Tags: model.Strings("克隆"),
		PriceTier: model.TierStandard, Status: status, CloneTier: in.Tier, SampleAssetID: in.SampleIDs[0], CreatedBy: x.User.ID, Emotions: model.Strings()}
	v.Defaults.V = model.VoiceDefaults{Speed: 1}
	if in.Scope == model.ScopeStudio && (x.IsManager() || x.Role == model.RoleProducer) {
		v.Scope, v.OwnerUserID = model.ScopeStudio, ""
	}
	if err := a.DB.Create(v).Error; err != nil {
		return nil, err
	}
	consent.VoiceID = v.ID
	a.DB.Create(&consent)
	a.DB.Model(v).Update("consent_id", consent.ID)
	meta := map[string]any{"samples": in.SampleIDs, "denoise": in.Denoise}
	a.DB.Model(v).Update("status_note", "")
	if status == model.VoiceTraining {
		a.enqueueClone(v, x.User.ID, meta)
	} else {
		a.DB.Model(v).Update("status_note", "等待平台审核授权材料（通常 1 个工作日内）")
	}
	return v, nil
}

// CloneOption tells the clone page which platform handles a tier and what it needs.
type CloneOption struct {
	Available     bool   `json:"available"`
	Provider      string `json:"provider"`
	Platform      string `json:"platform"`
	Demo          bool   `json:"demo"` // only the built-in demo engine: nothing is really cloned
	MinSeconds    int    `json:"minSeconds"`
	MaxSeconds    int    `json:"maxSeconds"`
	Recommend     int    `json:"recommendSeconds"` // sample length worth recording
	ExpiryDays    int    `json:"expiryDays"`
	ExpiresUnused bool   `json:"expiresUnused"`
	ModelLabel    string `json:"modelLabel,omitempty"` // model new clones are bound to, when the platform binds them
}

var platformNames = map[string]string{"minimax": "MiniMax", "qwen": "千问", "aliyun": "千问", "placeholder": "内置演示引擎", "elevenlabs": "ElevenLabs", "volcengine": "火山引擎"}

// CloneOptions resolves the platform used for quick and professional cloning in a studio.
func (a *App) CloneOptions(studioID string) map[string]CloneOption {
	out := map[string]CloneOption{}
	for _, tier := range []string{"quick", "pro"} {
		_, acc, err := a.designAccount("", studioID, func(c tts.Capabilities) bool {
			if tier == "pro" {
				return c.ProClone
			}
			return c.QuickClone
		})
		if err != nil {
			out[tier] = CloneOption{}
			continue
		}
		if acc.Provider == "placeholder" && !a.Cfg.DevMode {
			// the demo engine cannot really copy anyone's voice: in production the tier is simply not offered
			out[tier] = CloneOption{}
			continue
		}
		c := platformCaps(acc.Provider)
		o := CloneOption{Available: true, Provider: acc.Provider, Platform: firstNonEmpty(platformNames[acc.Provider], acc.Name), Demo: acc.Provider == "placeholder",
			MinSeconds: c.CloneMinSeconds, MaxSeconds: c.CloneMaxSeconds, ExpiryDays: c.CloneExpiryDays, ExpiresUnused: c.CloneExpiresUnused, Recommend: 30}
		if tier == "pro" {
			o.Recommend = 30 * 60
		} else if o.MaxSeconds > 0 && o.MaxSeconds < 90 {
			o.Recommend = 20 // e.g. Qianwen: 10-20 s works best
		}
		if acc.Provider == "qwen" || acc.Provider == "aliyun" {
			m, _ := acc.Config.V["cloneModel"].(string)
			if !contains(tts.QwenCloneModels, m) {
				m = tts.QwenDefaultCloneModel
			}
			o.ModelLabel = tts.ModelLabel(m)
		}
		out[tier] = o
	}
	return out
}

func maskIdentity(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= 6 {
		return string(r)
	}
	return string(r[:3]) + strings.Repeat("*", len(r)-6) + string(r[len(r)-3:])
}

func (a *App) checkCloneLimit(x *Access) error {
	p := a.StudioPlan(x.StudioID)
	if p.CloneVoices <= 0 { // 0 = cloning not included
		return &Error{Status: 403, Code: "plan_required", Msg: "「" + p.Name + "」不包含声音克隆，升级会员后可用", Data: map[string]any{"kind": "cloneVoices"}}
	}
	n := a.clonedVoices(x.StudioID)
	if n >= int64(p.CloneVoices) {
		return planLimit("cloneVoices", int64(p.CloneVoices), n, "「%s」每个工作室最多保留 %d 个克隆音色，可删除不用的音色或升级会员", p.Name, p.CloneVoices)
	}
	return nil
}

func (a *App) clonedVoices(studioID string) int64 {
	var n int64
	a.DB.Model(&model.Voice{}).Where("origin = ? AND studio_id = ? AND status NOT IN ?", model.VoiceCloned, studioID, []string{model.VoiceFailed, model.VoiceDisabled}).Count(&n)
	return n
}

// verifyChallenge checks the consent recording: duration/level always, and the spoken text when ASR is configured.
func (a *App) verifyChallenge(ctx context.Context, studioID, assetID, challenge string) (bool, string) {
	path, as, err := a.AssetPath(assetID)
	if err != nil {
		return false, "录音不存在"
	}
	if as.DurationMs < 1500 {
		return false, "录音太短"
	}
	prov, _, err := a.ASRFor(studioID)
	if err != nil {
		return true, "已录制验证句（未配置语音识别，未做文本比对）"
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := prov.Transcribe(ctx, path, "zh-CN")
	if err != nil {
		if err == asr.ErrNotImplemented {
			return true, "已录制验证句"
		}
		return true, "已录制验证句（识别服务暂不可用）"
	}
	_, score := align.Compare(challenge, res.Text)
	if score < 0.6 {
		return false, fmt.Sprintf("识别到的内容与验证句差异较大（相似度 %.0f%%）", score*100)
	}
	return true, fmt.Sprintf("验证句比对通过（相似度 %.0f%%）", score*100)
}

func (a *App) enqueueClone(v *model.Voice, userID string, meta map[string]any) {
	j := &model.Job{Type: "voice_clone", StudioID: v.StudioID, RefType: "voice", RefID: v.ID, Title: "声音克隆 · " + v.Name, CreatedBy: userID, MaxAttempts: 2, Payload: model.Map{V: meta}}
	a.Q.Enqueue(j)
	a.DB.Model(v).Update("training_job_id", j.ID)
}

func (a *App) jobVoiceClone(ctx context.Context, jc *jobs.Context) error {
	var v model.Voice
	if err := a.DB.First(&v, "id = ?", jc.Job.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	fail := func(err error) error {
		a.DB.Model(&v).Updates(map[string]any{"status": model.VoiceFailed, "status_note": truncateStr(err.Error(), 250)})
		a.Notify(v.CreatedBy, v.StudioID, "voice_failed", "声音克隆失败", err.Error(), "/voices")
		return jobs.Permanent(err)
	}
	tier := v.CloneTier
	p, acc, err := a.designAccount(v.Provider, v.StudioID, func(c tts.Capabilities) bool {
		if tier == "pro" {
			return c.ProClone
		}
		return c.QuickClone
	})
	if err != nil {
		return fail(err)
	}
	caps := p.Capabilities()
	var paths []string
	if raw, ok := jc.Job.Payload.V["samples"].([]any); ok {
		for _, id := range raw {
			if path, _, err := a.AssetPath(fmt.Sprint(id)); err == nil {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return fail(Bad("声音样本不存在，请重新上传"))
	}
	denoise, _ := jc.Job.Payload.V["denoise"].(bool)
	dir, err := a.Store.TempDir("clone")
	if err != nil {
		return fail(err)
	}
	defer removeAll(dir)
	jc.Progress(0.1, "正在整理声音样本")
	merged, err := a.mergeCloneSamples(ctx, paths, dir, denoise && !caps.ServerDenoise, caps.CloneMaxSeconds)
	if err != nil {
		return fail(err)
	}
	if denoise && !caps.ServerDenoise { // the original files are denoised too (some platforms read them directly)
		for i, sp := range paths {
			out := fmt.Sprintf("%s/s%d.wav", dir, i)
			if a.FF.ToWAV(ctx, sp, out, 44100, 1, cloneDenoise) == nil {
				paths[i] = out
			}
		}
	}
	jc.Progress(0.2, "已提交训练")
	sampleText := "你好，这是用我的声音克隆出来的音色，听起来像我吗？"
	res, err := p.(tts.VoiceCloner).CloneVoice(ctx, tts.CloneRequest{Name: v.Name, SamplePaths: paths, MergedPath: merged, Tier: tier, Language: v.Language, Gender: v.Gender, AgeGroup: v.AgeGroup, Seed: v.ID,
		Denoise: denoise, PreviewText: sampleText})
	if err != nil {
		return fail(err)
	}
	first := *res // status polls only report the state; keep what the clone call told us
	for i := 0; res.Status == "training" && i < 360; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
		jc.Progress(0.2+float64(i)/360*0.7, "平台审核中…")
		if res, err = p.(tts.VoiceCloner).CloneStatus(ctx, first.ProviderVoiceID); err != nil {
			return fail(err)
		}
	}
	if res.Status != "ready" {
		return fail(fmt.Errorf("训练未完成：%s", firstNonEmpty(res.Message, "平台长时间没有完成处理，请稍后重试")))
	}
	res.Model, res.PriceTier, res.Preview, res.ExpiresInDays = first.Model, first.PriceTier, first.Preview, first.ExpiresInDays
	res.Message = firstNonEmpty(res.Message, first.Message)
	feat := FeatClone
	if _, err := a.ChargeCredits(Charge{StudioID: v.StudioID, UserID: v.CreatedBy, JobID: jc.Job.ID, Feature: feat, Tier: tier, Quantity: 1, Unit: "calls", Provider: acc.Provider, AccountID: acc.ID}); err != nil {
		return fail(err)
	}
	up := map[string]any{"status": model.VoiceReady, "provider": acc.Provider, "provider_account_id": acc.ID, "provider_voice_id": res.ProviderVoiceID, "status_note": res.Message}
	if res.Model != "" { // e.g. Qianwen clones only work with the model they were created for
		up["model"] = res.Model
	}
	if res.PriceTier != "" {
		up["price_tier"] = res.PriceTier
	}
	days := res.ExpiresInDays
	if days == 0 {
		days = caps.CloneExpiryDays
	}
	if days > 0 {
		exp := now().AddDate(0, 0, days)
		up["expires_at"] = &exp
	}
	a.DB.Model(&v).Updates(up)
	a.DB.First(&v, "id = ?", v.ID)
	// sample: the platform's own preview when it returns one; otherwise synthesize one, unless the first
	// synthesis is billed as activation (the uploaded sample stays the preview until the voice is used)
	preview := res.Preview
	if preview == nil && !caps.CloneExpiresUnused {
		if out, err := p.Synthesize(ctx, tts.SynthesizeRequest{Text: sampleText, ProviderVoiceID: v.ProviderVoiceID, Model: v.Model, Speed: 1, VoiceGender: v.Gender, VoiceAge: v.AgeGroup, VoiceSeed: v.ID}); err == nil {
			preview = out
		}
	}
	if preview != nil {
		if as, _, err := a.storeSpeech(ctx, v.StudioID, v.CreatedBy, preview, model.AssetPreview, map[string]any{"voiceId": v.ID}); err == nil {
			a.DB.Model(&v).Updates(map[string]any{"sample_asset_id": as.ID, "sample_text": sampleText})
		}
	}
	a.Notify(v.CreatedBy, v.StudioID, "voice_ready", "声音克隆完成", "「"+v.Name+"」已可以使用。"+res.Message, "/voices")
	jc.Progress(1, "音色已可用")
	return nil
}

const cloneDenoise = "afftdn=nf=-28,highpass=f=70"

// mergeCloneSamples joins every sample into one mono 32 kHz WAV (short pauses between files), the single-file
// input cloning APIs expect, cut to the platform's limit (at most 290 s). Browser recordings (webm/ogg) are
// converted on the way.
func (a *App) mergeCloneSamples(ctx context.Context, paths []string, dir string, denoise bool, platformMax int) (string, error) {
	const rate = 32000
	maxSec := 290
	if platformMax > 0 && platformMax-2 < maxSec {
		maxSec = platformMax - 2
	}
	filter := ""
	if denoise {
		filter = cloneDenoise
	}
	var all []float32
	for i, sp := range paths {
		pcm, err := a.FF.DecodePCM(ctx, sp, rate, 1, filter)
		if err != nil {
			return "", fmt.Errorf("第 %d 段样本无法读取：%w", i+1, err)
		}
		if len(all) > 0 {
			all = append(all, make([]float32, rate*4/10)...)
		}
		all = append(all, audio.TrimSilence(pcm, -45, 120).Data...)
		if len(all) >= rate*maxSec {
			all = all[:rate*maxSec]
			break
		}
	}
	out := filepath.Join(dir, "clone_sample.wav")
	return out, audio.WriteWAVFile(out, &audio.PCM{Rate: rate, Channels: 1, Data: all})
}

// spaced adds the space Chinese typography puts after a Latin word ("MiniMax 至少…", "千问至少…").
func spaced(name string) string {
	if r := []rune(name); len(r) > 0 && r[len(r)-1] < 0x80 {
		return name + " "
	}
	return name
}

// platformCaps describes a platform as a whole (without account credentials).
func platformCaps(provider string) tts.Capabilities {
	if p, err := tts.New(tts.Account{Provider: provider}); err == nil {
		return p.Capabilities()
	}
	return tts.Capabilities{}
}

// voiceUsed runs after a successful synthesis with a voice: a clone that the platform would delete when
// unused is now kept, so its expiry is cleared.
func (a *App) voiceUsed(v *model.Voice, p tts.Provider) {
	if v == nil || v.ExpiresAt == nil || v.Origin != model.VoiceCloned || !p.Capabilities().CloneExpiresUnused {
		return
	}
	a.DB.Model(&model.Voice{}).Where("id = ?", v.ID).Updates(map[string]any{"expires_at": nil, "status_note": "已激活，长期有效"})
	v.ExpiresAt = nil
}

func removeAll(dir string) { _ = deleteDir(dir) }

// ReviewConsent is used by the admin console for third-party voice authorisations.
func (a *App) ReviewConsent(adminID, consentID string, approve bool, note string) error {
	var c model.VoiceConsent
	if err := a.DB.First(&c, "id = ?", consentID).Error; err != nil {
		return NotFound("授权记录")
	}
	t := now()
	status := "rejected"
	if approve {
		status = "approved"
	}
	a.DB.Model(&c).Updates(map[string]any{"status": status, "reviewer_id": adminID, "review_note": note, "reviewed_at": &t})
	var v model.Voice
	if a.DB.First(&v, "id = ?", c.VoiceID).Error != nil {
		return nil
	}
	if approve {
		a.DB.Model(&v).Updates(map[string]any{"status": model.VoiceTraining, "status_note": "授权已通过，正在训练"})
		var samples []string
		if v.SampleAssetID != "" {
			samples = []string{v.SampleAssetID}
		}
		a.enqueueClone(&v, v.CreatedBy, map[string]any{"samples": toAny(samples)})
		a.Notify(v.CreatedBy, v.StudioID, "voice_review", "克隆授权已通过", "「"+v.Name+"」已开始训练", "/voices")
	} else {
		a.DB.Model(&v).Updates(map[string]any{"status": model.VoiceFailed, "status_note": "授权未通过：" + note})
		a.Notify(v.CreatedBy, v.StudioID, "voice_review", "克隆授权未通过", note, "/voices")
	}
	return nil
}

func toAny(xs []string) []any {
	out := make([]any, len(xs))
	for i, s := range xs {
		out[i] = s
	}
	return out
}

// DeleteVoice removes a voice (and the remote voice when supported); characters lose the binding.
func (a *App) DeleteVoice(ctx context.Context, v *model.Voice) error {
	if (v.Origin == model.VoiceCloned || v.Origin == model.VoiceDesigned) && v.ProviderVoiceID != "" {
		var shared int64
		a.DB.Model(&model.Voice{}).Where("id <> ? AND provider IN ? AND provider_voice_id = ?", v.ID, providerNames(v.Provider), v.ProviderVoiceID).Count(&shared)
		if shared == 0 {
			if p, _, err := a.TTSFor(v); err == nil {
				if c, ok := p.(tts.VoiceCloner); ok {
					c.DeleteVoice(ctx, v.ProviderVoiceID)
				}
			}
		}
	}
	return a.DB.Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.Character{}).Where("voice_id = ?", v.ID).Update("voice_id", "")
		tx.Where("voice_id = ?", v.ID).Delete(&model.VoiceFavorite{})
		tx.Where("voice_id = ?", v.ID).Delete(&model.VoiceListing{})
		return tx.Delete(v).Error
	})
}

// SyncProviderVoices imports a platform's preset voices into the official library (unlisted).
func (a *App) SyncProviderVoices(ctx context.Context, acc *model.ProviderAccount) (int, error) {
	p, err := tts.New(a.ttsAccount(acc))
	if err != nil {
		return 0, err
	}
	lister, ok := p.(tts.VoiceLister)
	if !ok {
		return 0, Bad("该平台不支持同步音色列表")
	}
	list, err := lister.ListVoices(ctx)
	if err != nil {
		return 0, err
	}
	provider, accID := acc.Provider, acc.ID
	switch provider {
	case "aliyun", "qwen":
		provider, accID = "qwen", "" // Qianwen voices route to any enabled Qianwen account
	case "minimax":
		accID = "" // system voices work on every MiniMax account
	}
	defer a.InvalidateTTSProviders()
	return a.UpsertCatalogVoices(provider, accID, list)
}

// ExpireVoices marks expired clones and warns owners three days ahead.
func (a *App) ExpireVoices() {
	t := now()
	a.DB.Model(&model.Voice{}).Where("expires_at IS NOT NULL AND expires_at < ? AND status = ?", t, model.VoiceReady).Updates(map[string]any{"status": model.VoiceExpired, "status_note": "平台侧音色已过期，请重新克隆"})
	var soon []model.Voice
	a.DB.Where("expires_at IS NOT NULL AND expires_at BETWEEN ? AND ? AND status = ?", t.Add(71*time.Hour), t.Add(72*time.Hour), model.VoiceReady).Find(&soon)
	for _, v := range soon {
		msg := "「" + v.Name + "」将在 3 天后过期，部分平台要求定期使用或重新克隆"
		if v.Origin == model.VoiceCloned && platformCaps(v.Provider).CloneExpiresUnused {
			msg = "「" + v.Name + "」3 天后会被语音平台删除。用它生成任意一句配音即可长期保留"
		}
		a.Notify(firstNonEmpty(v.OwnerUserID, v.CreatedBy), v.StudioID, "voice_expiring", "复刻音色即将过期", msg, "/voices")
	}
}

// verifyVoiceprint checks the consent recording and the sample are the same speaker, when a platform supports it.
func (a *App) verifyVoiceprint(ctx context.Context, studioID, provider, evidenceID, sampleID, note string) (bool, string) {
	p, _, err := a.TTSAccount(provider, studioID)
	if err != nil {
		return true, note
	}
	v, ok := p.(tts.VoiceprintVerifier)
	if !ok {
		return true, note
	}
	pa, _, err1 := a.AssetPath(evidenceID)
	pb, _, err2 := a.AssetPath(sampleID)
	if err1 != nil || err2 != nil {
		return true, note
	}
	score, err := v.CompareSpeakers(ctx, pa, pb)
	if err != nil {
		return true, note + "；声纹比对暂不可用"
	}
	if score < 0.7 {
		return false, fmt.Sprintf("验证录音与样本不像同一个人（声纹相似度 %.0f%%）", score*100)
	}
	return true, note + fmt.Sprintf("；声纹比对通过（%.0f%%）", score*100)
}
