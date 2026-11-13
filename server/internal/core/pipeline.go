package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// One-click production ("一键成书"). After the opening character analysis and the first casting, the
// chapters go through a production line: each chapter is scripted, its new characters cast, voiced,
// given music and rendered, and a few chapters move along side by side. The first chapters can be
// heard while the rest are still being made, a chapter that fails does not hold up the others, and
// credits are reserved chapter by chapter, so a long book needs only enough for the next chapters;
// when credits run out the run waits for a top-up instead of failing.

var pipelineStages = []struct {
	Key    string
	Name   string
	Weight float64
}{
	{"characters", "角色分析", 0.05},
	{"casting", "AI 选角", 0.03},
	{"script", "生成画本", 0.22},
	{"tts", "AI 配音", 0.42},
	{"post", "后期设置", 0.03},
	{"render", "合成成品", 0.25},
}

// chapter steps, in order (the last four stages above)
var chapterSteps = []string{"script", "tts", "post", "render"}

// pipelineParallel is how many chapters of one run are in production at the same time. Model and
// voice calls are further limited per provider account.
const pipelineParallel = 3

// pipelineGiveUp stops a run when this many chapters have failed since the last one that succeeded:
// something is wrong for every chapter (an account is down, credits are capped), not with one of them.
const pipelineGiveUp = 3

// pipelineChapters lists the chapters a run covers, in book order: the chosen ones, or every chapter
// marked for production.
func (a *App) pipelineChapters(bookID string, ids []string) []model.Chapter {
	q := a.DB.Where("book_id = ? AND included = ?", bookID, true).Order("\"index\" asc")
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	var chs []model.Chapter
	q.Find(&chs)
	return chs
}

func (a *App) ttsEstimate(policy model.CastingPolicy, words int) int64 {
	// most text is narration; weight narrator tier at 70%
	return int64(float64(a.Estimate(FeatTTS, policy.Narrator, float64(words)))*0.7 + float64(a.Estimate(FeatTTS, policy.Lead, float64(words)))*0.3)
}

// chapterCredits estimates what is left to pay for one chapter: its script (unless it has a current
// one) and the voicing of the lines that have no audio yet.
func (a *App) chapterCredits(ch *model.Chapter, policy model.CastingPolicy) int64 {
	if ch.CurrentScriptID == "" || ch.ScriptStale {
		return a.Estimate(FeatScript, "", float64(ch.WordCount)) + a.ttsEstimate(policy, ch.WordCount)
	}
	var texts []string
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND removed = ? AND type NOT IN ? AND audio_status <> ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause}, model.AudioReady).Pluck("text", &texts)
	n := 0
	for _, t := range texts {
		n += utf8.RuneCountInString(t)
	}
	return a.ttsEstimate(policy, n)
}

// EstimatePipeline returns a credit and time estimate for one-click production.
func (a *App) EstimatePipeline(b *model.Book, cfg model.PipelineConfig) map[string]any {
	chs := a.pipelineChapters(b.ID, cfg.ChapterIDs)
	words := 0
	for _, c := range chs {
		words += c.WordCount
	}
	tier := firstNonEmpty(cfg.Tier, model.TierStandard)
	policy := a.CastingPolicy(tier)
	mode := firstNonEmpty(cfg.Mode, b.Mode)
	script := a.Estimate(FeatScript, "", float64(words))
	var chars int64
	if mode != model.ModeSingle && !a.hasCast(b.ID) {
		chars = a.Estimate(FeatCharacter, "", float64(analysisChars(AnalyzeOpening, words)))
	}
	tts := a.ttsEstimate(policy, words)
	// credits are reserved chapter by chapter: starting needs the analysis and the first chapter
	first := chars
	if len(chs) > 0 {
		first += a.chapterCredits(&chs[0], policy)
	}
	rate := b.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	par := max(1, min(pipelineParallel, len(chs)))
	return map[string]any{
		"words": words, "chapters": len(chs), "credits": script + chars + tts, "minCredits": first,
		"breakdown": map[string]int64{"characters": chars, "script": script, "tts": tts},
		"audioMs":   int64(words) * 60000 / int64(rate), "processingMinutes": max(2, words/2500/par),
		"available": a.Balance(b.StudioID).Available,
	}
}

// hasCast reports whether a book already has characters besides the narrator.
func (a *App) hasCast(bookID string) bool {
	var n int64
	a.DB.Model(&model.Character{}).Where("book_id = ? AND kind <> ? AND candidate = ?", bookID, model.CharNarrator, false).Count(&n)
	return n > 0
}

// CreditGuide translates credits into something people can picture: how many credits a typical
// multi-voice audiobook costs per 10,000 characters (character analysis + script + voicing), per tier.
func (a *App) CreditGuide() map[string]any {
	const words = 10000
	per := map[string]int64{}
	for _, tier := range []string{model.TierEconomy, model.TierStandard, model.TierPremium} {
		policy := a.CastingPolicy(tier)
		per[tier] = a.Estimate(FeatScript, "", words) + a.Estimate(FeatCharacter, "", words) + a.ttsEstimate(policy, words)
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
	if len(cfg.ChapterIDs) > 0 {
		chs := a.pipelineChapters(x.Book.ID, cfg.ChapterIDs)
		if len(chs) == 0 {
			return nil, Bad("所选章节不在这本书里，或者没有纳入制作")
		}
		cfg.ChapterIDs = cfg.ChapterIDs[:0]
		for _, c := range chs {
			cfg.ChapterIDs = append(cfg.ChapterIDs, c.ID)
		}
	} else if len(a.pipelineChapters(x.Book.ID, nil)) == 0 {
		return nil, Bad("这本书没有纳入制作的章节")
	}
	if cfg.Tier == "" {
		cfg.Tier = model.TierStandard
	}
	if cfg.Mode == "" {
		cfg.Mode = x.Book.Mode
	}
	est := a.EstimatePipeline(x.Book, cfg)
	if need, avail := est["minCredits"].(int64), est["available"].(int64); need > avail {
		return nil, PaymentRequired(fmt.Sprintf("积分不足：开始制作至少需要 %d 积分（角色分析和第一章），当前可用 %d", need, avail), map[string]any{"need": need, "available": avail})
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
	s.RemoveSpeechTags = cfg.SpeechTagsRemoved()
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

// pipelineRunner carries one run of the production line.
type pipelineRunner struct {
	a   *App
	jc  *jobs.Context
	run *model.PipelineRun
	b   *model.Book
	cfg model.PipelineConfig
	x   *Access
	chs []model.Chapter

	mu      sync.Mutex
	stages  map[string]any
	states  map[string]any     // chapter id -> {index, title, status, step, script, tts, post, render, left, error}
	partial map[string]float64 // chapter id -> progress within its current step
	saved   time.Time
	bgmOnce sync.Once
	bgm     string
	tried   map[string]bool // characters castNew already tried in this run
	halt    string          // "paused" | "canceled", set by the watcher
}

func (r *pipelineRunner) halted() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.halt
}

func (r *pipelineRunner) stageDone(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.stages[key].(map[string]any)
	return ok && m["status"] == "done"
}

func (r *pipelineRunner) setStage(key string, kv map[string]any) {
	r.mu.Lock()
	m, _ := r.stages[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	for k, v := range kv {
		m[k] = v
	}
	r.stages[key] = m
	r.mu.Unlock()
	r.report("")
	r.save(true, nil)
}

func (r *pipelineRunner) chapterState(id string) map[string]any {
	m, _ := r.states[id].(map[string]any)
	if m == nil {
		m = map[string]any{}
		r.states[id] = m
	}
	return m
}

// setChapter updates one chapter's state; status changes are saved at once, the rest at most every
// couple of seconds.
func (r *pipelineRunner) setChapter(id string, kv map[string]any) {
	r.mu.Lock()
	m := r.chapterState(id)
	_, statusChange := kv["status"]
	for k, v := range kv {
		m[k] = v
	}
	if _, ok := kv["step"]; ok {
		r.partial[id] = 0
	}
	r.mu.Unlock()
	r.report("")
	r.save(statusChange, nil)
}

// report recomputes the stages and the overall progress bar.
func (r *pipelineRunner) report(text string) {
	r.mu.Lock()
	total := len(r.chs)
	overall := 0.0
	for _, st := range pipelineStages {
		m, _ := r.stages[st.Key].(map[string]any)
		if m == nil {
			m = map[string]any{}
			r.stages[st.Key] = m
		}
		if st.Key == "characters" || st.Key == "casting" {
			switch m["status"] {
			case "done":
				overall += st.Weight
			case "running":
				p, _ := m["progress"].(float64)
				overall += st.Weight * p
			}
			continue
		}
		done, running, part := 0, 0, 0.0
		for _, ch := range r.chs {
			cs := r.chapterState(ch.ID)
			if cs[st.Key] == "done" {
				done++
			} else if cs["step"] == st.Key && cs["status"] == "running" {
				running++
				part += r.partial[ch.ID]
			}
		}
		p := 0.0
		if total > 0 {
			p = (float64(done) + part) / float64(total)
		}
		status := "pending"
		switch {
		case total > 0 && done == total:
			status = "done"
		case running > 0 || done > 0:
			status = "running"
		}
		m["status"], m["progress"], m["done"], m["total"] = status, p, done, total
		overall += st.Weight * p
	}
	r.mu.Unlock()
	r.jc.Progress(overall, text)
}

func (r *pipelineRunner) save(force bool, extra map[string]any) {
	r.mu.Lock()
	if !force && extra == nil && time.Since(r.saved) < 2*time.Second {
		r.mu.Unlock()
		return
	}
	r.saved = time.Now()
	up := map[string]any{"stages": model.Map{V: cloneMap(r.stages)}, "chapter_states": model.Map{V: cloneMap(r.states)}, "progress": r.jc.Job.Progress}
	r.mu.Unlock()
	for k, v := range extra {
		up[k] = v
	}
	r.a.DB.Model(r.run).Updates(up)
	r.a.emit(r.b.StudioID, eventBook(r.b.ID, "pipeline"))
}

// cloneMap copies two levels deep, so a snapshot can be saved while chapters keep changing.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if inner, ok := v.(map[string]any); ok {
			c := make(map[string]any, len(inner))
			for ik, iv := range inner {
				c[ik] = iv
			}
			out[k] = c
			continue
		}
		out[k] = v
	}
	return out
}

// stepReporter turns a chapter step's progress into the run's progress bar.
type stepReporter struct {
	r     *pipelineRunner
	id    string
	title string
}

func (s *stepReporter) Progress(p float64, text string) {
	s.r.mu.Lock()
	s.r.partial[s.id] = min(max(p, 0), 1)
	s.r.mu.Unlock()
	if text != "" {
		text = s.title + "：" + text
	}
	s.r.report(text)
	s.r.save(false, nil)
}

func (s *stepReporter) SetResult(map[string]any) {}

func (a *App) jobPipeline(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var run model.PipelineRun
	if err := a.DB.First(&run, "id = ?", j.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	switch run.Status { // paused or cancelled while still in the queue
	case "paused":
		return jobs.Pause()
	case "canceled":
		return context.Canceled
	}
	var b model.Book
	if err := a.DB.First(&b, "id = ?", run.BookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	r := &pipelineRunner{a: a, jc: jc, run: &run, b: &b, cfg: run.Config.V, stages: run.Stages.V, states: run.ChapterStates.V, partial: map[string]float64{}, tried: map[string]bool{}}
	if r.stages == nil {
		r.stages = map[string]any{}
	}
	if r.states == nil {
		r.states = map[string]any{}
	}
	r.chs = a.pipelineChapters(b.ID, r.cfg.ChapterIDs)
	for _, ch := range r.chs {
		cs := r.chapterState(ch.ID)
		cs["index"], cs["title"] = ch.Index, ch.Title
		if cs["status"] == nil || cs["status"] == "running" {
			cs["status"] = "pending"
		}
	}
	if run.StartedAt == nil {
		t := now()
		run.StartedAt = &t
	}
	r.save(true, map[string]any{"status": "running", "wait_reason": "", "started_at": run.StartedAt, "error": ""})
	if len(r.chs) == 0 {
		err := errors.New("没有可制作的章节：所选章节已被删除或不再纳入制作")
		r.save(true, map[string]any{"status": "failed", "finished_at": now(), "error": err.Error()})
		return jobs.Permanent(err)
	}
	r.x = &Access{User: &model.User{Base: model.Base{ID: run.CreatedBy}}, StudioID: b.StudioID, Role: model.RoleOwner, Book: &b, perms: map[Perm]bool{}}
	for _, p := range allPerms {
		r.x.perms[p] = true
	}
	var u model.User
	if a.DB.First(&u, "id = ?", run.CreatedBy).Error == nil {
		r.x.User = &u
	}

	// pause and cancel take effect at once: the watcher stops the work in progress
	work, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-t.C:
			}
			var cur model.PipelineRun
			if a.DB.Select("status").First(&cur, "id = ?", run.ID).Error != nil {
				continue
			}
			if cur.Status == "paused" || cur.Status == "canceled" {
				r.mu.Lock()
				r.halt = cur.Status
				r.mu.Unlock()
				stop()
				return
			}
		}
	}()

	err := r.produce(work)
	halt := r.halted()
	if halt == "" && ctx.Err() != nil {
		err = context.Canceled
	}
	r.report("")
	var used model.Job
	a.DB.Select("credits_used").First(&used, "id = ?", j.ID)
	t := now()
	switch {
	case halt == "paused" || errors.Is(err, jobs.Pause()):
		st := "paused"
		var cur model.PipelineRun
		a.DB.Select("status", "wait_reason").First(&cur, "id = ?", run.ID)
		if cur.Status == "waiting" {
			st = "waiting"
		}
		r.save(true, map[string]any{"status": st, "used_credits": used.CreditsUsed})
		return jobs.Pause()
	case halt == "canceled" || errors.Is(err, context.Canceled):
		r.save(true, map[string]any{"status": "canceled", "finished_at": &t, "used_credits": used.CreditsUsed})
		return context.Canceled
	case err != nil:
		r.save(true, map[string]any{"status": "failed", "finished_at": &t, "error": truncateStr(err.Error(), 900), "used_credits": used.CreditsUsed})
		a.FireWebhook(b.StudioID, "pipeline.failed", map[string]any{"runId": run.ID, "bookId": b.ID, "error": err.Error()})
		return jobs.Permanent(err)
	}
	r.save(true, map[string]any{"status": "completed", "finished_at": &t, "progress": 1, "used_credits": used.CreditsUsed, "stage": "done"})
	scope := ""
	if len(r.cfg.ChapterIDs) > 0 {
		scope = fmt.Sprintf("（%s 至 %s）", r.chs[0].Title, r.chs[len(r.chs)-1].Title)
	}
	if a.bookFinished(b.ID) {
		a.DB.Model(&b).Update("status", model.BookCompleted)
	}
	a.Notify(run.CreatedBy, b.StudioID, "pipeline_done", "《"+b.Title+"》一键成书完成"+scope, fmt.Sprintf("%d 章已生成成品，可以试听、精修或导出", len(r.chs)), "/books/"+b.ID)
	a.FireWebhook(b.StudioID, "pipeline.completed", map[string]any{"runId": run.ID, "bookId": b.ID})
	jc.Progress(1, "全部完成")
	return nil
}

// bookFinished reports whether every chapter marked for production has an up-to-date render.
func (a *App) bookFinished(bookID string) bool {
	var left int64
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ? AND (render_id = '' OR render_stale = ?)", bookID, true, true).Count(&left)
	return left == 0
}

func (r *pipelineRunner) produce(ctx context.Context) error {
	a, b, cfg := r.a, r.b, r.cfg
	j := r.jc.Job
	// 1) characters: read the opening chapters, unless the book already has a cast
	if !r.stageDone("characters") {
		a.DB.Model(r.run).Update("stage", "characters")
		r.setStage("characters", map[string]any{"status": "running", "progress": 0.0})
		if cfg.Mode != model.ModeSingle && !a.hasCast(b.ID) {
			h, err := a.LLMFor(FeatCharacter, cfg.Tier, b.StudioID)
			if err != nil {
				return err
			}
			words := 0
			for _, c := range r.chs {
				words += c.WordCount
			}
			est := a.Estimate(FeatCharacter, "", float64(analysisChars(AnalyzeOpening, words)))
			if err := a.Hold(j.ID, b.StudioID, est); err != nil {
				return err
			}
			infos, chars, err := a.AnalyzeCharacters(ctx, h, r.chs, AnalyzeOptions{Mode: AnalyzeOpening}, func(p float64, t string) {
				r.mu.Lock()
				r.stages["characters"].(map[string]any)["progress"] = p
				r.mu.Unlock()
				r.report(t)
			})
			if err != nil {
				return fmt.Errorf("角色分析失败：%w", err)
			}
			charged, _ := a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: r.run.CreatedBy, BookID: b.ID, JobID: j.ID, Feature: FeatCharacter, Quantity: float64(chars), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK})
			a.releasePart(j.ID, est-charged)
			a.SaveCharacterInfos(b, infos)
		}
		r.setStage("characters", map[string]any{"status": "done", "progress": 1.0})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 2) casting
	if !r.stageDone("casting") {
		a.DB.Model(r.run).Update("stage", "casting")
		r.setStage("casting", map[string]any{"status": "running", "progress": 0.2})
		r.report("正在为角色挑选音色")
		if cfg.NarratorVoiceID != "" {
			a.DB.Model(&model.Character{}).Where("book_id = ? AND kind = ? AND voice_id = ''", b.ID, model.CharNarrator).Update("voice_id", cfg.NarratorVoiceID)
		}
		if _, err := a.Cast(ctx, r.x, nil, true, nil); err != nil {
			return err
		}
		if cfg.Mode == model.ModeSingle {
			r.narratorForAll()
		}
		if cfg.PauseAfterCast {
			r.setStage("casting", map[string]any{"status": "done", "progress": 1.0, "awaiting": true})
			r.save(true, map[string]any{"status": "waiting", "wait_reason": "cast", "stage": "casting"})
			a.Notify(r.run.CreatedBy, b.StudioID, "pipeline_waiting", "《"+b.Title+"》选角完成，等待你确认", "确认后将自动继续生成画本与配音", "/books/"+b.ID+"/characters")
			return jobs.Pause()
		}
		r.setStage("casting", map[string]any{"status": "done", "progress": 1.0})
	} else if m, _ := r.stages["casting"].(map[string]any); m["awaiting"] == true && m["confirmed"] != true {
		r.save(true, map[string]any{"status": "waiting", "wait_reason": "cast", "stage": "casting"})
		return jobs.Pause()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 3) chapters, a few at a time
	a.DB.Model(r.run).Update("stage", "chapters")
	policy := a.CastingPolicy(cfg.Tier)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []string
		streak   int
		short    error
	)
	slots := make(chan struct{}, pipelineParallel)
	stopped := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return short != nil || streak >= pipelineGiveUp
	}
loop:
	for _, ch := range r.chs {
		r.mu.Lock()
		done := r.chapterState(ch.ID)["status"] == "done"
		r.mu.Unlock()
		if done {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		if ctx.Err() != nil || stopped() {
			<-slots
			break
		}
		var fresh model.Chapter
		if a.DB.First(&fresh, "id = ?", ch.ID).Error != nil {
			<-slots
			r.setChapter(ch.ID, map[string]any{"status": "failed", "error": "章节已被删除"})
			continue
		}
		// reserve this chapter's credits; when they run out, finish what is under way and wait
		est := a.chapterCredits(&fresh, policy)
		if err := a.Hold(j.ID, b.StudioID, est); err != nil {
			<-slots
			mu.Lock()
			short = err
			mu.Unlock()
			break
		}
		before := a.chapterUsage(j.ID, ch.ID)
		wg.Add(1)
		go func(ch model.Chapter, est int64) {
			defer wg.Done()
			defer func() { <-slots }()
			err := r.chapter(ctx, ch)
			a.releasePart(j.ID, est-(a.chapterUsage(j.ID, ch.ID)-before))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				streak = 0
			case ctx.Err() != nil: // paused or cancelled: the chapter continues next time
				r.setChapter(ch.ID, map[string]any{"status": "pending"})
			case outOfCredits(err): // the balance ran dry mid-chapter: wait for a top-up, do not fail it
				if short == nil {
					short = err
				}
				r.setChapter(ch.ID, map[string]any{"status": "pending"})
			default:
				streak++
				failures = append(failures, ch.Title+"："+err.Error())
				r.setChapter(ch.ID, map[string]any{"status": "failed", "error": truncateStr(err.Error(), 300)})
			}
		}(fresh, est)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case streak >= pipelineGiveUp:
		return fmt.Errorf("连续 %d 章没有完成，已停止。%s", streak, strings.Join(failures[max(0, len(failures)-pipelineGiveUp):], "；"))
	case len(failures) > 0:
		return fmt.Errorf("%d 章没有完成：%s。处理后点「继续」会只重做这些章", len(failures), strings.Join(failures, "；"))
	case short != nil:
		left := 0
		for _, ch := range r.chs {
			if r.chapterState(ch.ID)["status"] != "done" {
				left++
			}
		}
		r.save(true, map[string]any{"status": "waiting", "wait_reason": "credits", "error": short.Error()})
		a.Notify(r.run.CreatedBy, b.StudioID, "pipeline_waiting", "《"+b.Title+"》积分不足，已暂停", fmt.Sprintf("还有 %d 章没有制作。充值后在一键成书页面点「继续」即可接着做。", left), "/autobook?run="+r.run.ID)
		return jobs.Pause()
	}
	return nil
}

// chapter takes one chapter from text to finished audio, skipping what is already done.
func (r *pipelineRunner) chapter(ctx context.Context, ch model.Chapter) error {
	a, b, cfg := r.a, r.b, r.cfg
	j := r.jc.Job
	set := func(kv map[string]any) { r.setChapter(ch.ID, kv) }
	rep := func() reporter { return &stepReporter{r: r, id: ch.ID, title: ch.Title} }
	sub := func(payload map[string]any) *model.Job {
		return &model.Job{ID: j.ID, StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: r.run.CreatedBy, Payload: model.Map{V: payload}}
	}
	set(map[string]any{"status": "running", "error": ""})
	// script: speakers new to the book join the cast as they are met
	if ch.CurrentScriptID == "" || ch.ScriptStale {
		set(map[string]any{"step": "script"})
		if err := a.scriptParse(ctx, sub(map[string]any{"mode": "ai"}), rep(), true); err != nil {
			return fmt.Errorf("画本生成失败：%w", err)
		}
		a.DB.First(&ch, "id = ?", ch.ID)
	}
	set(map[string]any{"script": "done"})
	if err := r.castNew(ctx); err != nil {
		return fmt.Errorf("给新角色选声音失败：%w", err)
	}
	// voice
	set(map[string]any{"step": "tts"})
	targets, err := a.generationTargets(&ch, GenerateRequest{Scope: "missing"}, nil)
	if err != nil {
		return err
	}
	if len(targets) > 0 {
		if err := a.ttsSegments(ctx, sub(map[string]any{"segmentIds": toAny(targetIDs(targets))}), rep(), true); err != nil && !errors.Is(err, jobs.ErrPermanent) {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var left int64
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND removed = ? AND type NOT IN ? AND audio_status <> ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause}, model.AudioReady).Count(&left)
	set(map[string]any{"left": left})
	if left > 0 {
		var broke int64
		a.DB.Model(&model.Segment{}).Where("script_id = ? AND audio_status = ? AND last_error LIKE ?", ch.CurrentScriptID, model.AudioFailed, "积分不足%").Count(&broke)
		if broke > 0 {
			return PaymentRequired(fmt.Sprintf("积分不足：「%s」还有 %d 句没有配音", ch.Title, left), nil)
		}
		return fmt.Errorf("有 %d 句配音失败，请进入制作台查看原因后重试", left)
	}
	set(map[string]any{"tts": "done"})
	// background music and templates
	set(map[string]any{"step": "post"})
	r.post(&ch)
	set(map[string]any{"post": "done"})
	// render
	a.DB.First(&ch, "id = ?", ch.ID)
	if ch.RenderID == "" || ch.RenderStale {
		set(map[string]any{"step": "render"})
		if err := a.renderChapter(ctx, sub(map[string]any{"preset": cfg.Preset}), rep()); err != nil {
			return fmt.Errorf("合成失败：%w", err)
		}
	}
	set(map[string]any{"render": "done", "status": "done", "step": ""})
	return nil
}

// castNew gives a voice to characters that joined the cast while chapters were scripted.
func (r *pipelineRunner) castNew(ctx context.Context) error {
	a, b := r.a, r.b
	unlock := lockBook(b.ID)
	defer unlock()
	var all, ids []string
	a.DB.Model(&model.Character{}).Where("book_id = ? AND voice_id = '' AND mode = 'ai' AND kind <> ?", b.ID, model.CharNarrator).Pluck("id", &all)
	for _, id := range all {
		if !r.tried[id] { // a character no voice fits is not sent to casting again for every chapter
			r.tried[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	a.DB.Model(&model.Character{}).Where("id IN ?", ids).Update("candidate", false)
	if r.cfg.Mode == model.ModeSingle {
		r.narratorForAll()
		return nil
	}
	_, err := a.Cast(ctx, r.x, ids, true, nil)
	return err
}

// narratorForAll gives every character the narrator's voice (single-narrator books).
func (r *pipelineRunner) narratorForAll() {
	var narr model.Character
	if r.a.DB.First(&narr, "book_id = ? AND kind = ?", r.b.ID, model.CharNarrator).Error == nil && narr.VoiceID != "" {
		r.a.DB.Model(&model.Character{}).Where("book_id = ? AND voice_id = ''", r.b.ID).Update("voice_id", narr.VoiceID)
	}
}

// post lays background music under a chapter and applies the intro/outro templates.
func (r *pipelineRunner) post(ch *model.Chapter) {
	a, cfg := r.a, r.cfg
	if cfg.BGM {
		r.bgmOnce.Do(func() {
			r.bgm = cfg.BGMAssetID
			if r.bgm == "" {
				r.bgm = a.pickBGM(r.b)
			}
		})
		if r.bgm != "" {
			m := a.mixFor(ch)
			changed := false
			for ti := range m.Tracks.V {
				if m.Tracks.V[ti].Kind == "bgm" && len(m.Tracks.V[ti].Clips) == 0 {
					m.Tracks.V[ti].Clips = append(m.Tracks.V[ti].Clips, model.Clip{ID: model.NewID(), AssetID: r.bgm, Name: "背景音乐", Fill: true, Loop: true, Volume: 0, FadeInMs: 1500, FadeOutMs: 3000})
					changed = true
				}
			}
			if changed {
				a.DB.Model(m).Update("tracks", m.Tracks)
			}
		}
	}
	if cfg.IntroTemplateID != "" || cfg.OutroTemplateID != "" {
		a.DB.Model(&model.Mix{}).Where("chapter_id = ?", ch.ID).Updates(map[string]any{"intro": cfg.IntroTemplateID != "", "outro": cfg.OutroTemplateID != ""})
	}
}

// outOfCredits reports whether an error means the studio's balance is empty (a top-up fixes it), as
// opposed to a spending cap or anything else.
func outOfCredits(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == "insufficient_credits"
}

// chapterUsage is what a job has been charged for one chapter so far.
func (a *App) chapterUsage(jobID, chapterID string) int64 {
	var used int64
	a.DB.Model(&model.UsageRecord{}).Where("job_id = ? AND chapter_id = ?", jobID, chapterID).Select("COALESCE(SUM(credits),0)").Scan(&used)
	return used
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
		if action == "confirm" || (run.Status == "waiting" && run.WaitReason != "credits") {
			st := run.Stages.V
			if st == nil {
				st = map[string]any{}
			}
			st["casting"] = map[string]any{"status": "done", "progress": 1, "confirmed": true}
			a.DB.Model(run).Update("stages", model.Map{V: st})
		}
		a.DB.Model(run).Updates(map[string]any{"status": "running", "wait_reason": "", "error": ""})
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
	active := r.Status == "running" || r.Status == "queued"
	stages := []map[string]any{}
	for _, st := range pipelineStages {
		m, _ := r.Stages.V[st.Key].(map[string]any)
		view := map[string]any{"key": st.Key, "name": st.Name, "status": "pending", "progress": 0}
		for k, v := range m {
			view[k] = v
		}
		view["key"], view["name"] = st.Key, st.Name
		if view["status"] == "running" && !active {
			view["status"] = "paused"
		}
		stages = append(stages, view)
	}
	// chapters in book order (older runs did not store the index and title)
	type chView struct {
		id    string
		index int
		m     map[string]any
	}
	var list []chView
	for id, v := range r.ChapterStates.V {
		m, _ := v.(map[string]any)
		if m == nil {
			continue
		}
		idx, _ := m["index"].(float64)
		list = append(list, chView{id, int(idx), m})
	}
	if len(list) > 0 && list[0].index == 0 {
		for _, c := range a.pipelineChapters(r.BookID, r.Config.V.ChapterIDs) {
			for i := range list {
				if list[i].id == c.ID {
					list[i].index = c.Index
					list[i].m["title"] = c.Title
				}
			}
		}
	}
	sort.Slice(list, func(i, k int) bool { return list[i].index < list[k].index })
	chapters := make([]map[string]any, 0, len(list))
	for _, c := range list {
		status, _ := c.m["status"].(string)
		if status == "" { // older runs: done once rendered
			status = "pending"
			if c.m["render"] == "done" {
				status = "done"
			}
		}
		if status == "running" && !active {
			status = "pending"
		}
		chapters = append(chapters, map[string]any{"id": c.id, "index": c.index, "title": c.m["title"], "status": status, "step": c.m["step"], "error": c.m["error"], "left": c.m["left"]})
	}
	return map[string]any{
		"id": r.ID, "bookId": r.BookID, "book": map[string]any{"id": b.ID, "title": b.Title, "cover": a.AssetURLByID(b.CoverAssetID), "wordCount": b.WordCount},
		"status": r.Status, "waitReason": r.WaitReason, "stage": r.Stage, "progress": j.Progress, "progressText": j.ProgressText, "stages": stages,
		"chapters": chapters, "chapterStates": r.ChapterStates.V,
		"config": r.Config.V, "estCredits": r.EstCredits, "usedCredits": max(r.UsedCredits, j.CreditsUsed), "error": r.Error, "elapsedMs": elapsed,
		"createdAt": r.CreatedAt, "startedAt": r.StartedAt, "finishedAt": r.FinishedAt, "jobId": r.JobID, "priority": r.Priority,
	}
}
