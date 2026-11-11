package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

func (s *Server) miscRoutes(r chi.Router) {
	// ---- CV workbench: my lines ----
	r.Get("/me/lines", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var chars []model.Character
		q := s.app.DB.Where("assignee_id = ? AND mode = ?", u.ID, "human")
		if bid := r.URL.Query().Get("bookId"); bid != "" {
			q = q.Where("book_id = ?", bid)
		}
		q.Find(&chars)
		var books []map[string]any
		var lines []map[string]any
		status := r.URL.Query().Get("status")
		chapterFilter := r.URL.Query().Get("chapterId")
		seenBooks := map[string]bool{}
		for _, c := range chars {
			var b model.Book
			if s.app.DB.First(&b, "id = ? AND deleted_at IS NULL", c.BookID).Error != nil {
				continue
			}
			if !seenBooks[b.ID] {
				seenBooks[b.ID] = true
				books = append(books, map[string]any{"id": b.ID, "title": b.Title, "cover": s.app.AssetURLByID(b.CoverAssetID)})
			}
			var chs []model.Chapter
			cq := s.app.DB.Where("book_id = ? AND included = ? AND current_script_id <> ''", b.ID, true).Order("\"index\" asc")
			if chapterFilter != "" {
				cq = cq.Where("id = ?", chapterFilter)
			}
			cq.Find(&chs)
			for _, ch := range chs {
				segs := s.app.SegmentViews(func() []model.Segment {
					var list []model.Segment
					s.app.DB.Where("script_id = ?", ch.CurrentScriptID).Order("seq asc").Find(&list)
					return list
				}())
				for i, sg := range segs {
					if sg["characterId"] != c.ID || sg["removed"] == true {
						continue
					}
					st := "todo"
					if sg["audioStatus"] == model.AudioReady {
						st = "recorded"
					}
					var openIssues int64
					s.app.DB.Model(&model.Issue{}).Where("segment_id = ? AND status IN ?", sg["id"], []string{model.IssueOpen, model.IssueInProgress}).Count(&openIssues)
					if openIssues > 0 {
						st = "rework"
					}
					if status != "" && status != st {
						continue
					}
					var before, after []string
					for k := max(0, i-2); k < i; k++ {
						before = append(before, segs[k]["text"].(string))
					}
					for k := i + 1; k < len(segs) && k <= i+2; k++ {
						after = append(after, segs[k]["text"].(string))
					}
					sg["status"] = st
					sg["before"], sg["after"] = before, after
					sg["bookId"], sg["bookTitle"], sg["chapterTitle"], sg["chapterIndex"] = b.ID, b.Title, ch.Title, ch.Index
					sg["characterName"], sg["characterColor"] = c.Name, c.Color
					sg["openIssues"] = openIssues
					lines = append(lines, sg)
					if len(lines) >= 2000 {
						break
					}
				}
			}
		}
		var cv []map[string]any
		for _, c := range chars {
			cv = append(cv, map[string]any{"id": c.ID, "name": c.Name, "color": c.Color, "bookId": c.BookID})
		}
		return map[string]any{"books": books, "characters": cv, "lines": lines}, nil
	}))
	r.Get("/me/tasks", h(func(r *http.Request) (any, error) {
		var list []model.ProductionTask
		s.app.DB.Where("assignee_id = ? AND status <> ?", userOf(r).ID, model.TaskDone).Order("due_at asc").Limit(100).Find(&list)
		return s.taskViews(list), nil
	}))
	r.Get("/me/issues", h(func(r *http.Request) (any, error) {
		var list []model.Issue
		s.app.DB.Where("assignee_id = ? AND status IN ?", userOf(r).ID, []string{model.IssueOpen, model.IssueInProgress, model.IssueDisputed}).Order("created_at desc").Limit(100).Find(&list)
		return s.issueViews(list), nil
	}))
	// ---- studio-wide production views ----
	r.Get("/studios/{sid}/tasks", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Where("studio_id = ?", x.StudioID)
		if !x.IsManager() && x.Role != model.RoleProducer || r.URL.Query().Get("mine") == "1" {
			q = q.Where("assignee_id = ?", x.User.ID)
		}
		if b := r.URL.Query().Get("bookId"); b != "" {
			q = q.Where("book_id = ?", b)
		}
		if a := r.URL.Query().Get("assigneeId"); a != "" {
			q = q.Where("assignee_id = ?", a)
		}
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status IN ?", strings.Split(st, ","))
		}
		var list []model.ProductionTask
		q.Order("CASE WHEN due_at IS NULL THEN 1 ELSE 0 END, due_at asc, created_at asc").Limit(500).Find(&list)
		return s.taskViews(list), nil
	}))
	r.Get("/studios/{sid}/issues", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.Issue{}).Where("studio_id = ?", x.StudioID)
		if !x.IsManager() && x.Role != model.RoleProducer {
			q = q.Where("assignee_id = ? OR reporter_id = ?", x.User.ID, x.User.ID)
		}
		for p, col := range map[string]string{"bookId": "book_id", "assigneeId": "assignee_id", "type": "type"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		switch st := r.URL.Query().Get("status"); st {
		case "":
		case "active":
			q = q.Where("status <> ?", model.IssueClosed)
		default:
			q = q.Where("status = ?", st)
		}
		p, err := paginate[model.Issue](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		p.Items = s.issueViews(p.Items.([]model.Issue))
		return p, nil
	}))
	r.Get("/studios/{sid}/jobs", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.Job{}).Where("studio_id = ? AND type <> ?", x.StudioID, "webhook")
		if !x.IsManager() {
			q = q.Where("created_by = ?", x.User.ID)
		}
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status IN ?", strings.Split(st, ","))
		}
		if b := r.URL.Query().Get("bookId"); b != "" {
			q = q.Where("book_id = ?", b)
		}
		return paginate[model.Job](r, q, "created_at desc")
	}))
	r.Get("/jobs/{id}", h(func(r *http.Request) (any, error) {
		j, err := s.loadJob(r)
		if err != nil {
			return nil, err
		}
		return j, nil
	}))
	r.Post("/jobs/{id}/cancel", h(func(r *http.Request) (any, error) {
		j, err := s.loadJob(r)
		if err != nil {
			return nil, err
		}
		if j.Type == "pipeline" {
			var run model.PipelineRun
			if s.app.DB.First(&run, "id = ?", j.RefID).Error == nil {
				s.app.DB.Model(&run).Update("status", "canceled")
			}
		}
		return nil, s.app.Q.Cancel(j.ID)
	}))
	r.Post("/jobs/{id}/retry", h(func(r *http.Request) (any, error) {
		j, err := s.loadJob(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.Q.Retry(j.ID)
	}))
	r.Get("/studios/{sid}/search", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		kw := strings.TrimSpace(r.URL.Query().Get("q"))
		if kw == "" {
			return map[string]any{"books": []any{}, "chapters": []any{}, "characters": []any{}, "voices": []any{}}, nil
		}
		like := "%" + kw + "%"
		bookQ := s.app.DB.Model(&model.Book{}).Select("id").Where("studio_id = ? AND deleted_at IS NULL", x.StudioID)
		if !x.IsManager() && x.Role != model.RoleProducer {
			bookQ = bookQ.Where("id IN (?)", s.app.DB.Model(&model.BookMember{}).Select("book_id").Where("user_id = ?", x.User.ID))
		}
		var books []model.Book
		s.app.DB.Where("id IN (?) AND ("+dbx.Like(s.app.DB, "title")+" OR "+dbx.Like(s.app.DB, "author")+")", bookQ, like, like).Limit(8).Find(&books)
		var chapters []model.Chapter
		s.app.DB.Select("id", "book_id", "title", "index").Where("book_id IN (?) AND "+dbx.Like(s.app.DB, "title"), bookQ, like).Limit(8).Find(&chapters)
		var chars []model.Character
		s.app.DB.Select("id", "book_id", "name", "color").Where("book_id IN (?) AND ("+dbx.Like(s.app.DB, "name")+" OR "+dbx.Like(s.app.DB, "aliases")+")", bookQ, like, like).Limit(8).Find(&chars)
		var voices []model.Voice
		s.app.DB.Where("status = ? AND ("+dbx.Like(s.app.DB, "name")+" OR "+dbx.Like(s.app.DB, "tags")+") AND ((scope = 'system' AND listed = ?) OR studio_id = ? OR owner_user_id = ?)", model.VoiceReady, like, like, true, x.StudioID, x.User.ID).Limit(8).Find(&voices)
		var bv []map[string]any
		for _, b := range books {
			bv = append(bv, map[string]any{"id": b.ID, "title": b.Title, "author": b.Author, "cover": s.app.AssetURLByID(b.CoverAssetID)})
		}
		var vv []map[string]any
		for _, v := range voices {
			vv = append(vv, map[string]any{"id": v.ID, "name": v.Name, "tags": v.Tags.V, "gender": v.Gender})
		}
		titles := map[string]string{}
		for _, c := range chapters {
			if _, ok := titles[c.BookID]; !ok {
				var b model.Book
				s.app.DB.Select("title").First(&b, "id = ?", c.BookID)
				titles[c.BookID] = b.Title
			}
		}
		var cv []map[string]any
		for _, c := range chapters {
			cv = append(cv, map[string]any{"id": c.ID, "bookId": c.BookID, "title": c.Title, "index": c.Index, "bookTitle": titles[c.BookID]})
		}
		return map[string]any{"books": bv, "chapters": cv, "characters": chars, "voices": vv}, nil
	}))
	// ---- pipelines (studio list) ----
	r.Get("/studios/{sid}/pipelines", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var list []model.PipelineRun
		s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at desc").Limit(50).Find(&list)
		var out []map[string]any
		for i := range list {
			out = append(out, s.app.PipelineView(&list[i]))
		}
		return out, nil
	}))
	// ---- quick TTS ----
	r.Get("/studios/{sid}/quick", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var list []model.QuickTTS
		s.app.DB.Where("studio_id = ? AND user_id = ? AND created_at > ?", x.StudioID, x.User.ID, time.Now().AddDate(0, 0, -30)).Order("created_at desc").Limit(50).Find(&list)
		var out []map[string]any
		for _, q := range list {
			out = append(out, map[string]any{"item": q, "url": s.app.AssetURLByID(q.AssetID)})
		}
		return out, nil
	}))
	r.Post("/studios/{sid}/quick", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			Title string            `json:"title"`
			Lines []model.QuickLine `json:"lines"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return s.app.StartQuickTTS(x, in.Title, in.Lines)
	}))
	r.Get("/quick/{id}", h(func(r *http.Request) (any, error) {
		var q model.QuickTTS
		if err := s.app.DB.First(&q, "id = ? AND user_id = ?", param(r, "id"), userOf(r).ID).Error; err != nil {
			return nil, core.NotFound("记录")
		}
		return map[string]any{"item": q, "url": s.app.AssetURLByID(q.AssetID)}, nil
	}))
	r.Delete("/quick/{id}", h(func(r *http.Request) (any, error) {
		return nil, s.app.DB.Where("id = ? AND user_id = ?", param(r, "id"), userOf(r).ID).Delete(&model.QuickTTS{}).Error
	}))
	r.Post("/quick/{id}/to-book", h(func(r *http.Request) (any, error) {
		var q model.QuickTTS
		if err := s.app.DB.First(&q, "id = ? AND user_id = ?", param(r, "id"), userOf(r).ID).Error; err != nil {
			return nil, core.NotFound("记录")
		}
		x, err := s.app.StudioAccess(userOf(r), q.StudioID)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		for _, l := range q.Lines.V {
			if l.Speaker != "" && l.Speaker != "旁白" {
				sb.WriteString(l.Speaker + "说：“" + l.Text + "”\n")
			} else {
				sb.WriteString(l.Text + "\n")
			}
		}
		p, err := s.app.PreviewImport(q.Title+".txt", []byte(sb.String()))
		if err != nil {
			return nil, err
		}
		b, err := s.app.CreateBook(r.Context(), x, core.CreateBookInput{ImportID: p.ID, Title: q.Title, Mode: model.ModeMulti})
		if err != nil {
			return nil, err
		}
		return map[string]any{"bookId": b.ID}, nil
	}))
	// ---- announcements, feedback, reports ----
	r.Get("/announcements", h(func(r *http.Request) (any, error) {
		var list []model.Announcement
		now := time.Now()
		s.app.DB.Where("published = ? AND (starts_at IS NULL OR starts_at <= ?) AND (ends_at IS NULL OR ends_at >= ?)", true, now, now).Order("created_at desc").Limit(10).Find(&list)
		u := userOf(r)
		var planCodes []string
		var studioIDs []string
		s.app.DB.Model(&model.StudioMember{}).Where("user_id = ?", u.ID).Pluck("studio_id", &studioIDs)
		for _, sid := range studioIDs {
			var st model.Studio
			var p model.Plan
			if s.app.DB.Select("plan_id").First(&st, "id = ?", sid).Error == nil && s.app.DB.Select("code").First(&p, "id = ?", st.PlanID).Error == nil {
				planCodes = append(planCodes, p.Code)
			}
		}
		var out []model.Announcement
		for _, a := range list {
			aud := a.Audience.V
			match := true
			if pcs, ok := aud["planCodes"].([]any); ok && len(pcs) > 0 {
				match = false
				for _, pc := range pcs {
					if containsStr(planCodes, pc.(string)) {
						match = true
					}
				}
			}
			if sids, ok := aud["studioIds"].([]any); ok && len(sids) > 0 {
				match = false
				for _, sid := range sids {
					if containsStr(studioIDs, sid.(string)) {
						match = true
					}
				}
			}
			if match {
				out = append(out, a)
			}
		}
		if out == nil {
			out = []model.Announcement{}
		}
		return out, nil
	}))
	r.Post("/feedback", h(func(r *http.Request) (any, error) {
		var in model.Feedback
		bind(r, &in)
		if strings.TrimSpace(in.Content) == "" {
			return nil, core.Bad("请填写反馈内容")
		}
		f := model.Feedback{UserID: userOf(r).ID, Kind: firstNonEmptyStr(in.Kind, "suggestion"), Content: in.Content, Contact: in.Contact, Page: in.Page, Status: "open"}
		return nil, s.app.DB.Create(&f).Error
	}))
	r.Get("/me/feedback", h(func(r *http.Request) (any, error) {
		var list []model.Feedback
		s.app.DB.Where("user_id = ?", userOf(r).ID).Order("created_at desc").Limit(30).Find(&list)
		return list, nil
	}))
	r.Post("/reports", h(func(r *http.Request) (any, error) {
		var in model.Report
		bind(r, &in)
		if in.TargetID == "" || in.Reason == "" {
			return nil, core.Bad("请选择举报原因")
		}
		rep := model.Report{ReporterID: userOf(r).ID, TargetType: in.TargetType, TargetID: in.TargetID, Reason: in.Reason, Detail: in.Detail, Status: "open"}
		return nil, s.app.DB.Create(&rep).Error
	}))
}

func (s *Server) loadJob(r *http.Request) (*model.Job, error) {
	var j model.Job
	if err := s.app.DB.First(&j, "id = ?", param(r, "id")).Error; err != nil {
		return nil, core.NotFound("任务")
	}
	u := userOf(r)
	if j.CreatedBy == u.ID {
		return &j, nil
	}
	x, err := s.app.StudioAccess(u, j.StudioID)
	if err != nil {
		return nil, err
	}
	if !x.IsManager() && x.Role != model.RoleProducer {
		return nil, core.Forbidden("")
	}
	return &j, nil
}
