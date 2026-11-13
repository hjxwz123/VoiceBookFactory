// Package jobs implements a database-backed job queue with lanes, leases, retries and cancellation.
// The Queue interface boundary allows swapping to Redis (asynq) in production.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/model"
)

const (
	LaneDefault  = "default"  // LLM / text work
	LaneAudio    = "audio"    // synthesis, rendering, alignment
	LanePipeline = "pipeline" // long-running orchestrations
)

// ErrPermanent marks a failure that must not be retried.
var ErrPermanent = errors.New("permanent failure")

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrPermanent, err) // the cause stays visible to errors.As
}

type Handler func(ctx context.Context, jc *Context) error

type Context struct {
	Job  *model.Job
	q    *Queue
	last time.Time
	mu   sync.Mutex
}

// Progress updates progress (0..1) and a human readable line; throttled to avoid write storms.
func (c *Context) Progress(p float64, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	c.Job.Progress = p
	if text != "" {
		c.Job.ProgressText = text
	}
	if time.Since(c.last) < 400*time.Millisecond && p < 1 {
		return
	}
	c.last = time.Now()
	c.q.db.Model(&model.Job{}).Where("id = ?", c.Job.ID).Updates(map[string]any{"progress": p, "progress_text": c.Job.ProgressText})
	if c.q.OnUpdate != nil {
		c.q.OnUpdate(c.Job)
	}
}

// SetResult merges values into the job result.
func (c *Context) SetResult(kv map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Job.Result.V == nil {
		c.Job.Result.V = map[string]any{}
	}
	for k, v := range kv {
		c.Job.Result.V[k] = v
	}
	c.q.db.Model(&model.Job{}).Where("id = ?", c.Job.ID).Update("result", c.Job.Result)
}

// AddCredits records credits consumed by this job.
func (c *Context) AddCredits(n int64) {
	c.mu.Lock()
	c.Job.CreditsUsed += n
	c.mu.Unlock()
	c.q.db.Model(&model.Job{}).Where("id = ?", c.Job.ID).Update("credits_used", gorm.Expr("credits_used + ?", n))
}

// Canceled reports whether a cancel was requested.
func (c *Context) Canceled() bool {
	var j model.Job
	if err := c.q.db.Select("cancel_req", "status").First(&j, "id = ?", c.Job.ID).Error; err != nil {
		return false
	}
	return j.CancelReq || j.Status == model.JobCanceled
}

// Lanes are the kinds of work a job can be: every job type belongs to one lane.
var Lanes = []string{LaneDefault, LaneAudio, LanePipeline}

// Pool is a group of workers: it runs jobs of the given lanes for studios on the given membership
// plans. A job may be taken by any pool whose lanes and plans both match it, so a plan served by two
// pools can use the capacity of both. Jobs without a studio (system work) match every plan.
type Pool struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Size  int      `json:"size"`  // workers on this instance
	Lanes []string `json:"lanes"` // kinds of work it takes
	Plans []string `json:"plans"` // plan codes it serves; empty = every plan
}

type pool struct {
	Pool
	wake chan struct{}
	stop context.CancelFunc
}

type Queue struct {
	db       *gorm.DB
	handlers map[string]Handler
	workerID string
	running  sync.Map // jobID -> context.CancelFunc

	mu     sync.Mutex
	config []Pool
	pools  []*pool
	root   context.Context // set by Start; nil before

	OnUpdate func(*model.Job)
	// PriorityOf gives the default priority for a studio's jobs (membership queue priority).
	PriorityOf func(studioID string) int
	// PlanOf names a studio's membership plan; it decides which pools may take the studio's jobs.
	PlanOf   func(studioID string) string
	OnFinish func(*model.Job)
	// Broadcast, when set, tells the other instances of a deployment about a wake-up ("wake", lane) or
	// a cancellation ("cancel", job id); they call Wake / CancelLocal. Nil for a single instance.
	Broadcast func(kind, arg string)
	wg        sync.WaitGroup
	limiters  sync.Map // key -> chan struct{}
}

// New makes a queue with one pool per lane, sized as given, open to every plan. SetPools replaces them.
func New(db *gorm.DB, lanes map[string]int) *Queue {
	host, _ := os.Hostname()
	q := &Queue{db: db, handlers: map[string]Handler{}, workerID: fmt.Sprintf("%s-%d", host, os.Getpid())}
	for _, lane := range Lanes {
		if n, ok := lanes[lane]; ok {
			q.config = append(q.config, Pool{ID: "lane:" + lane, Name: lane, Size: n, Lanes: []string{lane}})
		}
	}
	return q
}

// Pools returns the current pool configuration.
func (q *Queue) Pools() []Pool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]Pool(nil), q.config...)
}

// SetPools replaces the pools. While running, workers of the old pools finish the job in hand and
// stop; the new pools start at once, so no job is interrupted.
func (q *Queue) SetPools(ps []Pool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.config = append([]Pool(nil), ps...)
	if q.root != nil {
		q.apply()
	}
}

// apply (with q.mu held) stops the running pools and starts the configured ones.
func (q *Queue) apply() {
	for _, p := range q.pools {
		p.stop()
	}
	q.pools = nil
	for _, cfg := range q.config {
		if cfg.Size <= 0 || len(cfg.Lanes) == 0 {
			continue
		}
		ctx, stop := context.WithCancel(q.root)
		p := &pool{Pool: cfg, wake: make(chan struct{}, 1), stop: stop}
		q.pools = append(q.pools, p)
		for i := 0; i < cfg.Size; i++ {
			q.wg.Add(1)
			go q.worker(ctx, p)
		}
	}
}

// LaneOf maps job types to lanes.
var LaneOf = map[string]string{}

func (q *Queue) Register(jobType, lane string, h Handler) {
	q.handlers[jobType] = h
	LaneOf[jobType] = lane
}

// Enqueue persists a job and wakes a worker in its lane.
func (q *Queue) Enqueue(j *model.Job) error {
	if j.Lane == "" {
		j.Lane = LaneOf[j.Type]
		if j.Lane == "" {
			j.Lane = LaneDefault
		}
	}
	if j.Status == "" {
		j.Status = model.JobQueued
	}
	if j.Priority == 0 && j.StudioID != "" && q.PriorityOf != nil {
		j.Priority = q.PriorityOf(j.StudioID)
	}
	if j.PlanCode == "" && j.StudioID != "" && q.PlanOf != nil {
		j.PlanCode = q.PlanOf(j.StudioID)
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = 3
	}
	if j.RunAt.IsZero() {
		j.RunAt = time.Now().UTC()
	}
	if j.Payload.V == nil {
		j.Payload.V = map[string]any{}
	}
	if j.Result.V == nil {
		j.Result.V = map[string]any{}
	}
	if err := q.db.Create(j).Error; err != nil {
		return err
	}
	q.Wake(j.Lane)
	q.tell("wake", j.Lane)
	if q.OnUpdate != nil {
		q.OnUpdate(j)
	}
	return nil
}

func (q *Queue) tell(kind, arg string) {
	if q.Broadcast != nil {
		q.Broadcast(kind, arg)
	}
}

// CancelLocal stops a job if it runs on this instance (the job row already says cancel_req).
func (q *Queue) CancelLocal(jobID string) {
	if c, ok := q.running.Load(jobID); ok {
		c.(context.CancelFunc)()
	}
}

// Wake nudges the pools that take jobs of a lane.
func (q *Queue) Wake(lane string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, p := range q.pools {
		if slices.Contains(p.Lanes, lane) {
			select {
			case p.wake <- struct{}{}:
			default:
			}
		}
	}
}

// Cancel requests cancellation; a running handler's context is canceled immediately.
func (q *Queue) Cancel(jobID string) error {
	var j model.Job
	if err := q.db.First(&j, "id = ?", jobID).Error; err != nil {
		return err
	}
	switch j.Status {
	case model.JobQueued, model.JobPaused:
		now := time.Now().UTC()
		q.db.Model(&j).Updates(map[string]any{"status": model.JobCanceled, "cancel_req": true, "finished_at": now})
		j.Status = model.JobCanceled
		if q.OnFinish != nil {
			q.OnFinish(&j)
		}
	case model.JobRunning:
		q.db.Model(&j).Update("cancel_req", true)
		q.CancelLocal(jobID)
		q.tell("cancel", jobID) // it may be running on another instance
	}
	return nil
}

// Retry re-queues a failed or canceled job.
func (q *Queue) Retry(jobID string) error {
	res := q.db.Model(&model.Job{}).Where("id = ? AND status IN ?", jobID, []string{model.JobFailed, model.JobCanceled}).
		Updates(map[string]any{"status": model.JobQueued, "cancel_req": false, "error": "", "attempts": 0, "run_at": time.Now().UTC(), "progress": 0})
	if res.RowsAffected == 0 {
		return errors.New("只有失败或已取消的任务可以重试")
	}
	var j model.Job
	q.db.First(&j, "id = ?", jobID)
	q.Wake(j.Lane)
	q.tell("wake", j.Lane)
	if q.OnUpdate != nil {
		q.OnUpdate(&j)
	}
	return nil
}

// Limiter returns a semaphore channel for a key (e.g. a provider account) with capacity n.
func (q *Queue) Limiter(key string, n int) chan struct{} {
	if n <= 0 {
		n = 4
	}
	v, _ := q.limiters.LoadOrStore(key, make(chan struct{}, n))
	return v.(chan struct{})
}

func (q *Queue) Start(ctx context.Context) {
	q.recover()
	q.mu.Lock()
	q.root = ctx
	q.apply()
	q.mu.Unlock()
	go q.reaper(ctx)
}

func (q *Queue) Wait() { q.wg.Wait() }

// recover resets jobs left running by a crashed process.
func (q *Queue) recover() {
	q.db.Model(&model.Job{}).Where("status = ? AND (locked_until IS NULL OR locked_until < ?)", model.JobRunning, time.Now().UTC()).
		Updates(map[string]any{"status": model.JobQueued, "locked_by": "", "progress_text": "服务重启后自动恢复"})
}

func (q *Queue) reaper(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			q.recover()
			for _, lane := range Lanes {
				q.Wake(lane)
			}
		}
	}
}

// worker takes jobs for its pool until the pool stops (ctx). A job runs under the queue's root
// context, so reconfiguring the pools never interrupts it.
func (q *Queue) worker(ctx context.Context, p *pool) {
	defer q.wg.Done()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		for {
			if ctx.Err() != nil {
				return
			}
			j := q.claim(p)
			if j == nil {
				break
			}
			q.run(q.root, j)
		}
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-poll.C:
		}
	}
}

func (q *Queue) claim(p *pool) *model.Job {
	var claimed *model.Job
	now := time.Now().UTC()
	q.db.Transaction(func(tx *gorm.DB) error {
		var j model.Job
		sel := tx.Where("lane IN ? AND status = ? AND run_at <= ?", p.Lanes, model.JobQueued, now)
		if len(p.Plans) > 0 {
			sel = sel.Where("(plan_code = '' OR plan_code IN ?)", p.Plans)
		}
		err := sel.Order("priority desc, created_at asc").Limit(1).Find(&j).Error
		if err != nil || j.ID == "" {
			return err
		}
		lease := now.Add(2 * time.Minute)
		res := tx.Model(&model.Job{}).Where("id = ? AND status = ?", j.ID, model.JobQueued).Updates(map[string]any{
			"status": model.JobRunning, "locked_by": q.workerID, "locked_until": lease, "queue_id": p.ID,
			"started_at": now, "attempts": gorm.Expr("attempts + 1"),
		})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		j.Status = model.JobRunning
		j.QueueID = p.ID
		j.Attempts++
		j.StartedAt = &now
		claimed = &j
		return nil
	})
	return claimed
}

func (q *Queue) run(parent context.Context, j *model.Job) {
	h, ok := q.handlers[j.Type]
	if !ok {
		q.finish(j, fmt.Errorf("%w: 未注册的任务类型 %s", ErrPermanent, j.Type))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	q.running.Store(j.ID, cancel)
	defer func() {
		q.running.Delete(j.ID)
		cancel()
	}()
	if q.OnUpdate != nil {
		q.OnUpdate(j)
	}
	// heartbeat keeps the lease alive for long jobs
	hbDone := make(chan struct{})
	go func() {
		t := time.NewTicker(45 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbDone:
				return
			case <-t.C:
				q.db.Model(&model.Job{}).Where("id = ?", j.ID).Update("locked_until", time.Now().UTC().Add(2*time.Minute))
			}
		}
	}()
	jc := &Context{Job: j, q: q}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("job panic", "type", j.Type, "id", j.ID, "panic", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("%w: 内部错误 %v", ErrPermanent, r)
			}
		}()
		err = h(ctx, jc)
	}()
	close(hbDone)
	if err != nil && parent.Err() != nil {
		q.requeueOnShutdown(j)
		return
	}
	q.finish(j, err)
}

// requeueOnShutdown hands a job interrupted by a server shutdown back to the queue, unless
// the user asked to cancel it. The attempt is not counted, so restarts never use up retries.
func (q *Queue) requeueOnShutdown(j *model.Job) {
	var cur model.Job
	q.db.Select("cancel_req").First(&cur, "id = ?", j.ID)
	if cur.CancelReq {
		q.finish(j, context.Canceled)
		return
	}
	q.db.Model(&model.Job{}).Where("id = ?", j.ID).Updates(map[string]any{
		"status": model.JobQueued, "locked_by": "", "locked_until": nil, "attempts": max(j.Attempts-1, 0),
		"progress_text": "服务重启，稍后自动继续",
	})
}

func (q *Queue) finish(j *model.Job, err error) {
	now := time.Now().UTC()
	var cur model.Job
	q.db.Select("cancel_req", "status").First(&cur, "id = ?", j.ID)
	up := map[string]any{"locked_by": "", "locked_until": nil}
	switch {
	case err == nil:
		up["status"] = model.JobSucceeded
		up["progress"] = 1
		up["finished_at"] = now
		up["error"] = ""
		j.Status = model.JobSucceeded
		j.Progress = 1
	case cur.CancelReq || errors.Is(err, context.Canceled):
		up["status"] = model.JobCanceled
		up["finished_at"] = now
		up["error"] = "已取消"
		j.Status = model.JobCanceled
	case errors.Is(err, errPaused):
		up["status"] = model.JobPaused
		j.Status = model.JobPaused
	case errors.Is(err, ErrPermanent) || j.Attempts >= j.MaxAttempts:
		up["status"] = model.JobFailed
		up["finished_at"] = now
		up["error"] = trimErr(err)
		j.Status = model.JobFailed
		j.Error = trimErr(err)
	default:
		backoff := time.Duration(j.Attempts*j.Attempts) * 10 * time.Second
		up["status"] = model.JobQueued
		up["run_at"] = now.Add(backoff)
		up["error"] = trimErr(err)
		up["progress_text"] = fmt.Sprintf("第 %d 次尝试失败，%s 后重试", j.Attempts, backoff)
		j.Status = model.JobQueued
		j.Error = trimErr(err)
	}
	q.db.Model(&model.Job{}).Where("id = ?", j.ID).Updates(up)
	if j.Status != model.JobQueued {
		j.FinishedAt = &now
	}
	if q.OnUpdate != nil {
		q.OnUpdate(j)
	}
	if q.OnFinish != nil && j.Status != model.JobQueued {
		q.OnFinish(j)
	}
}

var errPaused = errors.New("paused")

// Pause can be returned by a handler to park the job in paused state (resume re-queues it).
func Pause() error { return errPaused }

func (q *Queue) Resume(jobID string) error {
	res := q.db.Model(&model.Job{}).Where("id = ? AND status = ?", jobID, model.JobPaused).
		Updates(map[string]any{"status": model.JobQueued, "run_at": time.Now().UTC(), "cancel_req": false})
	if res.RowsAffected == 0 {
		return errors.New("任务不在暂停状态")
	}
	var j model.Job
	q.db.First(&j, "id = ?", jobID)
	q.Wake(j.Lane)
	q.tell("wake", j.Lane)
	return nil
}

func trimErr(err error) string {
	s := err.Error()
	if len(s) > 900 {
		s = s[:900]
	}
	return s
}
