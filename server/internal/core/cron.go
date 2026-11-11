package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

func nil2ctx() context.Context { return context.Background() }

func (a *App) registerJobs() {
	q := a.Q
	q.Register("character_analysis", jobs.LaneDefault, a.jobCharacterAnalysis)
	q.Register("script_parse", jobs.LaneDefault, a.jobScriptParse)
	q.Register("script_sync", jobs.LaneDefault, a.jobScriptSync)
	q.Register("tts_segments", jobs.LaneAudio, a.jobTTSSegments)
	q.Register("render", jobs.LaneAudio, a.jobRender)
	q.Register("export", jobs.LaneAudio, a.jobExport)
	q.Register("align", jobs.LaneAudio, a.jobAlign)
	q.Register("review", jobs.LaneAudio, a.jobReview)
	q.Register("voice_design", jobs.LaneAudio, a.jobVoiceDesign)
	q.Register("voice_clone", jobs.LaneAudio, a.jobVoiceClone)
	q.Register("sfx_generate", jobs.LaneAudio, a.jobGenerate(model.KindSFX))
	q.Register("music_generate", jobs.LaneAudio, a.jobGenerate(model.KindMusic))
	q.Register("image_generate", jobs.LaneDefault, a.jobGenerate(model.KindImage))
	q.Register("quick_tts", jobs.LaneAudio, a.jobQuickTTS)
	q.Register("pipeline", jobs.LanePipeline, a.jobPipeline)
	q.Register("webhook", jobs.LaneDefault, a.jobWebhook)
	q.Register("casting", jobs.LaneDefault, a.jobCasting)
	q.Register("eval_run", jobs.LaneDefault, a.jobEvalRun)
	q.Register("voice_preview_import", jobs.LaneDefault, a.jobPreviewPackImport)
	q.Register("music_import", jobs.LaneDefault, a.jobMusicImport)
	q.Register("sfx_import", jobs.LaneDefault, a.jobSfxImport)
}

// jobCasting runs AI casting asynchronously for many characters.
func (a *App) jobCasting(ctx context.Context, jc *jobs.Context) error {
	var b model.Book
	if err := a.DB.First(&b, "id = ?", jc.Job.BookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var u model.User
	a.DB.First(&u, "id = ?", jc.Job.CreatedBy)
	x, err := a.BookAccess(&u, b.ID)
	if err != nil {
		return jobs.Permanent(err)
	}
	var ids []string
	if raw, ok := jc.Job.Payload.V["characterIds"].([]any); ok {
		for _, v := range raw {
			ids = append(ids, v.(string))
		}
	}
	apply, _ := jc.Job.Payload.V["apply"].(bool)
	res, err := a.Cast(ctx, x, ids, apply, jc)
	if err != nil {
		return jobs.Permanent(err)
	}
	jc.Progress(1, "已为 "+itoa(len(res))+" 个角色生成候选音色")
	return nil
}

func itoa(n int) string {
	return func() string { return fmtInt(n) }()
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func (a *App) cron(ctx context.Context) {
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	tick := 0
	run := func(name string, fn func()) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("cron panic", "task", name, "panic", r)
			}
		}()
		fn()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-minute.C:
			tick++
			// with several instances each task runs on one of them per interval (see App.once)
			every := func(name string, interval time.Duration, fn func()) {
				if a.once(name, interval) {
					run(name, fn)
				}
			}
			every("publish", time.Minute, func() { a.PublishDue(ctx) })
			every("locks", time.Minute, a.releaseStaleLocks)
			every("membership", time.Minute, a.MembershipTick)
			if tick%10 == 0 {
				every("reminders", 10*time.Minute, a.RemindTasks)
			}
			if tick%60 == 1 {
				every("expire-lots", time.Hour, a.ExpireLots)
				every("expire-voices", time.Hour, a.ExpireVoices)
				every("design-candidates", time.Hour, a.CleanupDesignCandidates)
				every("plans", time.Hour, a.RenewPlans)
				every("deletions", time.Hour, a.ProcessDeletions)
			}
			if tick%720 == 2 {
				every("trash", 12*time.Hour, a.PurgeTrash)
				run("tmp", a.cleanupTemp)                // each instance has its own temporary files
				run("storage-evict", a.evictLocalCopies) // and its own local copies of stored files
			}
		}
	}
}

func (a *App) releaseStaleLocks() {
	a.DB.Model(&model.Chapter{}).Where("edit_lock_user_id <> '' AND edit_lock_at < ?", now().Add(-lockTTL)).Updates(map[string]any{"edit_lock_user_id": "", "edit_lock_at": nil})
}

// RenewPlans grants the monthly credits of contract plans pinned to studios by an administrator.
// (Regular memberships deliver their credits through MembershipTick.)
func (a *App) RenewPlans() {
	var studios []model.Studio
	a.DB.Where("plan_override_id <> '' AND plan_renew_at IS NOT NULL AND plan_renew_at <= ? AND status = ? AND deleted_at IS NULL", now(), "active").Find(&studios)
	for _, st := range studios {
		var p model.Plan
		if a.DB.First(&p, "id = ?", st.PlanOverrideID).Error != nil {
			continue
		}
		next := st.PlanRenewAt.AddDate(0, 1, 0)
		for next.Before(now()) {
			next = next.AddDate(0, 1, 0)
		}
		if p.MonthlyCredits > 0 {
			exp := next.AddDate(0, 1, 0) // unused credits roll over for one extra month
			a.Grant(st.ID, p.MonthlyCredits, "plan", &exp, p.Name+"月度积分", "")
		}
		a.DB.Model(&st).Update("plan_renew_at", next)
	}
}

// ProcessDeletions anonymizes accounts whose 7-day deletion cool-off has passed.
func (a *App) ProcessDeletions() {
	var users []model.User
	a.DB.Where("deletion_at IS NOT NULL AND deletion_at < ?", now()).Find(&users)
	for _, u := range users {
		a.DB.Model(&u).Updates(map[string]any{"email": nil, "phone": nil, "nickname": "已注销用户", "password_hash": "", "status": model.UserDisabled, "avatar_url": "", "bio": "", "deletion_at": nil})
		a.RevokeAll(u.ID, "")
		a.DB.Where("user_id = ?", u.ID).Delete(&model.StudioMember{})
	}
}

func (a *App) cleanupTemp() {
	root := filepath.Join(a.Store.Root, "tmp")
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
	imports := filepath.Join(root, "imports")
	files, _ := os.ReadDir(imports)
	for _, f := range files {
		if info, err := f.Info(); err == nil && time.Since(info.ModTime()) > 48*time.Hour {
			os.Remove(filepath.Join(imports, f.Name()))
		}
	}
}
