package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

var pipelineStages = []struct {
	Key    string
	Name   string
	Weight float64
}{
	{"characters", "角色分析", 0.08},
	{"casting", "AI 选角", 0.04},
	{"script", "生成画本", 0.25},
	{"tts", "AI 配音", 0.40},
	{"post", "后期设置", 0.03},
	{"render", "合成成品", 0.20},
}

// EstimatePipeline returns a credit and time estimate for one-click production.
func (a *App) EstimatePipeline(b *model.Book, cfg model.PipelineConfig) map[string]any {
	words := b.WordCount
	if len(cfg.ChapterIDs) > 0 {
		var w int64
		a.DB.Model(&model.Chapter{}).Where("id IN ?", cfg.ChapterIDs).Select("COALESCE(SUM(word_count),0)").Scan(&w)
		words = int(w)
	}
	tier := firstNonEmpty(cfg.Tier, model.TierStandard)
	policy := a.CastingPolicy(tier)
	script := a.Estimate(FeatScript, "", float64(words))
	chars := a.Estimate(FeatCharacter, "", float64(min(words, 72000)))
	// most text is narration; weight narrator tier at 70%
	tts := int64(float64(a.Estimate(FeatTTS, policy.Narrator, float64(words)))*0.7 + float64(a.Estimate(FeatTTS, policy.Lead, float64(words)))*0.3)
	rate := b.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	total := script + chars + tts
	return map[string]any{
		"words": words, "credits": total, "breakdown": map[string]int64{"characters": chars, "script": script, "tts": tts},
		"audioMs": int64(words) * 60000 / int64(rate), "processingMinutes": max(2, words/2500),
		"available": a.Balance(b.StudioID).Available,
	}
}

// CreditGuide translates credits into something people can picture: how many credits a typical
// multi-voice audiobook costs per 10,000 characters (character analysis + script + voicing), per tier.
func (a *App) CreditGuide() map[string]any {
	const words = 10000
	per := map[string]int64{}
	for _, tier := range []string{model.TierEconomy, model.TierStandard, model.TierPremium} {
		policy := a.CastingPolicy(tier)
		tts := int64(float64(a.Estimate(FeatTTS, policy.Narrator, words))*0.7 + float64(a.Estimate(FeatTTS, policy.Lead, words))*0.3)
		per[tier] = a.Estimate(FeatScript, "", words) + a.Estimate(FeatCharacter, "", words) + tts
	}
	return map[string]any{"per10kChars": per, "speechRate": a.Site().SpeechRate}
}

func (a *App) StartPipeline(x *Access, cfg model.PipelineConfig, apiKeyID string, priority int) (*model.PipelineRun, error) {
	if err := need(x, PermManage); err != nil {
		if err2 := need(x, PermGenerate); err2 != nil {
			return nil, err
		}
	}
	var active int64
	a.DB.Model(&model.PipelineRun{}).Where("book_id = ? AND status IN ?", x.Book.ID, []string{"queued", "running", "paused", "waiting"}).Count(&active)
	if active > 0 {
		return nil, Conflict("这本书已有正在进行的一键成书任务")
	}
	if _, err := a.LLMFor(FeatScript, cfg.Tier, x.StudioID); err != nil {
		return nil, err
	}
	est := a.EstimatePipeline(x.Book, cfg)
	if need := est["credits"].(int64); need > est["available"].(int64) {
		return nil, PaymentRequired(fmt.Sprintf("积分不足：预计需要 %d 积分，当前可用 %d", need, est["available"]), map[string]any{"need": need, "available": est["available"]})
	}
	if cfg.Tier == "" {
		cfg.Tier = model.TierStandard
	}
	if cfg.Mode == "" {
		cfg.Mode = x.Book.Mode
	}
	run := &model.PipelineRun{StudioID: x.StudioID, BookID: x.Book.ID, Status: "queued", Stage: "characters", EstCredits: est["credits"].(int64), Priority: priority, CreatedBy: x.User.ID, APIKeyID: apiKeyID, Stages: model.EmptyMap(), ChapterStates: model.EmptyMap()}
	run.Config.V = cfg
	if err := a.DB.Create(run).Error; err != nil {
		return nil, err
	}
	// apply book-level choices
	up := map[string]any{"status": model.BookProducing, "mode": cfg.Mode, "pipeline_run_id": run.ID}
	s := x.Book.Settings.V
	s.QualityTier = cfg.Tier
	if cfg.IntroTemplateID != "" {
		s.IntroTemplateID = cfg.IntroTemplateID
	}
	if cfg.OutroTemplateID != "" {
		s.OutroTemplateID = cfg.OutroTemplateID
	}
	if cfg.Preset != "" {
		s.LoudnessPreset = cfg.Preset
	}
	s.RemoveSpeechTags = cfg.Mode == model.ModeDrama
	s.AutoQC = true
	up["settings"] = model.JSON[model.BookSettings]{V: s}
	a.DB.Model(x.Book).Updates(up)
	j := &model.Job{Type: "pipeline", Lane: jobs.LanePipeline, StudioID: x.StudioID, BookID: x.Book.ID, RefType: "pipeline", RefID: run.ID, Title: "一键成书 · 《" + x.Book.Title + "》", CreatedBy: x.User.ID, Priority: priority, MaxAttempts: 1}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	a.DB.Model(run).Update("job_id", j.ID)
	a.Activity(x.StudioID, x.Book.ID, x.User.ID, "pipeline.start", "book", x.Book.ID, "启动了一键成书")
	return run, nil
}

var errPipelineStop = errors.New("pipeline stopped")

func (a *App) jobPipeline(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var run model.PipelineRun
	if err := a.DB.First(&run, "id = ?", j.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var b model.Book
	if err := a.DB.First(&b, "id = ?", run.BookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	cfg := run.Config.V
	stages := run.Stages.V
	if stages == nil {
		stages = map[string]any{}
	}
	chStates := run.ChapterStates.V
	if chStates == nil {
		chStates = map[string]any{}
	}
	save := func(extra map[string]any) {
		up := map[string]any{"stages": model.Map{V: stages}, "chapter_states": model.Map{V: chStates}, "progress": jc.Job.Progress}
		for k, v := range extra {
			up[k] = v
		}
		a.DB.Model(&run).Updates(up)
		a.emit(b.StudioID, eventBook(b.ID, "pipeline"))
	}
	if run.StartedAt == nil {
		t := now()
		run.StartedAt = &t
	}
	save(map[string]any{"status": "running", "started_at": run.StartedAt, "error": ""})
	// one hold for the whole run
	if j.CreditsFrozen == 0 && j.Attempts <= 1 {
		if err := a.Hold(j.ID, b.StudioID, run.EstCredits); err != nil {
			save(map[string]any{"status": "failed", "error": err.Error()})
			return jobs.Permanent(err)
		}
	}
	checkpoint := func() error {
		var cur model.PipelineRun
		a.DB.Select("status").First(&cur, "id = ?", run.ID)
		switch cur.Status {
		case "paused":
			return jobs.Pause()
		case "canceled":
			return context.Canceled
		}
		return ctx.Err()
	}
	base := 0.0
	stageProgress := func(key string, p float64, text string) {
		for _, st := range pipelineStages {
			if st.Key == key {
				jc.Progress(base+st.Weight*p, text)
				stages[key] = map[string]any{"status": "running", "progress": p}
				return
			}
		}
	}
	doneStage := func(key string) {
		for _, st := range pipelineStages {
			if st.Key == key {
				base += st.Weight
			}
		}
		stages[key] = map[string]any{"status": "done", "progress": 1}
		save(nil)
	}
	stageDone := func(key string) bool {
		m, ok := stages[key].(map[string]any)
		return ok && m["status"] == "done"
	}
	x := &Access{User: &model.User{Base: model.Base{ID: run.CreatedBy}}, StudioID: b.StudioID, Role: model.RoleOwner, Book: &b, perms: map[Perm]bool{}}
	for _, p := range allPerms {
		x.perms[p] = true
	}
	var u model.User
	if a.DB.First(&u, "id = ?", run.CreatedBy).Error == nil {
		x.User = &u
	}
	chapters := func() []model.Chapter {
		var chs []model.Chapter
		q := a.DB.Where("book_id = ? AND included = ?", b.ID, true).Order("\"index\" asc")
		if len(cfg.ChapterIDs) > 0 {
			q = q.Where("id IN ?", cfg.ChapterIDs)
		}
		q.Find(&chs)
		return chs
	}
	chState := func(id, key string, v any) {
		m, _ := chStates[id].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m[key] = v
		chStates[id] = m
	}
	run1 := func() error {
		// 1) characters
		if !stageDone("characters") {
			a.DB.Model(&run).Update("stage", "characters")
			var n int64
			a.DB.Model(&model.Character{}).Where("book_id = ? AND kind <> ? AND candidate = ?", b.ID, model.CharNarrator, false).Count(&n)
			if n == 0 && cfg.Mode != model.ModeSingle {
				h, err := a.LLMFor(FeatCharacter, cfg.Tier, b.StudioID)
				if err != nil {
					return err
				}
				infos, chars, err := a.AnalyzeCharacters(ctx, h, chapters(), func(p float64, t string) { stageProgress("characters", p, t) })
				if err != nil {
					return err
				}
				a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: run.CreatedBy, BookID: b.ID, JobID: j.ID, Feature: FeatCharacter, Quantity: float64(chars), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK})
				a.SaveCharacterInfos(&b, infos)
			}
			doneStage("characters")
		} else {
			base += pipelineStages[0].Weight
		}
		if err := checkpoint(); err != nil {
			return err
		}
		// 2) casting
		if !stageDone("casting") {
			a.DB.Model(&run).Update("stage", "casting")
			stageProgress("casting", 0.2, "正在为角色挑选音色")
			if cfg.NarratorVoiceID != "" {
				a.DB.Model(&model.Character{}).Where("book_id = ? AND kind = ? AND voice_id = ''", b.ID, model.CharNarrator).Update("voice_id", cfg.NarratorVoiceID)
			}
			if _, err := a.Cast(ctx, x, nil, true, nil); err != nil {
				return err
			}
			if cfg.Mode == model.ModeSingle {
				var narr model.Character
				if a.DB.First(&narr, "book_id = ? AND kind = ?", b.ID, model.CharNarrator).Error == nil && narr.VoiceID != "" {
					a.DB.Model(&model.Character{}).Where("book_id = ?", b.ID).Update("voice_id", narr.VoiceID)
				}
			}
			stages["casting"] = map[string]any{"status": "done", "progress": 1}
			base += pipelineStages[1].Weight
			if cfg.PauseAfterCast {
				m := stages["casting"].(map[string]any)
				if m["confirmed"] != true {
					stages["casting"] = map[string]any{"status": "done", "progress": 1, "awaiting": true}
					save(map[string]any{"status": "waiting", "stage": "casting"})
					a.Notify(run.CreatedBy, b.StudioID, "pipeline_waiting", "《"+b.Title+"》选角完成，等待你确认", "确认后将自动继续生成画本与配音", "/books/"+b.ID+"/characters")
					return jobs.Pause()
				}
			}
			save(nil)
		} else {
			base += pipelineStages[1].Weight
		}
		if err := checkpoint(); err != nil {
			return err
		}
		// 3) scripts
		if !stageDone("script") {
			a.DB.Model(&run).Update("stage", "script")
			chs := chapters()
			for i, ch := range chs {
				if err := checkpoint(); err != nil {
					return err
				}
				if ch.CurrentScriptID != "" && !ch.ScriptStale {
					chState(ch.ID, "script", "done")
					continue
				}
				sub := &model.Job{ID: j.ID, StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: run.CreatedBy, Payload: model.Map{V: map[string]any{"mode": "ai"}}}
				rep := &subReporter{parent: jc, base: base + pipelineStages[2].Weight*float64(i)/float64(len(chs)), span: pipelineStages[2].Weight / float64(len(chs)), prefix: ch.Title}
				if err := a.scriptParse(ctx, sub, rep, true); err != nil {
					chState(ch.ID, "script", "failed")
					return fmt.Errorf("「%s」画本生成失败：%w", ch.Title, err)
				}
				chState(ch.ID, "script", "done")
				save(nil)
			}
			doneStage("script")
		} else {
			base += pipelineStages[2].Weight
		}
		// cast candidate characters discovered while scripting
		var cands []string
		a.DB.Model(&model.Character{}).Where("book_id = ? AND voice_id = '' AND mode = 'ai'", b.ID).Pluck("id", &cands)
		if len(cands) > 0 {
			a.DB.Model(&model.Character{}).Where("id IN ?", cands).Update("candidate", false)
			a.Cast(ctx, x, cands, true, nil)
		}
		// 4) voice
		if !stageDone("tts") {
			a.DB.Model(&run).Update("stage", "tts")
			chs := chapters()
			for i, ch := range chs {
				if err := checkpoint(); err != nil {
					return err
				}
				targets, err := a.generationTargets(&ch, GenerateRequest{Scope: "missing"}, nil)
				if err != nil {
					return err
				}
				if len(targets) > 0 {
					sub := &model.Job{ID: j.ID, StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: run.CreatedBy, Payload: model.Map{V: map[string]any{"segmentIds": toAny(targetIDs(targets))}}}
					rep := &subReporter{parent: jc, base: base + pipelineStages[3].Weight*float64(i)/float64(len(chs)), span: pipelineStages[3].Weight / float64(len(chs)), prefix: ch.Title}
					if err := a.ttsSegments(ctx, sub, rep, true); err != nil && !errors.Is(err, jobs.ErrPermanent) {
						return err
					}
				}
				var left int64
				a.DB.Model(&model.Segment{}).Where("script_id = ? AND removed = ? AND type NOT IN ? AND audio_status <> ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause}, model.AudioReady).Count(&left)
				chState(ch.ID, "tts", map[string]any{"left": left})
				save(nil)
				if left > 0 {
					return fmt.Errorf("「%s」有 %d 句配音失败，请进入制作台查看原因后重试", ch.Title, left)
				}
			}
			doneStage("tts")
		} else {
			base += pipelineStages[3].Weight
		}
		// 5) post: background music + templates
		if !stageDone("post") {
			a.DB.Model(&run).Update("stage", "post")
			if cfg.BGM {
				bgm := cfg.BGMAssetID
				if bgm == "" {
					bgm = a.pickBGM(&b)
				}
				if bgm != "" {
					for _, ch := range chapters() {
						m := a.mixFor(&ch)
						for ti := range m.Tracks.V {
							if m.Tracks.V[ti].Kind == "bgm" && len(m.Tracks.V[ti].Clips) == 0 {
								m.Tracks.V[ti].Clips = append(m.Tracks.V[ti].Clips, model.Clip{ID: model.NewID(), AssetID: bgm, Name: "背景音乐", Fill: true, Loop: true, Volume: 0, FadeInMs: 1500, FadeOutMs: 3000})
							}
						}
						a.DB.Model(m).Update("tracks", m.Tracks)
					}
				}
			}
			if cfg.IntroTemplateID != "" || cfg.OutroTemplateID != "" {
				a.DB.Model(&model.Mix{}).Where("chapter_id IN (?)", a.DB.Model(&model.Chapter{}).Select("id").Where("book_id = ?", b.ID)).
					Updates(map[string]any{"intro": cfg.IntroTemplateID != "", "outro": cfg.OutroTemplateID != ""})
			}
			doneStage("post")
		} else {
			base += pipelineStages[4].Weight
		}
		// 6) render
		if !stageDone("render") {
			a.DB.Model(&run).Update("stage", "render")
			chs := chapters()
			for i, ch := range chs {
				if err := checkpoint(); err != nil {
					return err
				}
				if ch.RenderID != "" && !ch.RenderStale {
					chState(ch.ID, "render", "done")
					continue
				}
				sub := &model.Job{ID: j.ID, StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: run.CreatedBy, Payload: model.Map{V: map[string]any{"preset": cfg.Preset}}}
				rep := &subReporter{parent: jc, base: base + pipelineStages[5].Weight*float64(i)/float64(len(chs)), span: pipelineStages[5].Weight / float64(len(chs)), prefix: ch.Title}
				if err := a.renderChapter(ctx, sub, rep); err != nil {
					chState(ch.ID, "render", "failed")
					return fmt.Errorf("「%s」合成失败：%w", ch.Title, err)
				}
				chState(ch.ID, "render", "done")
				save(nil)
			}
			doneStage("render")
		}
		return nil
	}
	err := run1()
	var used model.Job
	a.DB.Select("credits_used").First(&used, "id = ?", j.ID)
	switch {
	case err == nil:
		t := now()
		save(map[string]any{"status": "completed", "finished_at": &t, "progress": 1, "used_credits": used.CreditsUsed, "stage": "done"})
		a.DB.Model(&b).Update("status", model.BookCompleted)
		a.Notify(run.CreatedBy, b.StudioID, "pipeline_done", "《"+b.Title+"》一键成书完成", "所有章节已生成成品，可以试听、精修或导出", "/books/"+b.ID)
		a.FireWebhook(b.StudioID, "pipeline.completed", map[string]any{"runId": run.ID, "bookId": b.ID})
		jc.Progress(1, "全部完成")
		return nil
	case errors.Is(err, jobs.Pause()):
		var cur model.PipelineRun
		a.DB.Select("status").First(&cur, "id = ?", run.ID)
		st := "paused"
		if cur.Status == "waiting" {
			st = "waiting"
		}
		save(map[string]any{"status": st, "used_credits": used.CreditsUsed})
		return err
	case errors.Is(err, context.Canceled):
		t := now()
		save(map[string]any{"status": "canceled", "finished_at": &t, "used_credits": used.CreditsUsed})
		return err
	default:
		t := now()
		save(map[string]any{"status": "failed", "finished_at": &t, "error": truncateStr(err.Error(), 900), "used_credits": used.CreditsUsed})
		a.FireWebhook(b.StudioID, "pipeline.failed", map[string]any{"runId": run.ID, "bookId": b.ID, "error": err.Error()})
		return jobs.Permanent(err)
	}
}

// pickBGM chooses an official background music cue matching the book's genre and tags. Books of the
// same genre get different tracks (spread by book ID), so a catalogue does not all sound the same.
func (a *App) pickBGM(b *model.Book) string {
	g := b.Genre + " " + strings.Join(b.Tags.V, " ") + " " + b.Title
	var prefs []string
	for _, p := range bgmPreferences {
		for _, w := range strings.Fields(p.words) {
			if strings.Contains(g, w) {
				prefs = append(prefs, p.tags...)
				break
			}
		}
	}
	prefs = append(prefs, "温馨治愈", "氛围空灵", "舒缓") // calm defaults
	for _, tag := range prefs {
		if id := a.libraryTrack(tag, b.ID); id != "" {
			return id
		}
	}
	var as model.Asset
	if a.DB.Where("kind = ? AND scope = ? AND listed = ?", model.AssetBGM, model.ScopeSystem, true).First(&as).Error == nil {
		return as.ID
	}
	return ""
}

func (a *App) PipelineAction(x *Access, run *model.PipelineRun, action string) error {
	switch action {
	case "pause":
		if run.Status != "running" && run.Status != "queued" {
			return Bad("当前状态不能暂停")
		}
		a.DB.Model(run).Update("status", "paused")
	case "resume", "confirm":
		if run.Status != "paused" && run.Status != "waiting" && run.Status != "failed" {
			return Bad("当前状态不能继续")
		}
		if action == "confirm" || run.Status == "waiting" {
			st := run.Stages.V
			if st == nil {
				st = map[string]any{}
			}
			st["casting"] = map[string]any{"status": "done", "progress": 1, "confirmed": true}
			a.DB.Model(run).Update("stages", model.Map{V: st})
		}
		a.DB.Model(run).Updates(map[string]any{"status": "running", "error": ""})
		if run.Status == "failed" {
			return a.Q.Retry(run.JobID)
		}
		return a.Q.Resume(run.JobID)
	case "cancel":
		a.DB.Model(run).Update("status", "canceled")
		a.Q.Cancel(run.JobID)
	default:
		return Bad("未知操作")
	}
	a.emit(x.StudioID, eventBook(run.BookID, "pipeline"))
	return nil
}

func (a *App) PipelineView(r *model.PipelineRun) map[string]any {
	var b model.Book
	a.DB.Select("id", "title", "cover_asset_id", "word_count").First(&b, "id = ?", r.BookID)
	elapsed := int64(0)
	if r.StartedAt != nil {
		end := now()
		if r.FinishedAt != nil {
			end = *r.FinishedAt
		}
		elapsed = end.Sub(*r.StartedAt).Milliseconds()
	}
	var j model.Job
	a.DB.Select("progress", "progress_text", "credits_used").First(&j, "id = ?", r.JobID)
	stages := []map[string]any{}
	for _, st := range pipelineStages {
		m, _ := r.Stages.V[st.Key].(map[string]any)
		status := "pending"
		var progress any = 0
		if m != nil {
			status, _ = m["status"].(string)
			progress = m["progress"]
		}
		if r.Stage == st.Key && status != "done" && (r.Status == "running" || r.Status == "queued") {
			status = "running"
		}
		stages = append(stages, map[string]any{"key": st.Key, "name": st.Name, "status": status, "progress": progress})
	}
	return map[string]any{
		"id": r.ID, "bookId": r.BookID, "book": map[string]any{"id": b.ID, "title": b.Title, "cover": a.AssetURLByID(b.CoverAssetID), "wordCount": b.WordCount},
		"status": r.Status, "stage": r.Stage, "progress": j.Progress, "progressText": j.ProgressText, "stages": stages, "chapterStates": r.ChapterStates.V,
		"config": r.Config.V, "estCredits": r.EstCredits, "usedCredits": max(r.UsedCredits, j.CreditsUsed), "error": r.Error, "elapsedMs": elapsed,
		"createdAt": r.CreatedAt, "startedAt": r.StartedAt, "finishedAt": r.FinishedAt, "jobId": r.JobID, "priority": r.Priority,
	}
}

var _ = time.Second
