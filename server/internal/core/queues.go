package core

import (
	"fmt"
	"slices"
	"strings"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// Task queues are set by the administrators (管理后台 → 任务队列): each one runs some kinds of jobs
// for some membership plans, a number at a time on each instance. A job can be taken by any queue
// whose kinds and plans both match it.

// LaneNames are what the kinds of jobs are called on screen.
var LaneNames = map[string]string{
	jobs.LaneDefault:  "文本与 AI 分析",
	jobs.LaneAudio:    "配音与合成",
	jobs.LanePipeline: "一键成书",
}

// laneHints say what each kind of job covers.
var laneHints = map[string]string{
	jobs.LaneDefault:  "角色分析、画本、拆章、简介、审听等调用大模型的任务",
	jobs.LaneAudio:    "配音、合成成品、导出、对轨、音效与配乐生成",
	jobs.LanePipeline: "一键成书的整本调度（每本书占一个位置，书内多章同时制作）",
}

const maxQueueConcurrency = 64

// seedQueues creates the default queues on first start, sized from VB_WORKERS: one per kind of job,
// open to every plan. Afterwards the administrators own them.
func (a *App) seedQueues() {
	var n int64
	a.DB.Model(&model.JobQueue{}).Count(&n)
	if n > 0 {
		return
	}
	w := a.Cfg.Workers
	for i, q := range []model.JobQueue{
		{Name: "文本与 AI 分析", Lanes: model.Strings(jobs.LaneDefault), Concurrency: max(2, w/2)},
		{Name: "配音与合成", Lanes: model.Strings(jobs.LaneAudio), Concurrency: max(2, w)},
		{Name: "一键成书", Lanes: model.Strings(jobs.LanePipeline), Concurrency: 2},
	} {
		q.Plans = model.Strings()
		q.Enabled = true
		q.SortOrder = i + 1
		a.DB.Create(&q)
	}
}

// loadQueues hands the enabled queues to the job runner. Without any (tests, or before seeding) the
// runner keeps its built-in pools.
func (a *App) loadQueues() {
	var qs []model.JobQueue
	a.DB.Where("enabled = ?", true).Order("sort_order asc, created_at asc").Find(&qs)
	if len(qs) == 0 {
		return
	}
	pools := make([]jobs.Pool, 0, len(qs))
	for _, q := range qs {
		pools = append(pools, jobs.Pool{ID: q.ID, Name: q.Name, Size: q.Concurrency, Lanes: q.Lanes.V, Plans: q.Plans.V})
	}
	a.Q.SetPools(pools)
}

// reloadQueues applies a queue change here and on every other instance.
func (a *App) reloadQueues() {
	a.loadQueues()
	a.publish(topicCache, cacheMsg{Kind: "queues"})
}

// QueueGap is a plan that cannot run one kind of job: no enabled queue takes it.
type QueueGap struct {
	Plan string `json:"plan"`
	Lane string `json:"lane"`
}

// queueGaps lists the plan × kind combinations no enabled queue serves.
func (a *App) queueGaps(qs []model.JobQueue) []QueueGap {
	var plans []model.Plan
	a.DB.Order("sort_order asc").Find(&plans)
	var gaps []QueueGap
	for _, p := range plans {
		for _, lane := range jobs.Lanes {
			served := false
			for _, q := range qs {
				if q.Enabled && q.Concurrency > 0 && slices.Contains(q.Lanes.V, lane) && (len(q.Plans.V) == 0 || slices.Contains(q.Plans.V, p.Code)) {
					served = true
					break
				}
			}
			if !served {
				gaps = append(gaps, QueueGap{Plan: p.Code, Lane: lane})
			}
		}
	}
	return gaps
}

func (a *App) describeGaps(gaps []QueueGap) string {
	names := a.planNames()
	var parts []string
	for _, g := range gaps {
		parts = append(parts, fmt.Sprintf("「%s」的%s任务", firstNonEmpty(names[g.Plan], g.Plan), LaneNames[g.Lane]))
	}
	if len(parts) > 4 {
		parts = append(parts[:4], fmt.Sprintf("等 %d 项", len(gaps)))
	}
	return strings.Join(parts, "、")
}

func (a *App) planNames() map[string]string {
	var plans []model.Plan
	a.DB.Select("code", "name").Find(&plans)
	out := map[string]string{}
	for _, p := range plans {
		out[p.Code] = p.Name
	}
	return out
}

// QueueInput is what an administrator sets on a queue.
type QueueInput struct {
	Name        string   `json:"name"`
	Lanes       []string `json:"lanes"`
	Plans       []string `json:"plans"`
	Concurrency int      `json:"concurrency"`
	Enabled     bool     `json:"enabled"`
	Note        string   `json:"note"`
}

func (a *App) normalizeQueue(in QueueInput) (model.JobQueue, error) {
	q := model.JobQueue{Name: strings.TrimSpace(in.Name), Concurrency: in.Concurrency, Enabled: in.Enabled, Note: truncateStr(strings.TrimSpace(in.Note), 190)}
	if q.Name == "" {
		return q, Bad("请填写队列名称")
	}
	var lanes []string
	for _, l := range jobs.Lanes { // known kinds only, in a fixed order
		if slices.Contains(in.Lanes, l) {
			lanes = append(lanes, l)
		}
	}
	if len(lanes) == 0 {
		return q, Bad("请至少选择一类任务")
	}
	if q.Concurrency < 1 || q.Concurrency > maxQueueConcurrency {
		return q, Bad("同时运行的任务数应在 1 到 %d 之间", maxQueueConcurrency)
	}
	names := a.planNames()
	var plans []string
	for _, p := range in.Plans {
		if _, ok := names[p]; ok && !slices.Contains(plans, p) {
			plans = append(plans, p)
		}
	}
	q.Lanes = model.Strings(lanes...)
	q.Plans = model.Strings(plans...)
	return q, nil
}

// SaveQueue creates (id == "") or updates a queue. A change that would leave some plan without a
// queue for some kind of job is refused: those jobs would wait forever.
func (a *App) SaveQueue(id string, in QueueInput) (*model.JobQueue, error) {
	q, err := a.normalizeQueue(in)
	if err != nil {
		return nil, err
	}
	var all []model.JobQueue
	a.DB.Find(&all)
	if id != "" {
		found := false
		for i := range all {
			if all[i].ID == id {
				q.Base, q.SortOrder = all[i].Base, all[i].SortOrder
				all[i] = q
				found = true
			}
		}
		if !found {
			return nil, NotFound("队列")
		}
	} else {
		q.SortOrder = len(all) + 1
		all = append(all, q)
	}
	if gaps := a.queueGaps(all); len(gaps) > 0 {
		return nil, Bad("这样设置后，%s将没有队列可用，会一直排队。请先让其他队列覆盖它们", a.describeGaps(gaps))
	}
	if id == "" {
		err = a.DB.Create(&q).Error
	} else {
		err = a.DB.Model(&model.JobQueue{}).Where("id = ?", id).Updates(map[string]any{
			"name": q.Name, "lanes": q.Lanes, "plans": q.Plans, "concurrency": q.Concurrency, "enabled": q.Enabled, "note": q.Note,
		}).Error
	}
	if err != nil {
		return nil, err
	}
	a.reloadQueues()
	return &q, nil
}

// DeleteQueue removes a queue, unless that leaves a plan without one.
func (a *App) DeleteQueue(id string) error {
	var all, rest []model.JobQueue
	a.DB.Find(&all)
	for _, q := range all {
		if q.ID != id {
			rest = append(rest, q)
		}
	}
	if len(rest) == len(all) {
		return NotFound("队列")
	}
	if gaps := a.queueGaps(rest); len(gaps) > 0 {
		return Bad("删除后，%s将没有队列可用。请先让其他队列覆盖它们", a.describeGaps(gaps))
	}
	if err := a.DB.Delete(&model.JobQueue{}, "id = ?", id).Error; err != nil {
		return err
	}
	a.reloadQueues()
	return nil
}

// QueueOverview is the administrators' view: every queue with what it is doing, the kinds and plans
// to choose from, and which queues each plan can use.
func (a *App) QueueOverview() map[string]any {
	var qs []model.JobQueue
	a.DB.Order("sort_order asc, created_at asc").Find(&qs)
	type count struct {
		QueueID string
		N       int64
	}
	var running []count
	a.DB.Model(&model.Job{}).Select("queue_id, COUNT(*) AS n").Where("status = ?", model.JobRunning).Group("queue_id").Scan(&running)
	runningBy := map[string]int64{}
	for _, c := range running {
		runningBy[c.QueueID] = c.N
	}
	items := make([]map[string]any, 0, len(qs))
	for _, q := range qs {
		var waiting int64
		sel := a.DB.Model(&model.Job{}).Where("status = ? AND lane IN ?", model.JobQueued, q.Lanes.V)
		if len(q.Plans.V) > 0 {
			sel = sel.Where("(plan_code = '' OR plan_code IN ?)", q.Plans.V)
		}
		sel.Count(&waiting)
		items = append(items, map[string]any{
			"id": q.ID, "name": q.Name, "lanes": q.Lanes.V, "plans": q.Plans.V, "concurrency": q.Concurrency, "enabled": q.Enabled, "note": q.Note,
			"running": runningBy[q.ID], "waiting": waiting,
		})
	}
	var plans []model.Plan
	a.DB.Order("sort_order asc").Find(&plans)
	planList := make([]map[string]any, 0, len(plans))
	coverage := []map[string]any{}
	for _, p := range plans {
		planList = append(planList, map[string]any{"code": p.Code, "name": p.Name, "queuePriority": p.QueuePriority})
		row := map[string]any{"plan": p.Code}
		for _, lane := range jobs.Lanes {
			var names []string
			slots := 0
			for _, q := range qs {
				if q.Enabled && slices.Contains(q.Lanes.V, lane) && (len(q.Plans.V) == 0 || slices.Contains(q.Plans.V, p.Code)) {
					names = append(names, q.Name)
					slots += q.Concurrency
				}
			}
			row[lane] = map[string]any{"queues": names, "slots": slots}
		}
		coverage = append(coverage, row)
	}
	lanes := make([]map[string]any, 0, len(jobs.Lanes))
	for _, l := range jobs.Lanes {
		lanes = append(lanes, map[string]any{"key": l, "name": LaneNames[l], "hint": laneHints[l]})
	}
	return map[string]any{"queues": items, "plans": planList, "lanes": lanes, "coverage": coverage, "gaps": a.queueGaps(qs), "maxConcurrency": maxQueueConcurrency}
}
