package core

import (
	"fmt"
	"time"

	"voicebook/internal/model"
)

var DefaultWorkflows = []model.WorkflowTemplate{
	{Name: "AI 多播", Note: "画本 → AI 配音 → 后期 → 审听，适合全 AI 制作", IsDefault: true, Stages: model.JSON[[]model.WorkflowStage]{V: []model.WorkflowStage{
		{Key: "script", Name: "画本", Kind: "script", DefaultDays: 2},
		{Key: "tts", Name: "AI 配音", Kind: "tts", DefaultDays: 1},
		{Key: "post", Name: "后期", Kind: "post", DefaultDays: 2},
		{Key: "review", Name: "审听", Kind: "review", NeedsApproval: false, DefaultDays: 2},
	}}},
	{Name: "真人精品", Note: "画本 → 真人录音 → 对轨 → 后期 → 审听", Stages: model.JSON[[]model.WorkflowStage]{V: []model.WorkflowStage{
		{Key: "script", Name: "画本", Kind: "script", DefaultDays: 2},
		{Key: "record", Name: "真人录音", Kind: "record", NeedsApproval: true, DefaultDays: 5},
		{Key: "align", Name: "对轨", Kind: "align", DefaultDays: 1},
		{Key: "post", Name: "后期", Kind: "post", DefaultDays: 3},
		{Key: "review", Name: "审听", Kind: "review", DefaultDays: 2},
	}}},
	{Name: "人机混编", Note: "主角真人、配角与旁白 AI，最常见的精品做法", Stages: model.JSON[[]model.WorkflowStage]{V: []model.WorkflowStage{
		{Key: "script", Name: "画本", Kind: "script", DefaultDays: 2},
		{Key: "voice", Name: "配音（AI + 真人）", Kind: "record", NeedsApproval: true, DefaultDays: 5},
		{Key: "post", Name: "后期", Kind: "post", DefaultDays: 3},
		{Key: "review", Name: "审听", Kind: "review", DefaultDays: 2},
	}}},
}

func (a *App) EnableWorkflow(x *Access, templateID string, stages []model.WorkflowStage) error {
	if err := need(x, PermManage); err != nil {
		return err
	}
	if err := a.RequireFeature(x.StudioID, model.FeatProduction, "生产管理"); err != nil {
		return err
	}
	if len(stages) == 0 {
		var t model.WorkflowTemplate
		if err := a.DB.First(&t, "id = ? AND (scope = 'system' OR studio_id = ?)", templateID, x.StudioID).Error; err != nil {
			return NotFound("工作流模板")
		}
		stages = t.Stages.V
	}
	for i := range stages {
		if stages[i].Key == "" {
			stages[i].Key = fmt.Sprintf("s%d", i+1)
		}
	}
	a.DB.Model(x.Book).Updates(map[string]any{"workflow_on": true, "workflow": model.JSON[[]model.WorkflowStage]{V: stages}})
	x.Book.WorkflowOn, x.Book.Workflow.V = true, stages
	// drop tasks for stages that no longer exist
	var keys []string
	for _, s := range stages {
		keys = append(keys, s.Key)
	}
	a.DB.Where("book_id = ? AND stage_key NOT IN ? AND status <> ?", x.Book.ID, keys, model.TaskDone).Delete(&model.ProductionTask{})
	a.EnsureChapterTasks(x.Book.ID)
	a.Activity(x.StudioID, x.Book.ID, x.User.ID, "workflow.enable", "book", x.Book.ID, "启用了生产工作流")
	return nil
}

func (a *App) DisableWorkflow(x *Access) error {
	if err := need(x, PermManage); err != nil {
		return err
	}
	return a.DB.Model(x.Book).Update("workflow_on", false).Error
}

// EnsureChapterTasks creates missing tasks (chapter × stage, record stages split by human characters).
func (a *App) EnsureChapterTasks(bookID string) {
	var b model.Book
	if a.DB.First(&b, "id = ?", bookID).Error != nil || !b.WorkflowOn || len(b.Workflow.V) == 0 {
		return
	}
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND included = ?", bookID, true).Order("\"index\" asc").Find(&chs)
	var humans []model.Character
	a.DB.Where("book_id = ? AND mode = ? AND candidate = ?", bookID, "human", false).Find(&humans)
	for _, ch := range chs {
		for si, st := range b.Workflow.V {
			if st.Kind == "record" && len(humans) > 0 {
				for _, c := range humans {
					// only characters that speak in this chapter (or unknown yet)
					if ch.CurrentScriptID != "" {
						var n int64
						a.DB.Model(&model.Segment{}).Where("script_id = ? AND character_id = ?", ch.CurrentScriptID, c.ID).Count(&n)
						if n == 0 {
							continue
						}
					}
					a.ensureTask(&b, &ch, si, st, c.ID, c.AssigneeID)
				}
				continue
			}
			assignee := st.DefaultAssigneeID
			if assignee == "" {
				assignee = b.LeadID
			}
			a.ensureTask(&b, &ch, si, st, "", assignee)
		}
	}
}

func (a *App) ensureTask(b *model.Book, ch *model.Chapter, idx int, st model.WorkflowStage, characterID, assignee string) {
	var t model.ProductionTask
	if a.DB.Where("chapter_id = ? AND stage_key = ? AND character_id = ?", ch.ID, st.Key, characterID).First(&t).Error == nil {
		if t.AssigneeID == "" && assignee != "" {
			a.DB.Model(&t).Update("assignee_id", assignee)
		}
		return
	}
	t = model.ProductionTask{StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, StageKey: st.Key, StageIndex: idx, StageName: st.Name, StageKind: st.Kind, CharacterID: characterID,
		AssigneeID: assignee, Status: model.TaskPending, NeedsApproval: st.NeedsApproval, MeasureUnit: "words", Measure: int64(ch.WordCount)}
	if characterID != "" {
		var chars int64
		a.DB.Model(&model.Segment{}).Where("script_id = ? AND character_id = ?", ch.CurrentScriptID, characterID).Select("COALESCE(SUM(LENGTH(text)),0)").Scan(&chars)
		if chars > 0 {
			t.Measure = chars
		}
	}
	if st.Kind == "post" {
		t.MeasureUnit = "minutes"
		t.Measure = ch.DurationMs / 60000
	}
	if idx == 0 && st.DefaultDays > 0 {
		due := now().AddDate(0, 0, st.DefaultDays)
		t.DueAt = &due
	}
	a.DB.Create(&t)
}

// CurrentStage returns the index of the first unfinished stage for a chapter (-1 when all done).
func (a *App) CurrentStage(chapterID string) int {
	var tasks []model.ProductionTask
	a.DB.Where("chapter_id = ?", chapterID).Order("stage_index asc").Find(&tasks)
	for _, t := range tasks {
		if t.Status != model.TaskDone {
			return t.StageIndex
		}
	}
	return -1
}

type TaskAction struct {
	Action     string     `json:"action"` // start | submit | approve | reject | reassign | due | reopen
	Reason     string     `json:"reason"`
	AssigneeID string     `json:"assigneeId"`
	DueAt      *time.Time `json:"dueAt"`
}

func (a *App) ActOnTask(x *Access, t *model.ProductionTask, act TaskAction) error {
	isManager := x.Can(PermManage)
	mine := t.AssigneeID == x.User.ID
	set := map[string]any{}
	switch act.Action {
	case "start":
		if !mine && !isManager {
			return Forbidden("只能开始分配给自己的任务")
		}
		if t.Status != model.TaskPending && t.Status != model.TaskRejected {
			return Bad("任务当前状态不能开始")
		}
		set["status"], set["started_at"] = model.TaskInProgress, ptrTime(now())
	case "submit":
		if !mine && !isManager {
			return Forbidden("只能提交分配给自己的任务")
		}
		if t.Status == model.TaskDone {
			return Bad("任务已完成")
		}
		if err := a.validateStageDone(t); err != nil {
			return err
		}
		set["submitted_at"] = ptrTime(now())
		if t.NeedsApproval {
			set["status"] = model.TaskReviewing
			a.NotifyMany([]string{x.Book.LeadID}, t.StudioID, "task_review", "有任务等待你审核", fmt.Sprintf("「%s」%s 已提交", t.StageName, a.chapterTitle(t.ChapterID)), "/production")
		} else {
			set["status"], set["completed_at"] = model.TaskDone, ptrTime(now())
		}
	case "approve":
		if !isManager {
			return Forbidden("只有负责人可以审核任务")
		}
		if t.Status != model.TaskReviewing {
			return Bad("任务不在待审核状态")
		}
		set["status"], set["completed_at"], set["approved_by"] = model.TaskDone, ptrTime(now()), x.User.ID
	case "reject":
		if !isManager {
			return Forbidden("只有负责人可以打回任务")
		}
		if act.Reason == "" {
			return Bad("请填写打回原因")
		}
		set["status"], set["reject_reason"], set["reworks"] = model.TaskRejected, act.Reason, t.Reworks+1
		a.Notify(t.AssigneeID, t.StudioID, "task_rejected", "任务被打回："+t.StageName, act.Reason, "/production")
	case "reassign":
		if !isManager {
			return Forbidden("只有负责人可以改派任务")
		}
		set["assignee_id"] = act.AssigneeID
		if t.CharacterID != "" {
			a.DB.Model(&model.Character{}).Where("id = ?", t.CharacterID).Update("assignee_id", act.AssigneeID)
		}
		a.Notify(act.AssigneeID, t.StudioID, "task_assigned", "你有新任务："+t.StageName, a.chapterTitle(t.ChapterID), "/production")
	case "due":
		if !isManager {
			return Forbidden("")
		}
		set["due_at"] = act.DueAt
		set["reminded_at"] = nil
	case "reopen":
		if !isManager {
			return Forbidden("")
		}
		set["status"], set["completed_at"] = model.TaskInProgress, nil
	default:
		return Bad("未知操作")
	}
	if err := a.DB.Model(t).Updates(set).Error; err != nil {
		return err
	}
	a.DB.First(t, "id = ?", t.ID)
	if t.Status == model.TaskDone {
		a.onStageProgress(t)
	}
	a.emit(t.StudioID, eventBook(t.BookID, "tasks"))
	return nil
}

func (a *App) chapterTitle(id string) string {
	var ch model.Chapter
	a.DB.Select("title").First(&ch, "id = ?", id)
	return ch.Title
}

// validateStageDone prevents submitting a stage whose work is clearly unfinished.
func (a *App) validateStageDone(t *model.ProductionTask) error {
	var ch model.Chapter
	a.DB.First(&ch, "id = ?", t.ChapterID)
	switch t.StageKind {
	case "script":
		if ch.CurrentScriptID == "" {
			return Bad("画本还没有生成")
		}
		var n int64
		a.DB.Model(&model.Segment{}).Where("script_id = ? AND needs_review = ?", ch.CurrentScriptID, true).Count(&n)
		if n > 0 {
			return Bad("还有 %d 处说话人待确认", n)
		}
	case "tts", "record":
		q := a.DB.Model(&model.Segment{}).Where("script_id = ? AND removed = ? AND type NOT IN ? AND audio_status <> ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause}, model.AudioReady)
		if t.CharacterID != "" {
			q = q.Where("character_id = ?", t.CharacterID)
		}
		var n int64
		q.Count(&n)
		if n > 0 {
			return Bad("还有 %d 句没有音频", n)
		}
	case "post":
		if ch.RenderID == "" || ch.RenderStale {
			return Bad("请先合成最新的成品")
		}
		a.DB.Model(t).Update("measure", ch.DurationMs/60000+1)
	}
	return nil
}

// onStageProgress activates the next stage when every task of the current stage is done.
func (a *App) onStageProgress(t *model.ProductionTask) {
	var left int64
	a.DB.Model(&model.ProductionTask{}).Where("chapter_id = ? AND stage_index = ? AND status <> ?", t.ChapterID, t.StageIndex, model.TaskDone).Count(&left)
	if left > 0 {
		return
	}
	var next []model.ProductionTask
	a.DB.Where("chapter_id = ? AND stage_index = ?", t.ChapterID, t.StageIndex+1).Find(&next)
	var b model.Book
	a.DB.First(&b, "id = ?", t.BookID)
	for _, n := range next {
		up := map[string]any{}
		if n.Status == model.TaskPending && n.DueAt == nil && n.StageIndex < len(b.Workflow.V) {
			if d := b.Workflow.V[n.StageIndex].DefaultDays; d > 0 {
				due := now().AddDate(0, 0, d)
				up["due_at"] = &due
			}
		}
		if len(up) > 0 {
			a.DB.Model(&n).Updates(up)
		}
		a.Notify(n.AssigneeID, n.StudioID, "task_ready", "轮到你了："+n.StageName, fmt.Sprintf("《%s》%s 的上一环节已完成", b.Title, a.chapterTitle(n.ChapterID)), "/production")
	}
}

// AdvanceTaskOnEvent nudges tasks forward on production events (script ready, render, review pass…).
func (a *App) AdvanceTaskOnEvent(chapterID, event string) {
	var tasks []model.ProductionTask
	a.DB.Where("chapter_id = ?", chapterID).Find(&tasks)
	for i := range tasks {
		t := &tasks[i]
		switch {
		case event == "review" && t.StageKind == "review" && t.Status != model.TaskDone:
			a.DB.Model(t).Updates(map[string]any{"status": model.TaskDone, "completed_at": ptrTime(now())})
			a.onStageProgress(t)
		case event == "post" && t.StageKind == "post" && t.Status == model.TaskPending:
			a.DB.Model(t).Updates(map[string]any{"status": model.TaskInProgress, "started_at": ptrTime(now())})
		case event == "record:"+t.CharacterID && t.StageKind == "record" && (t.Status == model.TaskPending || t.Status == model.TaskInProgress || t.Status == model.TaskRejected):
			a.DB.Model(t).Updates(map[string]any{"status": model.TaskInProgress})
			a.Notify(t.AssigneeID, t.StudioID, "task_ready_submit", "录音已全部完成", a.chapterTitle(chapterID)+" 你负责的台词都已录完，可以提交任务了", "/my-lines")
		}
	}
}

// RejectToStage sends a chapter back to an earlier stage after review.
func (a *App) RejectToStage(x *Access, ch *model.Chapter, stageKey, reason string) {
	var tasks []model.ProductionTask
	a.DB.Where("chapter_id = ?", ch.ID).Order("stage_index asc").Find(&tasks)
	target := -1
	for _, t := range tasks {
		if t.StageKey == stageKey {
			target = t.StageIndex
		}
	}
	if target < 0 {
		return
	}
	for i := range tasks {
		t := &tasks[i]
		switch {
		case t.StageIndex == target:
			a.DB.Model(t).Updates(map[string]any{"status": model.TaskRejected, "reject_reason": reason, "reworks": t.Reworks + 1, "completed_at": nil})
			a.Notify(t.AssigneeID, t.StudioID, "task_rejected", "章节被打回："+t.StageName, reason, "/production")
		case t.StageIndex > target:
			a.DB.Model(t).Updates(map[string]any{"status": model.TaskPending, "completed_at": nil})
		}
	}
}

// RemindTasks notifies assignees about upcoming and overdue deadlines (runs from cron).
func (a *App) RemindTasks() {
	t := now()
	var soon []model.ProductionTask
	a.DB.Where("status IN ? AND due_at IS NOT NULL AND due_at < ? AND (reminded_at IS NULL OR reminded_at < ?)", []string{model.TaskPending, model.TaskInProgress, model.TaskRejected}, t.Add(24*time.Hour), t.Add(-20*time.Hour)).Find(&soon)
	for _, task := range soon {
		var b model.Book
		a.DB.Select("title", "lead_id").First(&b, "id = ?", task.BookID)
		title := fmt.Sprintf("《%s》%s · %s", b.Title, a.chapterTitle(task.ChapterID), task.StageName)
		if task.DueAt.Before(t) {
			a.NotifyMany([]string{task.AssigneeID, b.LeadID}, task.StudioID, "task_overdue", "任务已逾期", title, "/production")
		} else {
			a.Notify(task.AssigneeID, task.StudioID, "task_due", "任务即将到期", title+"（"+task.DueAt.Local().Format("01-02 15:04")+"）", "/production")
		}
		a.DB.Model(&task).Update("reminded_at", &t)
	}
}
