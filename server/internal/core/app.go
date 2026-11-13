// Package core implements the application's business logic, shared by HTTP handlers and job workers.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/audio"
	"voicebook/internal/bus"
	"voicebook/internal/config"
	"voicebook/internal/events"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/storage"
)

type App struct {
	Cfg   *config.Config
	DB    *gorm.DB
	Store *storage.Store
	Hub   *events.Hub
	Q     *jobs.Queue
	FF    *audio.FF
	Bus   bus.Bus // in-process unless SetBus gives it Redis

	settings sync.Map
	seeding  atomic.Bool // the built-in voice catalog and library are still being written
}

// Seeding reports whether first-start seeding (voice catalogs, built-in library) is still running.
func (a *App) Seeding() bool { return a.seeding.Load() }

func New(cfg *config.Config, db *gorm.DB, store *storage.Store) *App {
	a := &App{
		Cfg: cfg, DB: db, Store: store, Hub: events.NewHub(),
		FF:  &audio.FF{FFmpeg: cfg.FFmpeg, FFprobe: cfg.FFprobe},
		Bus: bus.NewLocal(),
	}
	a.Q = jobs.New(db, map[string]int{
		jobs.LaneDefault:  max(2, cfg.Workers/2),
		jobs.LaneAudio:    max(2, cfg.Workers),
		jobs.LanePipeline: 2,
	})
	a.Q.OnUpdate = a.onJobUpdate
	a.Q.OnFinish = a.onJobFinish
	a.Q.PriorityOf = func(studioID string) int { return min(max(a.StudioPlan(studioID).QueuePriority, 0), 10) }
	a.Q.PlanOf = func(studioID string) string { return a.StudioPlan(studioID).Code }
	a.registerJobs()
	if store != nil {
		store.UseIndex(objectIndex{db})
	}
	return a
}

func (a *App) Start(ctx context.Context) {
	a.wireBus(ctx)
	a.ApplyStorage()
	a.loadQueues()
	a.Q.Start(ctx)
	go a.cron(ctx)
}

// ---------- errors ----------

type Error struct {
	Status int    `json:"-"`
	Code   string `json:"code"`
	Msg    string `json:"message"`
	Data   any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Msg }

func Bad(msg string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Msg: fmt.Sprintf(msg, args...)}
}
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Msg: what + "不存在或已被删除"}
}
func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "你没有执行此操作的权限"
	}
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Msg: msg}
}
func Conflict(msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: "conflict", Msg: msg}
}
func Unauthorized(msg string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Msg: msg}
}
func PaymentRequired(msg string, data any) *Error {
	return &Error{Status: http.StatusPaymentRequired, Code: "insufficient_credits", Msg: msg, Data: data}
}
func TooMany(msg string) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Msg: msg}
}

// AsError converts gorm "not found" and others into *Error.
func AsError(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NotFound(what)
	}
	return err
}

// ---------- helpers ----------

// clock can be replaced in tests to move time forward.
var clock = func() time.Time { return time.Now().UTC() }

func now() time.Time { return clock() }

func ptrTime(t time.Time) *time.Time { return &t }

func (a *App) emit(studioID string, ev events.Event) {
	if studioID == "" {
		return
	}
	a.publish(topicEvents, eventMsg{Scope: "studio", ID: studioID, Event: stamp(ev)})
}

func (a *App) onJobUpdate(j *model.Job) {
	view := map[string]any{
		"id": j.ID, "type": j.Type, "status": j.Status, "progress": j.Progress, "progressText": j.ProgressText,
		"bookId": j.BookID, "chapterId": j.ChapterID, "refType": j.RefType, "refId": j.RefID, "title": j.Title,
		"error": j.Error, "creditsUsed": j.CreditsUsed,
	}
	a.emit(j.StudioID, events.Event{Type: "job", BookID: j.BookID, ChapterID: j.ChapterID, Data: view})
}

func (a *App) onJobFinish(j *model.Job) {
	// release any remaining credit hold
	a.releaseHold(j.ID)
	if j.CreatedBy == "" || j.Type == "tts_preview" || j.Type == "webhook" {
		return
	}
	title := j.Title
	if title == "" {
		title = jobTypeName(j.Type)
	}
	link := ""
	if j.BookID != "" {
		link = "/books/" + j.BookID
		if j.ChapterID != "" {
			link += "/chapters/" + j.ChapterID
		}
	}
	switch j.Status {
	case model.JobSucceeded:
		if j.Type == "tts_segments" || j.Type == "render" || j.Type == "export" || j.Type == "script_parse" || j.Type == "character_analysis" || j.Type == "align" || j.Type == "review" || j.Type == "voice_clone" || j.Type == "pipeline" {
			a.Notify(j.CreatedBy, j.StudioID, "job_done", title+" 已完成", j.ProgressText, link)
		}
	case model.JobFailed:
		a.Notify(j.CreatedBy, j.StudioID, "job_failed", title+" 失败", j.Error, link)
	}
	slog.Info("job finished", "type", j.Type, "id", j.ID, "status", j.Status)
}

func jobTypeName(t string) string {
	names := map[string]string{
		"book_import": "导入书稿", "chapter_split_llm": "智能拆章", "character_analysis": "角色分析", "script_parse": "生成画本",
		"tts_segments": "AI 配音", "render": "合成成品", "export": "导出", "align": "智能对轨", "review": "智能审听",
		"voice_design": "AI 设计音色", "voice_clone": "声音克隆", "sfx_generate": "AI 音效", "music_generate": "AI 配乐",
		"image_generate": "AI 图片", "pipeline": "一键成书", "quick_tts": "快速配音", "lexicon_extract": "提取发音词条",
		"casting": "AI 选角", "moderation": "内容审核", "webhook": "Webhook 投递", "publish": "发布", "eval_run": "评测",
	}
	if n, ok := names[t]; ok {
		return n
	}
	return t
}

// Activity records a book-level activity log entry.
func (a *App) Activity(studioID, bookID, actorID, action, targetType, targetID, summary string) {
	a.DB.Create(&model.ActivityLog{StudioID: studioID, BookID: bookID, ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID, Summary: summary, Detail: model.EmptyMap()})
}

// Audit records an admin console action.
func (a *App) Audit(actorID, action, targetType, targetID, reason, ip string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	a.DB.Create(&model.AuditLog{ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID, Reason: reason, IP: ip, Detail: model.Map{V: detail}})
}
