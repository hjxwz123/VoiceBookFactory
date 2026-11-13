package core

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"voicebook/internal/ai/tts"
	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/secure"
	"voicebook/internal/text"

	"gorm.io/gorm"
)

// ======================= Open API keys =======================

func (a *App) CreateAPIKey(x *Access, name string, scopes, ips []string, rpm int) (*model.APIKey, string, error) {
	if !x.IsManager() {
		return nil, "", Forbidden("只有所有者或管理员可以管理 API Key")
	}
	if err := a.RequireFeature(x.StudioID, model.FeatOpenAPI, "开放平台"); err != nil {
		return nil, "", err
	}
	scopes = NormalizeScopes(scopes)
	if len(scopes) == 0 {
		scopes = []string{ScopeRead, ScopeProduce, ScopeTTS}
	}
	if rpm <= 0 {
		rpm = 60
	}
	raw := "vb_" + secure.Token(30)
	k := &model.APIKey{StudioID: x.StudioID, Name: firstNonEmpty(name, "默认 Key"), Prefix: raw[:10], KeyHash: secure.SHA256(raw), Scopes: model.Strings(scopes...), IPAllowlist: model.Strings(ips...), RateLimit: rpm, CreatedBy: x.User.ID}
	return k, raw, a.DB.Create(k).Error
}

// API key scopes (see the Open API guide).
const (
	ScopeRead    = "read"    // look up voices, books, runs and download finished audio
	ScopeProduce = "produce" // import manuscripts and start one-click production (uses credits)
	ScopeTTS     = "tts"     // voice single passages (uses credits)
)

// legacyScopes are the names the key dialog offered before 0.1.4: what each was meant to allow.
var legacyScopes = map[string][]string{"write": {ScopeProduce}, "generate": {ScopeProduce, ScopeTTS}}

// NormalizeScopes keeps the known scopes, in a fixed order, translating the older names.
func NormalizeScopes(in []string) []string {
	want := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if alias, ok := legacyScopes[s]; ok {
			for _, x := range alias {
				want[x] = true
			}
			continue
		}
		want[s] = true
	}
	var out []string
	for _, s := range []string{ScopeRead, ScopeProduce, ScopeTTS} {
		if want[s] {
			out = append(out, s)
		}
	}
	return out
}

// HasScope reports whether a key may use an endpoint that needs scope (older scope names count).
func HasScope(k *model.APIKey, scope string) bool {
	return contains(NormalizeScopes(k.Scopes.V), scope)
}

// AuthAPIKey validates an API key and returns studio-owner level access.
func (a *App) AuthAPIKey(raw, ip string) (*model.APIKey, *Access, error) {
	var k model.APIKey
	if raw == "" || a.DB.First(&k, "key_hash = ?", secure.SHA256(raw)).Error != nil || k.Disabled {
		return nil, nil, Unauthorized("API Key 无效或已停用")
	}
	if len(k.IPAllowlist.V) > 0 && !contains(k.IPAllowlist.V, ip) {
		return nil, nil, Forbidden("当前 IP 不在白名单中")
	}
	if !a.Allow("apikey:"+k.ID, k.RateLimit) {
		return nil, nil, TooMany("请求过于频繁，超过了该 Key 的限流配置")
	}
	var owner model.User
	var st model.Studio
	if a.DB.First(&st, "id = ?", k.StudioID).Error != nil || a.DB.First(&owner, "id = ?", st.OwnerID).Error != nil {
		return nil, nil, Unauthorized("API Key 所属工作室不可用")
	}
	t := now()
	a.DB.Model(&k).Updates(map[string]any{"last_used_at": &t, "calls": k.Calls + 1})
	x := &Access{User: &owner, StudioID: k.StudioID, Role: model.RoleOwner, perms: map[Perm]bool{}}
	return &k, x, nil
}

// ======================= Webhooks =======================

var WebhookEvents = []string{"pipeline.completed", "pipeline.failed", "export.completed", "chapter.rendered", "voice.ready", "ping"}

func (a *App) FireWebhook(studioID, event string, payload map[string]any) {
	var hooks []model.Webhook
	a.DB.Where("studio_id = ? AND enabled = ?", studioID, true).Find(&hooks)
	for _, h := range hooks {
		if event != "ping" && !contains(h.Events.V, event) {
			continue
		}
		body, _ := json.Marshal(map[string]any{"event": event, "id": model.NewID(), "createdAt": now(), "data": payload})
		d := model.WebhookDelivery{WebhookID: h.ID, StudioID: studioID, Event: event, Payload: string(body), Status: "pending"}
		if a.DB.Create(&d).Error != nil {
			continue
		}
		a.Q.Enqueue(&model.Job{Type: "webhook", StudioID: studioID, RefType: "webhook_delivery", RefID: d.ID, Title: "Webhook " + event, MaxAttempts: 5})
	}
}

func (a *App) jobWebhook(ctx context.Context, jc *jobs.Context) error {
	var d model.WebhookDelivery
	if err := a.DB.First(&d, "id = ?", jc.Job.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var h model.Webhook
	if err := a.DB.First(&h, "id = ?", d.WebhookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	ts := fmt.Sprint(time.Now().Unix())
	sig := secure.HMAC([]byte(h.Secret), ts+"."+d.Payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader([]byte(d.Payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Voicebook-Webhook/1.0")
	req.Header.Set("X-Voicebook-Event", d.Event)
	req.Header.Set("X-Voicebook-Signature", "t="+ts+",v1="+sig)
	start := time.Now()
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	up := map[string]any{"attempts": d.Attempts + 1, "duration_ms": time.Since(start).Milliseconds()}
	if err != nil {
		up["status"], up["error"] = "failed", truncateStr(err.Error(), 480)
		a.DB.Model(&d).Updates(up)
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	up["response_code"] = resp.StatusCode
	if resp.StatusCode >= 300 {
		up["status"], up["error"] = "failed", fmt.Sprintf("HTTP %d", resp.StatusCode)
		a.DB.Model(&d).Updates(up)
		return fmt.Errorf("webhook 返回 HTTP %d", resp.StatusCode)
	}
	up["status"], up["error"] = "success", ""
	a.DB.Model(&d).Updates(up)
	return nil
}

// ======================= Quick TTS =======================

func (a *App) StartQuickTTS(x *Access, title string, lines []model.QuickLine) (*model.QuickTTS, error) {
	total := 0
	var clean []model.QuickLine
	for _, l := range lines {
		l.Text = strings.TrimSpace(l.Text)
		if l.Text == "" {
			continue
		}
		if l.VoiceID == "" {
			return nil, Bad("请为每一行选择音色")
		}
		total += utf8.RuneCountInString(l.Text)
		clean = append(clean, l)
	}
	if len(clean) == 0 {
		return nil, Bad("请输入要配音的文本")
	}
	if total > 5000 {
		return nil, Bad("快速配音单次最多 5000 字，更长的内容请创建图书")
	}
	q := &model.QuickTTS{UserID: x.User.ID, StudioID: x.StudioID, Title: firstNonEmpty(title, clip(clean[0].Text, 18)), Status: "running"}
	q.Lines.V = clean
	if err := a.DB.Create(q).Error; err != nil {
		return nil, err
	}
	return q, a.Q.Enqueue(&model.Job{Type: "quick_tts", StudioID: x.StudioID, RefType: "quick", RefID: q.ID, Title: "快速配音 · " + q.Title, CreatedBy: x.User.ID, MaxAttempts: 1})
}

func (a *App) jobQuickTTS(ctx context.Context, jc *jobs.Context) error {
	var q model.QuickTTS
	if err := a.DB.First(&q, "id = ?", jc.Job.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	fail := func(err error) error {
		a.DB.Model(&q).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 250)})
		return jobs.Permanent(err)
	}
	var all []float32
	var credits int64
	lex := a.lexicon(q.StudioID, "")
	for i, l := range q.Lines.V {
		jc.Progress(float64(i)/float64(len(q.Lines.V)), fmt.Sprintf("正在生成第 %d/%d 行", i+1, len(q.Lines.V)))
		var v model.Voice
		if err := a.DB.First(&v, "id = ?", l.VoiceID).Error; err != nil {
			return fail(Bad("第 %d 行的音色不存在", i+1))
		}
		p, acc, err := a.TTSFor(&v)
		if err != nil {
			return fail(err)
		}
		txt, prons := BuildTTSInput(l.Text, nil, lex, tts.CapsFor(p, v.Model, v.ProviderVoiceID).Phoneme)
		out, err := p.Synthesize(ctx, tts.SynthesizeRequest{Text: txt, ProviderVoiceID: v.ProviderVoiceID, Model: v.Model, Emotion: l.Emotion, Speed: f64(l.Speed, firstNonZero(v.Defaults.V.Speed, 1)), Prons: prons, VoiceGender: v.Gender, VoiceAge: v.AgeGroup, VoiceSeed: v.ID, Intensity: 2})
		if err != nil {
			return fail(err)
		}
		a.voiceUsed(&v, p)
		c, err := a.ChargeCredits(Charge{StudioID: q.StudioID, UserID: q.UserID, JobID: jc.Job.ID, Feature: FeatQuick, Tier: v.PriceTier, Quantity: float64(utf8.RuneCountInString(txt)), Unit: "chars", Provider: acc.Provider, AccountID: acc.ID})
		if err != nil {
			return fail(err)
		}
		credits += c
		pcm, err := a.decodeSpeech(ctx, out)
		if err != nil {
			return fail(err)
		}
		all = append(all, pcm.Data...)
		all = append(all, make([]float32, audio.SampleRate*45/100)...)
	}
	dir, _ := a.Store.TempDir("quick")
	defer deleteDir(dir)
	wav := dir + "/quick.wav"
	audio.WriteWAVFile(wav, &audio.PCM{Rate: audio.SampleRate, Channels: 1, Data: all})
	mp3 := dir + "/quick.mp3"
	if err := a.FF.Encode(ctx, wav, mp3, audio.EncodeOptions{Format: "mp3", Bitrate: 160, TargetI: -16, TargetTP: -1.5}); err != nil {
		return fail(err)
	}
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: q.StudioID, Kind: model.AssetPreview, Name: q.Title, Ext: "mp3", Origin: "generated", CreatedBy: q.UserID}, mp3)
	if err != nil {
		return fail(err)
	}
	a.DB.Model(&q).Updates(map[string]any{"status": "ready", "asset_id": asset.ID, "duration_ms": asset.DurationMs, "credits": credits})
	jc.Progress(1, "快速配音已完成")
	return nil
}

func (a *App) decodeSpeech(ctx context.Context, au *tts.Audio) (*audio.PCM, error) {
	if au.Format == "wav" {
		if p, err := audio.DecodeWAV(bytes.NewReader(au.Data)); err == nil {
			p = audio.TrimSilence(audio.Resample(audio.ToMono(p), audio.SampleRate), -48, 40)
			audio.NormalizeRMS(p, -20, -2)
			return p, nil
		}
	}
	dir, _ := a.Store.TempDir("dec")
	defer deleteDir(dir)
	in := dir + "/in." + firstNonEmpty(au.Format, "mp3")
	if err := writeFile(in, au.Data); err != nil {
		return nil, err
	}
	p, err := a.FF.DecodePCM(ctx, in, audio.SampleRate, 1, "")
	if err != nil {
		return nil, err
	}
	p = audio.TrimSilence(p, -48, 40)
	audio.NormalizeRMS(p, -20, -2)
	return p, nil
}

// ======================= Moderation =======================

func (a *App) moderationSettings() ModerationSettings {
	var m ModerationSettings
	a.Setting("moderation", &m)
	return m
}

// ScanSensitive returns sensitive word hits in text.
func (a *App) ScanSensitive(content string) (block, review []string) {
	var words []model.SensitiveWord
	a.DB.Find(&words)
	for _, w := range words {
		if w.Word != "" && strings.Contains(content, w.Word) {
			if w.Level == "block" {
				block = append(block, w.Word)
			} else {
				review = append(review, w.Word)
			}
		}
	}
	return
}

// ModerateBook checks a newly imported book (sensitive words + optional LLM).
func (a *App) ModerateBook(ctx context.Context, bookID string) {
	set := a.moderationSettings()
	if !set.Enabled {
		return
	}
	var b model.Book
	if a.DB.First(&b, "id = ?", bookID).Error != nil {
		return
	}
	var chs []model.Chapter
	a.DB.Where("book_id = ?", bookID).Order("\"index\" asc").Find(&chs)
	var sb strings.Builder
	for _, c := range chs {
		sb.WriteString(c.Title + "\n" + c.SourceText + "\n")
	}
	full := sb.String()
	block, review := a.ScanSensitive(full)
	result, source := "pass", "words"
	hits := map[string]any{"block": block, "review": review}
	if len(block) > 0 {
		result = "block"
	} else if len(review) > 0 {
		result = "review"
	}
	if set.UseLLM && result == "pass" {
		if h, err := a.LLMFor(FeatModerate, "", b.StudioID); err == nil {
			sample := truncateStr(full, 6000)
			if v, err := a.ModerateLLM(ctx, h, sample); err == nil {
				source = "llm"
				hits["llm"] = v
				if v.Verdict != "pass" {
					result = v.Verdict
				}
				a.ChargeCredits(Charge{StudioID: b.StudioID, BookID: b.ID, Feature: FeatModerate, Quantity: float64(utf8.RuneCountInString(sample)), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider})
			}
		}
	}
	status := "auto"
	if result != "pass" {
		status = "pending"
	}
	excerpt := ""
	for _, w := range append(block, review...) {
		if i := strings.Index(full, w); i >= 0 {
			excerpt = clip(full[max(0, i-60):min(len(full), i+60)], 120)
			break
		}
	}
	a.DB.Create(&model.ModerationRecord{StudioID: b.StudioID, BookID: b.ID, TargetType: "book", TargetID: b.ID, Result: result, Source: source, Hits: model.Map{V: hits}, Status: status, Excerpt: excerpt})
	if result != "pass" && set.Mode == "pre" {
		a.DB.Model(&b).Update("status", model.BookReviewing)
		a.Notify(b.CreatedBy, b.StudioID, "moderation", "《"+b.Title+"》需要人工审核", "内容审核发现可能的问题，审核通过后才能继续制作", "/books/"+b.ID)
	}
}

func (a *App) ReviewModeration(adminID, recordID string, approve bool, note string) error {
	var r model.ModerationRecord
	if err := a.DB.First(&r, "id = ?", recordID).Error; err != nil {
		return NotFound("审核记录")
	}
	st := "rejected"
	if approve {
		st = "approved"
	}
	a.DB.Model(&r).Updates(map[string]any{"status": st, "reviewer_id": adminID, "note": note})
	var b model.Book
	if a.DB.First(&b, "id = ?", r.BookID).Error == nil {
		if approve && b.Status == model.BookReviewing {
			a.DB.Model(&b).Update("status", model.BookPreparing)
			a.Notify(b.CreatedBy, b.StudioID, "moderation", "《"+b.Title+"》审核已通过", "可以继续制作了", "/books/"+b.ID)
		} else if !approve {
			a.DB.Model(&b).Update("status", model.BookArchived)
			a.Notify(b.CreatedBy, b.StudioID, "moderation", "《"+b.Title+"》未通过内容审核", note, "/books/"+b.ID)
		}
	}
	return nil
}

// ======================= Distribution =======================

var ChannelKinds = map[string]string{"rss": "RSS 播客订阅", "webhook": "自定义推送（Webhook）", "ximalaya": "喜马拉雅", "qingting": "蜻蜓 FM", "lizhi": "荔枝", "spotify": "Spotify", "apple": "Apple Podcasts"}

func (a *App) PrePublishCheck(b *model.Book, chapterIDs []string, ch *model.Channel) []map[string]any {
	var checks []map[string]any
	add := func(key, name string, ok bool, tip string) {
		checks = append(checks, map[string]any{"key": key, "name": name, "ok": ok, "tip": tip})
	}
	var chs []model.Chapter
	q := a.DB.Where("book_id = ? AND included = ?", b.ID, true)
	if len(chapterIDs) > 0 {
		q = q.Where("id IN ?", chapterIDs)
	}
	q.Order("\"index\" asc").Find(&chs)

	// production: every step a professional QC sign-off looks at, in the order they happen
	scripted, rendered, passed, reviewed := 0, 0, 0, 0
	var scriptIDs, ids []string
	for _, c := range chs {
		ids = append(ids, c.ID)
		if c.CurrentScriptID != "" {
			scripted++
			scriptIDs = append(scriptIDs, c.CurrentScriptID)
		}
		if c.ReviewStatus == "passed" {
			reviewed++
		}
		if c.RenderID != "" && !c.RenderStale {
			rendered++
			var r model.Render
			if a.DB.First(&r, "id = ?", c.RenderID).Error == nil && r.Report.V["passed"] == true {
				passed++
			}
		}
	}
	add("script", fmt.Sprintf("画本（%d/%d 章）", scripted, len(chs)), len(chs) > 0 && scripted == len(chs), "还有章节没有生成画本")
	var missing, changed int64
	if len(scriptIDs) > 0 {
		lines := a.DB.Model(&model.Segment{}).Where("script_id IN ? AND removed = ? AND type NOT IN ?", scriptIDs, false, []string{model.SegSfx, model.SegPause})
		lines.Session(&gorm.Session{}).Where("audio_status = ?", model.AudioStale).Count(&changed)
		lines.Session(&gorm.Session{}).Where("audio_status NOT IN ?", []string{model.AudioReady, model.AudioStale}).Count(&missing)
	}
	audioTip := "在章节列表点「生成配音」"
	switch {
	case missing > 0 && changed > 0:
		audioTip = fmt.Sprintf("%d 句还没有音频，%d 句改动后还没重新生成", missing, changed)
	case missing > 0:
		audioTip = fmt.Sprintf("%d 句还没有音频", missing)
	case changed > 0:
		audioTip = fmt.Sprintf("%d 句改动后还没重新生成，生成配音时选「有改动的」", changed)
	}
	add("audio", "逐句配音", scripted > 0 && missing == 0 && changed == 0, audioTip)
	add("renders", fmt.Sprintf("成品音频（%d/%d 章）", rendered, len(chs)), len(chs) > 0 && rendered == len(chs), "未合成或有改动的章节需要重新合成")
	add("tech", fmt.Sprintf("技术指标（%d/%d 章达标）", passed, rendered), rendered > 0 && passed == rendered, "在制作台「成品」里查看不达标的检测项")
	add("review", fmt.Sprintf("审听（%d/%d 章通过）", reviewed, len(chs)), len(chs) > 0 && reviewed == len(chs), "逐章审听，没问题后点「审听通过」")
	var open int64
	if len(ids) > 0 {
		a.DB.Model(&model.Issue{}).Where("book_id = ? AND chapter_id IN ? AND status <> ?", b.ID, ids, model.IssueClosed).Count(&open)
	}
	add("issues", "返修工单", open == 0, fmt.Sprintf("还有 %d 个工单没有关闭", open))

	// listing metadata
	add("title", "书名与作者", b.Title != "" && b.Author != "", "在图书设置中补充作者信息")
	add("description", "作品简介", utf8.RuneCountInString(b.Description) >= 20, "简介至少 20 字，可以在概览页让 AI 撰写")
	add("cover", "封面", b.CoverAssetID != "", "在概览页上传或 AI 生成封面")
	cr := b.Copyright.V
	valid := cr.EndDate == "" || cr.EndDate >= now().Format("2006-01-02")
	add("copyright", "版权授权", cr.Type != "" && valid, "在图书设置中填写版权信息（原创作品选择「原创」）")
	var label AILabelSettings
	a.Setting("ai_label", &label)
	add("ai_label", "AI 生成内容标识", label.Implicit || label.Explicit || b.Settings.V.AILabel, "按法规要求，在图书设置里开启 AI 内容标识")
	if ch != nil {
		switch ch.Kind {
		case "ximalaya", "qingting", "lizhi", "spotify", "apple":
			add("channel", ChannelKinds[ch.Kind]+" 接口", false, "该平台需要官方开放平台授权，适配器为预留接口；可先使用 RSS 或手动上传导出文件")
		}
	}
	return checks
}

func (a *App) CreatePublication(x *Access, channelID string, chapterIDs []string, startAt time.Time, intervalH, perBatch int) (*model.Publication, error) {
	if err := need(x, PermExport); err != nil {
		return nil, err
	}
	if err := a.RequireFeature(x.StudioID, model.FeatDistribute, "多平台分发"); err != nil {
		return nil, err
	}
	var ch model.Channel
	if err := a.DB.First(&ch, "id = ? AND studio_id = ?", channelID, x.StudioID).Error; err != nil {
		return nil, NotFound("发布渠道")
	}
	var chs []model.Chapter
	q := a.DB.Where("book_id = ? AND included = ? AND render_id <> ''", x.Book.ID, true).Order("\"index\" asc")
	if len(chapterIDs) > 0 {
		q = q.Where("id IN ?", chapterIDs)
	}
	q.Find(&chs)
	if len(chs) == 0 {
		return nil, Bad("没有已合成的章节可以发布")
	}
	if startAt.IsZero() {
		startAt = now()
	}
	if perBatch <= 0 {
		perBatch = len(chs)
	}
	p := &model.Publication{StudioID: x.StudioID, BookID: x.Book.ID, ChannelID: ch.ID, Status: "scheduled", StartAt: startAt, IntervalH: intervalH, PerBatch: perBatch, CreatedBy: x.User.ID, Checks: model.Map{V: map[string]any{"items": a.PrePublishCheck(x.Book, chapterIDs, &ch)}}}
	var ids []string
	for _, c := range chs {
		ids = append(ids, c.ID)
	}
	p.ChapterIDs = model.Strings(ids...)
	if err := a.DB.Create(p).Error; err != nil {
		return nil, err
	}
	for i, c := range chs {
		at := startAt.Add(time.Duration(i/perBatch*intervalH) * time.Hour)
		a.DB.Create(&model.PublishedEpisode{PublicationID: p.ID, BookID: x.Book.ID, ChapterID: c.ID, ChannelID: ch.ID, RenderID: c.RenderID, Status: "scheduled", PublishAt: at, Title: c.Title, Index: c.Index})
	}
	a.Activity(x.StudioID, x.Book.ID, x.User.ID, "publish", "book", x.Book.ID, fmt.Sprintf("创建了发布计划：%s，%d 集", ch.Name, len(chs)))
	return p, nil
}

// PublishDue publishes scheduled episodes whose time has come (cron).
func (a *App) PublishDue(ctx context.Context) {
	var eps []model.PublishedEpisode
	a.DB.Where("status = ? AND publish_at <= ?", "scheduled", now()).Limit(50).Find(&eps)
	for _, ep := range eps {
		var ch model.Channel
		if a.DB.First(&ch, "id = ?", ep.ChannelID).Error != nil {
			continue
		}
		err := a.publishEpisode(ctx, &ch, &ep)
		t := now()
		if err != nil {
			a.DB.Model(&ep).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 480)})
		} else {
			a.DB.Model(&ep).Updates(map[string]any{"status": "published", "published_at": &t, "error": ""})
		}
		var left int64
		a.DB.Model(&model.PublishedEpisode{}).Where("publication_id = ? AND status = ?", ep.PublicationID, "scheduled").Count(&left)
		if left == 0 {
			var failed int64
			a.DB.Model(&model.PublishedEpisode{}).Where("publication_id = ? AND status = ?", ep.PublicationID, "failed").Count(&failed)
			st := "published"
			if failed > 0 {
				st = "failed"
			}
			a.DB.Model(&model.Publication{}).Where("id = ?", ep.PublicationID).Update("status", st)
			a.DB.Model(&model.Book{}).Where("id = ?", ep.BookID).Update("status", model.BookPublished)
		} else {
			a.DB.Model(&model.Publication{}).Where("id = ?", ep.PublicationID).Update("status", "publishing")
		}
	}
}

func (a *App) episodeAudioURL(ep *model.PublishedEpisode) (string, int64, string) {
	var r model.Render
	if a.DB.First(&r, "id = ?", ep.RenderID).Error != nil {
		return "", 0, ""
	}
	var as model.Asset
	if a.DB.First(&as, "id = ?", r.AssetID).Error != nil {
		return "", 0, ""
	}
	return a.Cfg.PublicURL + a.Store.SignedPath(as.StorageKey, 10*365*24*time.Hour, ""), as.Size, as.Mime
}

func (a *App) publishEpisode(ctx context.Context, ch *model.Channel, ep *model.PublishedEpisode) error {
	switch ch.Kind {
	case "rss":
		return nil // feed lists published episodes
	case "webhook":
		url, _ := ch.Config.V["url"].(string)
		if url == "" {
			return fmt.Errorf("未配置推送地址")
		}
		var b model.Book
		a.DB.First(&b, "id = ?", ep.BookID)
		audioURL, size, mime := a.episodeAudioURL(ep)
		body, _ := json.Marshal(map[string]any{"book": map[string]any{"id": b.ID, "title": b.Title, "author": b.Author, "description": b.Description, "cover": a.Cfg.PublicURL + a.AssetURLByID(b.CoverAssetID)},
			"episode": map[string]any{"chapterId": ep.ChapterID, "index": ep.Index, "title": ep.Title, "audioUrl": audioURL, "size": size, "mime": mime}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if secret, _ := ch.Config.V["secret"].(string); secret != "" {
			ts := fmt.Sprint(time.Now().Unix())
			req.Header.Set("X-Voicebook-Signature", "t="+ts+",v1="+secure.HMAC([]byte(secret), ts+"."+string(body)))
		}
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("推送地址返回 HTTP %d", resp.StatusCode)
		}
		return nil
	default:
		return fmt.Errorf("%s 需要官方开放平台授权，适配器为预留接口", ChannelKinds[ch.Kind])
	}
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type rssItem struct {
	Title     string       `xml:"title"`
	GUID      string       `xml:"guid"`
	PubDate   string       `xml:"pubDate"`
	Enclosure rssEnclosure `xml:"enclosure"`
	Episode   int          `xml:"itunes:episode"`
	Duration  string       `xml:"itunes:duration"`
}

// RSSFeed renders a podcast RSS feed for a book published to an RSS channel.
func (a *App) RSSFeed(token, bookID string) ([]byte, error) {
	var ch model.Channel
	if a.DB.First(&ch, "token = ? AND kind = ? AND enabled = ?", token, "rss", true).Error != nil {
		return nil, NotFound("订阅源")
	}
	var b model.Book
	if a.DB.First(&b, "id = ? AND studio_id = ?", bookID, ch.StudioID).Error != nil {
		return nil, NotFound("作品")
	}
	var eps []model.PublishedEpisode
	a.DB.Where("channel_id = ? AND book_id = ? AND status = ?", ch.ID, b.ID, "published").Order("\"index\" asc").Find(&eps)
	var items []rssItem
	for _, ep := range eps {
		url, size, mime := a.episodeAudioURL(&ep)
		var c model.Chapter
		a.DB.Select("duration_ms").First(&c, "id = ?", ep.ChapterID)
		pub := ep.PublishAt
		if ep.PublishedAt != nil {
			pub = *ep.PublishedAt
		}
		items = append(items, rssItem{Title: ep.Title, GUID: ep.ID, PubDate: pub.Format(time.RFC1123Z), Enclosure: rssEnclosure{URL: url, Length: size, Type: firstNonEmpty(mime, "audio/mpeg")}, Episode: ep.Index, Duration: fmtDuration(c.DurationMs)})
	}
	type channelXML struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Language    string `xml:"language"`
		Author      string `xml:"itunes:author"`
		Image       struct {
			Href string `xml:"href,attr"`
		} `xml:"itunes:image"`
		Type  string    `xml:"itunes:type"`
		Items []rssItem `xml:"item"`
	}
	type rss struct {
		XMLName xml.Name   `xml:"rss"`
		Version string     `xml:"version,attr"`
		Itunes  string     `xml:"xmlns:itunes,attr"`
		Channel channelXML `xml:"channel"`
	}
	feed := rss{Version: "2.0", Itunes: "http://www.itunes.com/dtds/podcast-1.0.dtd"}
	feed.Channel = channelXML{Title: b.Title, Link: a.Cfg.PublicURL, Description: firstNonEmpty(b.Description, b.Title), Language: b.Language, Author: b.Author, Type: "serial", Items: items}
	if b.CoverAssetID != "" {
		feed.Channel.Image.Href = a.Cfg.PublicURL + a.AssetURLByID(b.CoverAssetID)
	}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

// ======================= Lexicon extraction =======================

func (a *App) ExtractBookLexicon(ctx context.Context, x *Access) ([]model.LexiconEntry, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	h, err := a.LLMFor(FeatLexicon, "", x.StudioID)
	if err != nil {
		return nil, err
	}
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND included = ?", x.Book.ID, true).Order("\"index\" asc").Limit(6).Find(&chs)
	var sb strings.Builder
	for _, c := range chs {
		if sb.Len() > 18000 {
			break
		}
		sb.WriteString(c.SourceText + "\n")
	}
	var names []string
	a.DB.Model(&model.Character{}).Where("book_id = ? AND kind <> ?", x.Book.ID, model.CharNarrator).Pluck("name", &names)
	terms, n, err := a.ExtractLexicon(ctx, h, truncateStr(sb.String(), 18000), names)
	if err != nil {
		return nil, err
	}
	a.ChargeCredits(Charge{StudioID: x.StudioID, UserID: x.User.ID, BookID: x.Book.ID, Feature: FeatLexicon, Quantity: float64(n), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK})
	var out []model.LexiconEntry
	for _, t := range terms {
		t.Term = strings.TrimSpace(t.Term)
		if t.Term == "" || utf8.RuneCountInString(t.Term) > 20 {
			continue
		}
		var exists int64
		a.DB.Model(&model.LexiconEntry{}).Where("book_id = ? AND term = ?", x.Book.ID, t.Term).Count(&exists)
		if exists > 0 {
			continue
		}
		e := model.LexiconEntry{Scope: "book", StudioID: x.StudioID, BookID: x.Book.ID, Term: t.Term, Pinyin: t.Pinyin, Note: t.Note, Status: "candidate", CreatedBy: x.User.ID}
		if a.DB.Create(&e).Error == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

// WordReadings returns, for each character of a word, its readings and the reading a TTS engine would
// most likely use in this word, so people can pick tones from a list instead of typing tone marks.
func WordReadings(word string) []map[string]any {
	word = strings.TrimSpace(word)
	if utf8.RuneCountInString(word) > 20 {
		word = string([]rune(word)[:20])
	}
	defaults := text.PinyinOf(word)
	out := []map[string]any{}
	k := 0
	for _, r := range word {
		item := map[string]any{"char": string(r), "readings": []string{}, "pinyin": ""}
		if unicode.Is(unicode.Han, r) {
			rs := text.Readings(string(r))
			if rs == nil {
				rs = []string{}
			}
			item["readings"] = rs
			if k < len(defaults) {
				item["pinyin"] = defaults[k]
			} else if len(rs) > 0 {
				item["pinyin"] = rs[0]
			}
			k++
		}
		out = append(out, item)
	}
	return out
}

// PinyinOptions returns reading candidates for one character.
func PinyinOptions(ch string) map[string]any {
	r := []rune(ch)
	if len(r) == 0 {
		return map[string]any{"readings": []string{}}
	}
	return map[string]any{"char": string(r[0]), "readings": text.Readings(string(r[0])), "polyphonic": text.IsPolyphonic(r[0])}
}
