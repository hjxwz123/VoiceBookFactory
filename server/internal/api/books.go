package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

func (s *Server) bookView(b *model.Book, x *core.Access, withProgress bool) map[string]any {
	out := map[string]any{
		"id": b.ID, "studioId": b.StudioID, "title": b.Title, "author": b.Author, "narrator": b.Narrator, "publisher": b.Publisher, "adaptation": b.Adaptation,
		"description": b.Description, "coverAssetId": b.CoverAssetID, "cover": s.app.AssetURLByID(b.CoverAssetID), "language": b.Language, "genre": b.Genre,
		"tags": b.Tags.V, "mode": b.Mode, "status": b.Status, "isbn": b.ISBN, "seriesId": b.SeriesID, "seriesOrder": b.SeriesOrder, "copyright": b.Copyright.V,
		"settings": b.Settings.V, "workflow": b.Workflow.V, "workflowOn": b.WorkflowOn, "wordCount": b.WordCount, "budget": b.Budget, "createdBy": b.CreatedBy,
		"leadId": b.LeadID, "pipelineRunId": b.PipelineRunID, "createdAt": b.CreatedAt, "updatedAt": b.UpdatedAt, "deletedAt": b.DeletedAt,
	}
	if x != nil {
		out["permissions"] = x.PermList()
		out["positions"] = x.Positions
		out["voiceOnly"] = x.VoiceOnly
	}
	if withProgress {
		out["progress"] = s.app.BookProgress(b)
	}
	return out
}

func (s *Server) bookRoutes(r chi.Router) {
	// ---- imports & book list ----
	r.Post("/studios/{sid}/imports", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		if !x.CanCreateBook() {
			return nil, core.Forbidden("只有所有者、管理员或制作人可以创建图书")
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			var in struct{ Text, Title string }
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Text) == "" {
				return nil, core.Bad("请粘贴正文内容")
			}
			name := firstNonEmptyStr(in.Title, "粘贴的文本") + ".txt"
			p, err := s.app.PreviewImport(name, []byte(in.Text))
			if err == nil && strings.TrimSpace(in.Title) == "" {
				// never call a book "粘贴的文本": use a title line when the paste starts with one, else ask
				p.Title, p.Author = core.GuessTitle(in.Text)
			}
			return p, err
		}
		path, name, cleanup, err := s.tempUpload(r, "file")
		if err != nil {
			return nil, err
		}
		defer cleanup()
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return s.app.PreviewImport(name, data)
	}))
	r.Post("/studios/{sid}/imports/{id}/resplit", h(func(r *http.Request) (any, error) {
		if _, err := s.studio(r); err != nil {
			return nil, err
		}
		var in struct {
			Target   int    `json:"target"`
			TitleFmt string `json:"titleFmt"`
		}
		bind(r, &in)
		return s.app.ResplitImport(param(r, "id"), in.Target, in.TitleFmt)
	}))
	r.Post("/studios/{sid}/imports/{id}/ai-detect", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		return s.app.DetectImportWithAI(ctx, x, param(r, "id"))
	}))
	r.Get("/studios/{sid}/books", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.Book{}).Where("studio_id = ? AND deleted_at IS NULL", x.StudioID)
		if !x.IsManager() && x.Role != model.RoleProducer {
			q = q.Where("id IN (?)", s.app.DB.Model(&model.BookMember{}).Select("book_id").Where("user_id = ?", x.User.ID))
		}
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if m := r.URL.Query().Get("mode"); m != "" {
			q = q.Where("mode = ?", m)
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where("("+dbx.Like(q, "title")+" OR "+dbx.Like(q, "author")+")", "%"+kw+"%", "%"+kw+"%")
		}
		if sid := r.URL.Query().Get("seriesId"); sid != "" {
			q = q.Where("series_id = ?", sid)
		}
		order := "updated_at desc"
		if r.URL.Query().Get("sort") == "title" {
			order = "title asc"
		}
		p, err := paginate[model.Book](r, q, order)
		if err != nil {
			return nil, err
		}
		books := p.Items.([]model.Book)
		items := make([]map[string]any, 0, len(books))
		for i := range books {
			v := s.bookView(&books[i], nil, false)
			prog := s.app.BookProgress(&books[i])
			v["progress"] = map[string]any{"chapters": prog["included"], "rendered": prog["rendered"], "segments": prog["segments"], "readySegments": prog["readySegments"], "durationMs": prog["durationMs"], "characters": prog["characters"], "next": prog["next"], "steps": prog["steps"]}
			items = append(items, v)
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/studios/{sid}/books", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in core.CreateBookInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		b, err := s.app.CreateBook(r.Context(), x, in)
		if err != nil {
			return nil, err
		}
		return s.bookView(b, nil, false), nil
	}))
	r.Get("/studios/{sid}/trash", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var list []model.Book
		q := s.app.DB.Where("studio_id = ? AND deleted_at IS NOT NULL", x.StudioID)
		if !x.IsManager() {
			q = q.Where("created_by = ?", x.User.ID)
		}
		q.Order("deleted_at desc").Find(&list)
		var out []map[string]any
		for i := range list {
			out = append(out, s.bookView(&list[i], nil, false))
		}
		return out, nil
	}))
	r.Post("/studios/{sid}/trash/{bid}/restore", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		return nil, s.app.DB.Model(&model.Book{}).Where("id = ? AND studio_id = ?", param(r, "bid"), x.StudioID).Update("deleted_at", nil).Error
	}))
	r.Delete("/studios/{sid}/trash/{bid}", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var b model.Book
		if err := s.app.DB.First(&b, "id = ? AND studio_id = ? AND deleted_at IS NOT NULL", param(r, "bid"), x.StudioID).Error; err != nil {
			return nil, core.NotFound("图书")
		}
		if !x.IsManager() && b.CreatedBy != x.User.ID {
			return nil, core.Forbidden("")
		}
		s.app.DeleteBookData(b.ID)
		return nil, nil
	}))

	r.Route("/books/{bid}", func(r chi.Router) {
		r.Get("/", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			return s.bookView(x.Book, x, true), nil
		}))
		r.Patch("/", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermEditInfo); err != nil {
				return nil, err
			}
			var in struct {
				Title        *string             `json:"title"`
				Author       *string             `json:"author"`
				Narrator     *string             `json:"narrator"`
				Publisher    *string             `json:"publisher"`
				Adaptation   *string             `json:"adaptation"`
				Description  *string             `json:"description"`
				CoverAssetID *string             `json:"coverAssetId"`
				Language     *string             `json:"language"`
				Genre        *string             `json:"genre"`
				Tags         *[]string           `json:"tags"`
				Mode         *string             `json:"mode"`
				Status       *string             `json:"status"`
				ISBN         *string             `json:"isbn"`
				SeriesID     *string             `json:"seriesId"`
				SeriesOrder  *int                `json:"seriesOrder"`
				Copyright    *model.Copyright    `json:"copyright"`
				Settings     *model.BookSettings `json:"settings"`
				Budget       *int64              `json:"budget"`
				LeadID       *string             `json:"leadId"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			up := map[string]any{}
			set := func(col string, v *string) {
				if v != nil {
					up[col] = strings.TrimSpace(*v)
				}
			}
			set("title", in.Title)
			set("author", in.Author)
			set("narrator", in.Narrator)
			set("publisher", in.Publisher)
			set("adaptation", in.Adaptation)
			set("description", in.Description)
			if in.CoverAssetID != nil && *in.CoverAssetID != "" {
				// only pictures this studio owns can become its cover; a foreign id would leak a signed link to it
				var n int64
				s.app.DB.Model(&model.Asset{}).Where("id = ? AND kind = ? AND (studio_id = ? OR scope = ?)", *in.CoverAssetID, model.AssetImage, x.StudioID, "system").Count(&n)
				if n == 0 {
					return nil, core.Bad("找不到这张图片")
				}
			}
			set("cover_asset_id", in.CoverAssetID)
			set("language", in.Language)
			set("genre", in.Genre)
			set("isbn", in.ISBN)
			set("series_id", in.SeriesID)
			if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
				return nil, core.Bad("书名不能为空")
			}
			if in.Mode != nil {
				up["mode"] = *in.Mode
			}
			if in.Status != nil {
				if *in.Status == model.BookReviewing && !x.User.IsAdmin() {
					return nil, core.Forbidden("")
				}
				if x.Book.Status == model.BookReviewing {
					return nil, core.Forbidden("图书正在等待内容审核")
				}
				up["status"] = *in.Status
			}
			if in.Tags != nil {
				up["tags"] = model.Strings(*in.Tags...)
			}
			if in.SeriesOrder != nil {
				up["series_order"] = *in.SeriesOrder
			}
			if in.Copyright != nil {
				up["copyright"] = model.JSON[model.Copyright]{V: *in.Copyright}
			}
			if in.Settings != nil {
				if err := core.Need(x, core.PermManage); err != nil && !x.Can(core.PermScript) {
					return nil, err
				}
				old := x.Book.Settings.V
				up["settings"] = model.JSON[model.BookSettings]{V: *in.Settings}
				if old.Pause != in.Settings.Pause || old.Flow != in.Settings.Flow || old.DuckingDb != in.Settings.DuckingDb || old.MonologueReverb != in.Settings.MonologueReverb || old.IntroTemplateID != in.Settings.IntroTemplateID || old.OutroTemplateID != in.Settings.OutroTemplateID || old.LoudnessPreset != in.Settings.LoudnessPreset {
					s.app.DB.Model(&model.Chapter{}).Where("book_id = ? AND render_id <> ''", x.Book.ID).Update("render_stale", true)
				}
				s.app.ApplyBookTemplates(x.Book.ID, old, *in.Settings)
			}
			if in.Budget != nil {
				if err := core.Need(x, core.PermManage); err != nil {
					return nil, err
				}
				up["budget"] = *in.Budget
			}
			if in.LeadID != nil {
				if err := core.Need(x, core.PermManage); err != nil {
					return nil, err
				}
				up["lead_id"] = *in.LeadID
			}
			if err := s.app.DB.Model(x.Book).Updates(up).Error; err != nil {
				return nil, err
			}
			s.app.DB.First(x.Book, "id = ?", x.Book.ID)
			return s.bookView(x.Book, x, false), nil
		}))
		r.Delete("/", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			return nil, s.app.TrashBook(x)
		}))
		r.Get("/progress", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			return s.app.BookProgress(x.Book), nil
		}))
		r.Post("/intro", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermEditInfo); err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			return s.app.GenerateIntro(ctx, x)
		}))
		r.Get("/covers", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			return s.app.CoverOptions(x.Book), nil
		}))
		r.Post("/cover", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermEditInfo); err != nil {
				return nil, err
			}
			as, err := s.saveUpload(r, "file", core.AssetInput{StudioID: x.StudioID, Kind: model.AssetImage, Name: x.Book.Title + " 封面", Origin: "upload", CreatedBy: x.User.ID})
			if err != nil {
				return nil, err
			}
			s.app.DB.Model(x.Book).Update("cover_asset_id", as.ID)
			return map[string]any{"assetId": as.ID, "url": s.app.AssetURL(as)}, nil
		}))
		r.Post("/copy", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if !x.CanCreateBook() {
				return nil, core.Forbidden("")
			}
			b := *x.Book
			b.ID, b.Title, b.Status, b.CreatedBy, b.LeadID, b.PipelineRunID = "", b.Title+"（副本）", model.BookPreparing, x.User.ID, x.User.ID, ""
			if err := s.app.DB.Create(&b).Error; err != nil {
				return nil, err
			}
			s.app.DB.Create(&model.BookMember{BookID: b.ID, UserID: x.User.ID, Positions: model.Strings(model.PosLead), AddedBy: x.User.ID})
			var chs []model.Chapter
			s.app.DB.Where("book_id = ?", x.Book.ID).Find(&chs)
			for _, c := range chs {
				c.ID, c.BookID, c.CurrentScriptID, c.RenderID, c.DurationMs, c.ReviewStatus, c.EditLockUserID, c.EditLockAt = "", b.ID, "", "", 0, "", "", nil
				s.app.DB.Create(&c)
			}
			var cs []model.Character
			s.app.DB.Where("book_id = ?", x.Book.ID).Find(&cs)
			for _, c := range cs {
				c.ID, c.BookID, c.AssigneeID, c.ClaimStatus, c.ClaimantID = "", b.ID, "", "", ""
				s.app.DB.Create(&c)
			}
			return s.bookView(&b, nil, false), nil
		}))
		// ---- crew ----
		r.Get("/crew", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			var ms []model.BookMember
			s.app.DB.Where("book_id = ?", x.Book.ID).Find(&ms)
			var out []map[string]any
			for _, m := range ms {
				var u model.User
				if s.app.DB.First(&u, "id = ?", m.UserID).Error != nil {
					continue
				}
				var sm model.StudioMember
				s.app.DB.First(&sm, "studio_id = ? AND user_id = ?", x.StudioID, u.ID)
				var chars []model.Character
				s.app.DB.Select("id", "name", "color").Where("book_id = ? AND assignee_id = ?", x.Book.ID, u.ID).Find(&chars)
				out = append(out, map[string]any{"userId": u.ID, "nickname": u.Nickname, "avatarUrl": u.AvatarURL, "positions": m.Positions.V, "studioRole": sm.Role, "characters": chars, "isLead": x.Book.LeadID == u.ID})
			}
			return out, nil
		}))
		r.Post("/crew", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermManage); err != nil {
				return nil, err
			}
			var in struct {
				UserID    string   `json:"userId"`
				Positions []string `json:"positions"`
			}
			bind(r, &in)
			var sm model.StudioMember
			if s.app.DB.First(&sm, "studio_id = ? AND user_id = ? AND status = ?", x.StudioID, in.UserID, "active").Error != nil {
				return nil, core.Bad("只能添加工作室成员；外部人员请使用「邀请外部协作者」")
			}
			var bm model.BookMember
			if s.app.DB.First(&bm, "book_id = ? AND user_id = ?", x.Book.ID, in.UserID).Error == nil {
				return nil, s.app.DB.Model(&bm).Update("positions", model.Strings(in.Positions...)).Error
			}
			s.app.Notify(in.UserID, x.StudioID, "crew", "你被加入了《"+x.Book.Title+"》剧组", "岗位："+strings.Join(in.Positions, "、"), "/books/"+x.Book.ID)
			return nil, s.app.DB.Create(&model.BookMember{BookID: x.Book.ID, UserID: in.UserID, Positions: model.Strings(in.Positions...), AddedBy: x.User.ID}).Error
		}))
		r.Delete("/crew/{uid}", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermManage); err != nil {
				return nil, err
			}
			return nil, s.app.DB.Where("book_id = ? AND user_id = ?", x.Book.ID, param(r, "uid")).Delete(&model.BookMember{}).Error
		}))
		r.Get("/activity", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			q := s.app.DB.Model(&model.ActivityLog{}).Where("book_id = ?", x.Book.ID)
			p, err := paginate[model.ActivityLog](r, q, "created_at desc")
			if err != nil {
				return nil, err
			}
			logs := p.Items.([]model.ActivityLog)
			var out []map[string]any
			for _, l := range logs {
				var u model.User
				s.app.DB.Select("nickname", "avatar_url").First(&u, "id = ?", l.ActorID)
				out = append(out, map[string]any{"id": l.ID, "action": l.Action, "summary": l.Summary, "actor": u.Nickname, "avatarUrl": u.AvatarURL, "createdAt": l.CreatedAt, "targetType": l.TargetType, "targetId": l.TargetID})
			}
			p.Items = out
			return p, nil
		}))
		// ---- chapters ----
		r.Get("/chapters", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			var chs []model.Chapter
			s.app.DB.Where("book_id = ?", x.Book.ID).Order("\"index\" asc").Find(&chs)
			states := s.app.ChapterStates(x.Book.ID)
			var tasks []model.ProductionTask
			if x.Book.WorkflowOn {
				s.app.DB.Where("book_id = ?", x.Book.ID).Order("stage_index asc").Find(&tasks)
			}
			renderIDs := []string{}
			for _, c := range chs {
				if c.RenderID != "" {
					renderIDs = append(renderIDs, c.RenderID)
				}
			}
			renderURL := map[string]string{}
			if len(renderIDs) > 0 {
				var rs []model.Render
				s.app.DB.Select("id", "asset_id").Where("id IN ? AND status = ?", renderIDs, "ready").Find(&rs)
				for _, r := range rs {
					renderURL[r.ID] = s.app.AssetURLByID(r.AssetID)
				}
			}
			// unresolved review tickets per chapter, so the review overview can show where work is left
			type issueCount struct {
				ChapterID       string
				Open, Verifying int
			}
			var ics []issueCount
			s.app.DB.Model(&model.Issue{}).Select("chapter_id, SUM(CASE WHEN status = ? THEN 0 ELSE 1 END) AS open, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) AS verifying", model.IssueVerifying, model.IssueVerifying).
				Where("book_id = ? AND status <> ?", x.Book.ID, model.IssueClosed).Group("chapter_id").Scan(&ics)
			issuesBy := map[string]issueCount{}
			for _, ic := range ics {
				issuesBy[ic.ChapterID] = ic
			}
			var out []map[string]any
			for _, c := range chs {
				v := map[string]any{"renderUrl": renderURL[c.RenderID], "id": c.ID, "openIssues": issuesBy[c.ID].Open, "verifyingIssues": issuesBy[c.ID].Verifying, "index": c.Index, "title": c.Title, "volume": c.Volume, "kind": c.Kind, "included": c.Included, "wordCount": c.WordCount,
					"currentScriptId": c.CurrentScriptID, "scriptStale": c.ScriptStale, "renderId": c.RenderID, "renderStale": c.RenderStale, "durationMs": c.DurationMs,
					"reviewStatus": c.ReviewStatus, "editLockUserId": c.EditLockUserID, "state": states[c.ID], "updatedAt": c.UpdatedAt}
				if x.Book.WorkflowOn {
					var ct []model.ProductionTask
					for _, t := range tasks {
						if t.ChapterID == c.ID {
							ct = append(ct, t)
						}
					}
					v["tasks"] = ct
				}
				out = append(out, v)
			}
			if out == nil {
				out = []map[string]any{}
			}
			return out, nil
		}))
		r.Post("/chapters", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			var in struct {
				Title      string `json:"title"`
				Text       string `json:"text"`
				AfterIndex int    `json:"afterIndex"`
				Split      bool   `json:"split"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			var maxIdx int
			s.app.DB.Model(&model.Chapter{}).Where("book_id = ?", x.Book.ID).Select("COALESCE(MAX(\"index\"),0)").Scan(&maxIdx)
			drafts := []text.ChapterDraft{{Title: in.Title, Kind: "body", Included: true, Text: text.Clean(in.Text)}}
			if in.Split {
				if ds, okk := text.SplitChapters(in.Text); okk {
					drafts = ds
				}
			}
			var created []model.Chapter
			for i, d := range drafts {
				c := model.Chapter{BookID: x.Book.ID, Index: maxIdx + i + 1, Title: firstNonEmptyStr(d.Title, "新章节"), Kind: firstNonEmptyStr(d.Kind, "body"), Included: d.Included || !in.Split, SourceText: d.Text, WordCount: text.WordCount(d.Text)}
				if in.AfterIndex > 0 && len(drafts) == 1 {
					s.app.DB.Model(&model.Chapter{}).Where("book_id = ? AND \"index\" > ?", x.Book.ID, in.AfterIndex).Update("index", gormExpr("\"index\" + 1"))
					c.Index = in.AfterIndex + 1
				}
				s.app.DB.Create(&c)
				created = append(created, c)
			}
			s.app.ReindexChapters(x.Book.ID)
			s.app.RecountBook(x.Book.ID)
			s.app.EnsureChapterTasks(x.Book.ID)
			return created, nil
		}))
		r.Post("/chapters/reorder", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			var in struct {
				IDs []string `json:"ids"`
			}
			bind(r, &in)
			for i, id := range in.IDs {
				s.app.DB.Model(&model.Chapter{}).Where("id = ? AND book_id = ?", id, x.Book.ID).Update("index", i+1)
			}
			return nil, nil
		}))
		r.Post("/chapters/merge", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			var in struct {
				IDs []string `json:"ids"`
			}
			bind(r, &in)
			var chs []model.Chapter
			s.app.DB.Where("book_id = ? AND id IN ?", x.Book.ID, in.IDs).Order("\"index\" asc").Find(&chs)
			if len(chs) < 2 {
				return nil, core.Bad("请至少选择两个章节")
			}
			var parts []string
			for _, c := range chs {
				parts = append(parts, c.SourceText)
			}
			first := chs[0]
			body := strings.Join(parts, "\n")
			s.app.DB.Model(&first).Updates(map[string]any{"source_text": body, "word_count": text.WordCount(body), "current_script_id": "", "render_id": "", "script_stale": false})
			for _, c := range chs[1:] {
				s.app.DB.Delete(&c)
			}
			s.app.ReindexChapters(x.Book.ID)
			s.app.RecountBook(x.Book.ID)
			return nil, nil
		}))
		r.Post("/chapters/resplit", h(func(r *http.Request) (any, error) {
			x, err := s.book(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			var in struct {
				IDs    []string `json:"ids"`
				Target int      `json:"target"`
			}
			bind(r, &in)
			var chs []model.Chapter
			s.app.DB.Where("book_id = ? AND id IN ?", x.Book.ID, in.IDs).Order("\"index\" asc").Find(&chs)
			n := 0
			for _, c := range chs {
				if c.CurrentScriptID != "" {
					continue // keep produced chapters intact
				}
				parts := text.ResplitChapter(c.Title, c.SourceText, in.Target)
				if len(parts) <= 1 {
					continue
				}
				s.app.DB.Model(&model.Chapter{}).Where("book_id = ? AND \"index\" > ?", x.Book.ID, c.Index).Update("index", gormExpr("\"index\" + ?", len(parts)-1))
				s.app.DB.Model(&c).Updates(map[string]any{"title": parts[0].Title, "source_text": parts[0].Text, "word_count": parts[0].WordCount})
				for i, p := range parts[1:] {
					s.app.DB.Create(&model.Chapter{BookID: x.Book.ID, Index: c.Index + i + 1, Title: p.Title, Volume: c.Volume, Kind: c.Kind, Included: c.Included, SourceText: p.Text, WordCount: p.WordCount})
				}
				n++
			}
			s.app.ReindexChapters(x.Book.ID)
			return map[string]any{"split": n}, nil
		}))
	})
	r.Route("/chapters/{cid}", func(r chi.Router) {
		r.Get("/", h(func(r *http.Request) (any, error) {
			x, ch, err := s.chapter(r)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": ch.ID, "bookId": ch.BookID, "index": ch.Index, "title": ch.Title, "volume": ch.Volume, "kind": ch.Kind, "included": ch.Included,
				"text": ch.SourceText, "wordCount": ch.WordCount, "currentScriptId": ch.CurrentScriptID, "scriptStale": ch.ScriptStale, "renderId": ch.RenderID,
				"renderStale": ch.RenderStale, "durationMs": ch.DurationMs, "reviewStatus": ch.ReviewStatus, "state": s.app.ChapterStates(ch.BookID)[ch.ID], "permissions": x.PermList()}, nil
		}))
		r.Patch("/", h(func(r *http.Request) (any, error) {
			x, ch, err := s.chapter(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			var in struct {
				Title    *string `json:"title"`
				Volume   *string `json:"volume"`
				Included *bool   `json:"included"`
				Kind     *string `json:"kind"`
			}
			bind(r, &in)
			up := map[string]any{}
			if in.Title != nil {
				up["title"] = strings.TrimSpace(*in.Title)
			}
			if in.Volume != nil {
				up["volume"] = *in.Volume
			}
			if in.Included != nil {
				up["included"] = *in.Included
			}
			if in.Kind != nil {
				up["kind"] = *in.Kind
			}
			if err := s.app.DB.Model(ch).Updates(up).Error; err != nil {
				return nil, err
			}
			s.app.RecountBook(ch.BookID)
			if in.Included != nil && *in.Included {
				s.app.EnsureChapterTasks(ch.BookID)
			}
			return ch, nil
		}))
		r.Delete("/", h(func(r *http.Request) (any, error) {
			x, ch, err := s.chapter(r)
			if err != nil {
				return nil, err
			}
			if err := core.Need(x, core.PermScript); err != nil {
				return nil, err
			}
			s.app.DB.Where("chapter_id = ?", ch.ID).Delete(&model.Segment{})
			s.app.DB.Where("chapter_id = ?", ch.ID).Delete(&model.Script{})
			s.app.DB.Where("chapter_id = ?", ch.ID).Delete(&model.ProductionTask{})
			s.app.DB.Delete(ch)
			s.app.ReindexChapters(ch.BookID)
			s.app.RecountBook(ch.BookID)
			return nil, nil
		}))
		r.Put("/text", h(func(r *http.Request) (any, error) {
			x, ch, err := s.chapter(r)
			if err != nil {
				return nil, err
			}
			var in struct{ Text string }
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			return s.app.UpdateChapterText(x, ch, in.Text)
		}))
		r.Post("/sync", h(func(r *http.Request) (any, error) {
			x, ch, err := s.chapter(r)
			if err != nil {
				return nil, err
			}
			return s.app.EnqueueScriptSync(x, ch)
		}))
	})
	r.Post("/books/{bid}/chapters/import", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ImportID    string            `json:"importId"`
			Chapters    []core.ChapterRef `json:"chapters"`
			AutoAnalyze bool              `json:"autoAnalyze"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		n, err := s.app.ImportIntoBook(r.Context(), x, in.ImportID, in.Chapters, in.AutoAnalyze)
		return map[string]any{"added": n}, err
	}))
	r.Post("/books/{bid}/chapters/append", h(func(r *http.Request) (any, error) {
		x, err := s.book(r)
		if err != nil {
			return nil, err
		}
		if err := core.Need(x, core.PermScript); err != nil {
			return nil, err
		}
		path, name, cleanup, err := s.tempUpload(r, "file")
		if err != nil {
			return nil, err
		}
		defer cleanup()
		f, _ := os.Open(path)
		data, _ := io.ReadAll(f)
		f.Close()
		p, err := s.app.PreviewImport(name, data)
		if err != nil {
			return nil, err
		}
		var maxIdx int
		s.app.DB.Model(&model.Chapter{}).Where("book_id = ?", x.Book.ID).Select("COALESCE(MAX(\"index\"),0)").Scan(&maxIdx)
		// skip chapters whose titles already exist (serial updates)
		var titles []string
		s.app.DB.Model(&model.Chapter{}).Where("book_id = ?", x.Book.ID).Pluck("title", &titles)
		existing := map[string]bool{}
		for _, t := range titles {
			existing[t] = true
		}
		added := 0
		for _, c := range p.Chapters {
			if existing[c.Title] {
				continue
			}
			added++
			s.app.DB.Create(&model.Chapter{BookID: x.Book.ID, Index: maxIdx + added, Title: c.Title, Volume: c.Volume, Kind: c.Kind, Included: c.Included, SourceText: c.Text, WordCount: c.WordCount})
		}
		s.app.RecountBook(x.Book.ID)
		s.app.EnsureChapterTasks(x.Book.ID)
		return map[string]any{"added": added}, nil
	}))
}
