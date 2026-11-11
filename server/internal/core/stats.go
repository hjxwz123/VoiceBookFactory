package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"voicebook/internal/export"
	"voicebook/internal/model"
)

func writeFile(p string, b []byte) error { return os.WriteFile(p, b, 0o644) }

type MemberStat struct {
	UserID   string  `json:"userId"`
	Nickname string  `json:"nickname"`
	Tasks    int     `json:"tasks"`
	Words    int64   `json:"words"`
	Minutes  int64   `json:"minutes"`
	Reworks  int     `json:"reworks"`
	Active   int     `json:"active"`
	PassRate float64 `json:"passRate"`
	Credits  int64   `json:"credits"`
}

// StudioStats aggregates production, quality and cost metrics over a period.
func (a *App) StudioStats(studioID string, from, to time.Time, userID string) map[string]any {
	var tasks []model.ProductionTask
	q := a.DB.Where("studio_id = ? AND status = ? AND completed_at BETWEEN ? AND ?", studioID, model.TaskDone, from, to)
	if userID != "" {
		q = q.Where("assignee_id = ?", userID)
	}
	q.Find(&tasks)
	members := map[string]*MemberStat{}
	get := func(id string) *MemberStat {
		if m, ok := members[id]; ok {
			return m
		}
		m := &MemberStat{UserID: id}
		var u model.User
		if a.DB.Select("nickname").First(&u, "id = ?", id).Error == nil {
			m.Nickname = u.Nickname
		}
		members[id] = m
		return m
	}
	var words int64
	byDay := map[string]int64{}
	reworkTasks := 0
	for _, t := range tasks {
		if t.AssigneeID == "" {
			continue
		}
		m := get(t.AssigneeID)
		m.Tasks++
		m.Reworks += t.Reworks
		if t.Reworks > 0 {
			reworkTasks++
		}
		if t.MeasureUnit == "minutes" {
			m.Minutes += t.Measure
		} else {
			m.Words += t.Measure
			words += t.Measure
			byDay[t.CompletedAt.Format("2006-01-02")] += t.Measure
		}
	}
	var active []model.ProductionTask
	a.DB.Where("studio_id = ? AND status IN ?", studioID, []string{model.TaskPending, model.TaskInProgress, model.TaskReviewing, model.TaskRejected}).Find(&active)
	for _, t := range active {
		if t.AssigneeID != "" && (userID == "" || t.AssigneeID == userID) {
			get(t.AssigneeID).Active++
		}
	}
	for _, m := range members {
		if m.Tasks > 0 {
			m.PassRate = float64(m.Tasks-min(m.Reworks, m.Tasks)) / float64(m.Tasks)
		}
		a.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND user_id = ? AND created_at BETWEEN ? AND ?", studioID, m.UserID, from, to).Select("COALESCE(SUM(credits),0)").Scan(&m.Credits)
	}
	list := make([]*MemberStat, 0, len(members))
	for _, m := range members {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Words+list[i].Minutes*200 > list[j].Words+list[j].Minutes*200 })
	days := int(to.Sub(from).Hours()/24) + 1
	var series []map[string]any
	for d := 0; d < days && d < 400; d++ {
		day := from.AddDate(0, 0, d).Format("2006-01-02")
		series = append(series, map[string]any{"date": day, "words": byDay[day]})
	}
	// reviews: chapters passed vs rejected in period
	var passed, rejected int64
	a.DB.Model(&model.ActivityLog{}).Where("studio_id = ? AND action = ? AND created_at BETWEEN ? AND ?", studioID, "review.pass", from, to).Count(&passed)
	a.DB.Model(&model.ActivityLog{}).Where("studio_id = ? AND action = ? AND created_at BETWEEN ? AND ?", studioID, "review.reject", from, to).Count(&rejected)
	firstPass := 0.0
	if passed+rejected > 0 {
		firstPass = float64(passed) / float64(passed+rejected)
	}
	// cost
	type fr struct {
		Feature string
		Credits int64
	}
	var byFeature []fr
	a.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND created_at BETWEEN ? AND ?", studioID, from, to).Select("feature, COALESCE(SUM(credits),0) AS credits").Group("feature").Scan(&byFeature)
	var features []map[string]any
	var totalCredits int64
	for _, f := range byFeature {
		totalCredits += f.Credits
		features = append(features, map[string]any{"feature": f.Feature, "name": FeatureNames[f.Feature], "credits": f.Credits})
	}
	sort.Slice(features, func(i, j int) bool { return features[i]["credits"].(int64) > features[j]["credits"].(int64) })
	type br struct {
		BookID  string
		Credits int64
	}
	var byBook []br
	a.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND book_id <> '' AND created_at BETWEEN ? AND ?", studioID, from, to).Select("book_id, COALESCE(SUM(credits),0) AS credits").Group("book_id").Order("credits desc").Limit(10).Scan(&byBook)
	var books []map[string]any
	for _, r := range byBook {
		var b model.Book
		a.DB.Select("title", "word_count").First(&b, "id = ?", r.BookID)
		per10k := int64(0)
		if b.WordCount > 0 {
			per10k = r.Credits * 10000 / int64(b.WordCount)
		}
		books = append(books, map[string]any{"bookId": r.BookID, "title": b.Title, "credits": r.Credits, "per10k": per10k, "words": b.WordCount})
	}
	productivity := float64(0)
	if days > 0 {
		productivity = float64(words) / float64(days)
	}
	reworkRate := 0.0
	if len(tasks) > 0 {
		reworkRate = float64(reworkTasks) / float64(len(tasks))
	}
	return map[string]any{
		"from": from, "to": to, "tasksDone": len(tasks), "words": words, "productivity": productivity, "reworkRate": reworkRate,
		"firstPassRate": firstPass, "reviewsPassed": passed, "reviewsRejected": rejected, "members": list, "series": series,
		"credits": totalCredits, "byFeature": features, "byBook": books,
	}
}

type PayrollRow struct {
	TaskID    string `json:"taskId"`
	UserID    string `json:"userId"`
	Nickname  string `json:"nickname"`
	Book      string `json:"book"`
	Chapter   string `json:"chapter"`
	Stage     string `json:"stage"`
	Character string `json:"character"`
	Measure   int64  `json:"measure"`
	Unit      string `json:"unit"`
	Rate      int64  `json:"rateCent"`
	RateUnit  string `json:"rateUnit"`
	Amount    int64  `json:"amountCent"`
	Completed string `json:"completedAt"`
	Settled   bool   `json:"settled"`
	Reworks   int    `json:"reworks"`
}

func (a *App) payRate(studioID, userID, stageKey, stageKind string) (model.PayRate, bool) {
	var rates []model.PayRate
	a.DB.Where("studio_id = ?", studioID).Find(&rates)
	best := -1
	var pick model.PayRate
	for _, r := range rates {
		score := -1
		switch {
		case r.UserID == userID && (r.StageKey == stageKey || r.StageKind == stageKind):
			score = 3
		case r.UserID == "" && r.StageKey == stageKey:
			score = 2
		case r.UserID == "" && r.StageKind == stageKind:
			score = 1
		}
		if score > best {
			best, pick = score, r
		}
	}
	return pick, best >= 0
}

// Payroll computes amounts for completed tasks in a period.
func (a *App) Payroll(studioID string, from, to time.Time, includeSettled bool) ([]PayrollRow, map[string]int64) {
	var tasks []model.ProductionTask
	q := a.DB.Where("studio_id = ? AND status = ? AND completed_at BETWEEN ? AND ?", studioID, model.TaskDone, from, to)
	if !includeSettled {
		q = q.Where("settled = ?", false)
	}
	q.Order("completed_at asc").Find(&tasks)
	names := map[string]string{}
	name := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		var u model.User
		a.DB.Select("nickname").First(&u, "id = ?", id)
		names[id] = u.Nickname
		return u.Nickname
	}
	var rows []PayrollRow
	totals := map[string]int64{}
	for _, t := range tasks {
		if t.AssigneeID == "" {
			continue
		}
		var b model.Book
		a.DB.Select("title").First(&b, "id = ?", t.BookID)
		var c model.Character
		if t.CharacterID != "" {
			a.DB.Select("name").First(&c, "id = ?", t.CharacterID)
		}
		row := PayrollRow{TaskID: t.ID, UserID: t.AssigneeID, Nickname: name(t.AssigneeID), Book: b.Title, Chapter: a.chapterTitle(t.ChapterID), Stage: t.StageName, Character: c.Name,
			Measure: t.Measure, Unit: t.MeasureUnit, Completed: t.CompletedAt.Local().Format("2006-01-02 15:04"), Settled: t.Settled, Reworks: t.Reworks}
		if r, ok := a.payRate(studioID, t.AssigneeID, t.StageKey, t.StageKind); ok {
			row.Rate, row.RateUnit = r.PriceCent, r.Unit
			switch r.Unit {
			case "per_1k_words":
				row.Amount = t.Measure * r.PriceCent / 1000
				if t.MeasureUnit == "minutes" {
					row.Amount = 0
				}
			case "per_minute":
				if t.MeasureUnit == "minutes" {
					row.Amount = t.Measure * r.PriceCent
				} else {
					row.Amount = t.Measure * r.PriceCent / 220 // words -> minutes estimate
				}
			case "per_episode":
				row.Amount = r.PriceCent
			}
		}
		rows = append(rows, row)
		totals[row.UserID] += row.Amount
	}
	return rows, totals
}

func (a *App) PayrollXLSX(studioID string, from, to time.Time) (string, string, error) {
	rows, totals := a.Payroll(studioID, from, to, true)
	unit := map[string]string{"words": "字", "minutes": "分钟"}
	rateUnit := map[string]string{"per_1k_words": "元/千字", "per_minute": "元/分钟", "per_episode": "元/集"}
	var detail [][]any
	for _, r := range rows {
		settled := "未结算"
		if r.Settled {
			settled = "已结算"
		}
		detail = append(detail, []any{r.Nickname, r.Book, r.Chapter, r.Stage, r.Character, r.Measure, unit[r.Unit], float64(r.Rate) / 100, rateUnit[r.RateUnit], float64(r.Amount) / 100, r.Completed, r.Reworks, settled})
	}
	var summary [][]any
	for uid, amt := range totals {
		var u model.User
		a.DB.Select("nickname").First(&u, "id = ?", uid)
		summary = append(summary, []any{u.Nickname, float64(amt) / 100})
	}
	dir, err := a.Store.TempDir("payroll")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "payroll.xlsx")
	if err := export.WriteXLSX(p, []export.Sheet{
		{Name: "汇总", Header: []string{"成员", "应付金额（元）"}, Rows: summary, Widths: []float64{20, 16}},
		{Name: "明细", Header: []string{"成员", "图书", "章节", "环节", "角色", "计量", "单位", "单价", "计价方式", "金额（元）", "完成时间", "返修次数", "状态"}, Rows: detail, Widths: []float64{12, 20, 18, 10, 10, 10, 6, 8, 10, 10, 18, 8, 8}},
	}); err != nil {
		return "", "", err
	}
	name := fmt.Sprintf("薪酬报表_%s_%s.xlsx", from.Format("20060102"), to.Format("20060102"))
	asset, err := a.SaveAssetFile(nil2ctx(), AssetInput{StudioID: studioID, Kind: model.AssetExport, Name: name, Ext: "xlsx", Origin: "generated", NoProbe: true}, p)
	if err != nil {
		return "", "", err
	}
	return a.Store.SignedPath(asset.StorageKey, 2*time.Hour, name), name, nil
}

func (a *App) SettleTasks(studioID string, ids []string) int64 {
	t := now()
	res := a.DB.Model(&model.ProductionTask{}).Where("studio_id = ? AND id IN ? AND status = ?", studioID, ids, model.TaskDone).Updates(map[string]any{"settled": true, "settled_at": &t})
	return res.RowsAffected
}
