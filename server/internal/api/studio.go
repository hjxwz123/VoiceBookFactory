package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/ai/tts"
	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
	"voicebook/internal/secure"
)

func (s *Server) studioView(st *model.Studio, x *core.Access) map[string]any {
	var p model.Plan
	s.app.DB.First(&p, "id = ?", st.PlanID)
	bal := s.app.Balance(st.ID)
	var books int64
	s.app.DB.Model(&model.Book{}).Where("studio_id = ? AND deleted_at IS NULL", st.ID).Count(&books)
	out := map[string]any{
		"id": st.ID, "name": st.Name, "type": st.Type, "logo": st.Logo, "description": st.Description, "ownerId": st.OwnerID,
		"plan": p, "planRenewAt": st.PlanRenewAt, "planOverrideId": st.PlanOverrideID, "settings": st.Settings.V, "status": st.Status, "features": st.Features.V,
		"seats":   map[string]int{"used": s.app.SeatsUsed(st.ID), "limit": s.app.SeatLimit(st.ID)},
		"storage": map[string]int64{"used": st.StorageUsed, "limit": s.app.StorageLimit(st.ID)}, "books": books,
		"credits": bal, "createdAt": st.CreatedAt,
	}
	if x != nil {
		out["role"] = x.Role
		var feats []string
		for _, f := range []string{model.FeatAlignment, model.FeatReview, model.FeatProduction, model.FeatPayroll, model.FeatBYOK, model.FeatOpenAPI, model.FeatBatch, model.FeatProClone, model.FeatDistribute, model.FeatPrivate} {
			if s.app.HasFeature(st.ID, f) {
				feats = append(feats, f)
			}
		}
		out["effectiveFeatures"] = feats
	}
	return out
}

func (s *Server) studioRoutes(r chi.Router) {
	r.Post("/studios", h(func(r *http.Request) (any, error) {
		var in struct{ Name, Description string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		st, err := s.app.CreateStudio(userOf(r), in.Name, in.Description)
		if err != nil {
			return nil, err
		}
		x, _ := s.app.StudioAccess(userOf(r), st.ID)
		return s.studioView(st, x), nil
	}))
	r.Route("/studios/{sid}", func(r chi.Router) {
		r.Get("/", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var st model.Studio
			s.app.DB.First(&st, "id = ?", x.StudioID)
			return s.studioView(&st, x), nil
		}))
		r.Patch("/", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.CanManageStudio() {
				return nil, core.Forbidden("只有所有者或管理员可以修改工作室设置")
			}
			var in struct {
				Name        *string               `json:"name"`
				Logo        *string               `json:"logo"`
				Description *string               `json:"description"`
				Settings    *model.StudioSettings `json:"settings"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			up := map[string]any{}
			if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
				up["name"] = strings.TrimSpace(*in.Name)
			}
			if in.Logo != nil {
				up["logo"] = *in.Logo
			}
			if in.Description != nil {
				up["description"] = *in.Description
			}
			if in.Settings != nil {
				up["settings"] = model.JSON[model.StudioSettings]{V: *in.Settings}
			}
			var st model.Studio
			s.app.DB.First(&st, "id = ?", x.StudioID)
			if err := s.app.DB.Model(&st).Updates(up).Error; err != nil {
				return nil, err
			}
			s.app.DB.First(&st, "id = ?", x.StudioID)
			return s.studioView(&st, x), nil
		}))
		r.Delete("/", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if x.Role != model.RoleOwner {
				return nil, core.Forbidden("只有所有者可以解散工作室")
			}
			var st model.Studio
			s.app.DB.First(&st, "id = ?", x.StudioID)
			if st.Type == "personal" {
				return nil, core.Bad("个人工作室不能解散")
			}
			var running int64
			s.app.DB.Model(&model.Job{}).Where("studio_id = ? AND status IN ?", x.StudioID, []string{model.JobQueued, model.JobRunning}).Count(&running)
			if running > 0 {
				return nil, core.Bad("还有 %d 个任务正在运行，请等待完成后再解散", running)
			}
			t := time.Now()
			return nil, s.app.DB.Model(&st).Update("deleted_at", &t).Error
		}))
		r.Post("/logo", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.CanManageStudio() {
				return nil, core.Forbidden("")
			}
			as, err := s.saveUpload(r, "file", core.AssetInput{StudioID: x.StudioID, Kind: model.AssetImage, Origin: "upload", CreatedBy: x.User.ID})
			if err != nil {
				return nil, err
			}
			url := s.app.Store.SignedPath(as.StorageKey, 10*365*24*time.Hour, "")
			s.app.DB.Model(&model.Studio{}).Where("id = ?", x.StudioID).Update("logo", url)
			return map[string]any{"url": url}, nil
		}))
		// ---- members ----
		r.Get("/members", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var ms []model.StudioMember
			s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at asc").Find(&ms)
			monthStart := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
			var out []map[string]any
			for _, m := range ms {
				var u model.User
				if s.app.DB.First(&u, "id = ?", m.UserID).Error != nil {
					continue
				}
				var used int64
				s.app.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND user_id = ? AND created_at >= ?", x.StudioID, u.ID, monthStart).Select("COALESCE(SUM(credits),0)").Scan(&used)
				email := ""
				if u.Email != nil && x.IsManager() {
					email = *u.Email
				}
				out = append(out, map[string]any{"userId": u.ID, "nickname": u.Nickname, "avatarUrl": u.AvatarURL, "email": email, "role": m.Role, "status": m.Status,
					"creditCap": m.CreditCap, "creditsThisMonth": used, "joinedAt": m.CreatedAt, "lastLoginAt": u.LastLoginAt, "cv": u.CV.V, "message": m.Message})
			}
			return out, nil
		}))
		r.Patch("/members/{uid}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("只有所有者或管理员可以管理成员")
			}
			var m model.StudioMember
			if err := s.app.DB.First(&m, "studio_id = ? AND user_id = ?", x.StudioID, param(r, "uid")).Error; err != nil {
				return nil, core.NotFound("成员")
			}
			var in struct {
				Role      *string `json:"role"`
				CreditCap *int64  `json:"creditCap"`
				Approve   *bool   `json:"approve"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			up := map[string]any{}
			if in.Role != nil {
				if m.Role == model.RoleOwner || *in.Role == model.RoleOwner {
					return nil, core.Bad("所有者变更请使用「转让工作室」")
				}
				if x.Role != model.RoleOwner && *in.Role == model.RoleAdmin {
					return nil, core.Forbidden("只有所有者可以设置管理员")
				}
				up["role"] = *in.Role
			}
			if in.CreditCap != nil {
				up["credit_cap"] = *in.CreditCap
			}
			if in.Approve != nil {
				if *in.Approve {
					if s.app.SeatsUsed(x.StudioID) >= s.app.SeatLimit(x.StudioID) {
						return nil, core.Forbidden("成员席位已满，请升级套餐")
					}
					up["status"] = "active"
					s.app.Notify(m.UserID, x.StudioID, "join_approved", "你已加入工作室", "", "/")
				} else {
					return nil, s.app.DB.Delete(&m).Error
				}
			}
			return nil, s.app.DB.Model(&m).Updates(up).Error
		}))
		r.Delete("/members/{uid}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			uid := param(r, "uid")
			if !x.IsManager() && uid != x.User.ID {
				return nil, core.Forbidden("只有所有者或管理员可以移除成员")
			}
			var m model.StudioMember
			if err := s.app.DB.First(&m, "studio_id = ? AND user_id = ?", x.StudioID, uid).Error; err != nil {
				return nil, core.NotFound("成员")
			}
			if m.Role == model.RoleOwner {
				return nil, core.Bad("不能移除所有者")
			}
			s.app.DB.Where("user_id = ? AND book_id IN (?)", uid, s.app.DB.Model(&model.Book{}).Select("id").Where("studio_id = ?", x.StudioID)).Delete(&model.BookMember{})
			return nil, s.app.DB.Delete(&m).Error
		}))
		r.Post("/transfer", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in struct{ UserID string }
			bind(r, &in)
			return nil, s.app.TransferStudio(x, in.UserID)
		}))
		r.Get("/invitations", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.Invitation
			s.app.DB.Where("studio_id = ? AND revoked_at IS NULL AND expires_at > ?", x.StudioID, time.Now()).Order("created_at desc").Find(&list)
			return list, nil
		}))
		r.Post("/invitations", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				Target    string   `json:"target"`
				Role      string   `json:"role"`
				BookID    string   `json:"bookId"`
				Positions []string `json:"positions"`
				Days      int      `json:"days"`
				MaxUses   int      `json:"maxUses"`
			}
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			if in.BookID != "" {
				bx, err := s.app.BookAccess(x.User, in.BookID)
				if err != nil {
					return nil, err
				}
				if err := core.Need(bx, core.PermManage); err != nil {
					return nil, err
				}
			}
			inv, err := s.app.CreateInvitation(x, in.Target, in.Role, in.BookID, in.Positions, in.Days, in.MaxUses)
			if err != nil {
				return nil, err
			}
			return map[string]any{"invitation": inv, "link": s.app.Cfg.PublicURL + "/invite/" + inv.Token}, nil
		}))
		r.Delete("/invitations/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			t := time.Now()
			return nil, s.app.DB.Model(&model.Invitation{}).Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Update("revoked_at", &t).Error
		}))
		// ---- credits & usage ----
		r.Get("/credits", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var lots []model.CreditLot
			s.app.DB.Where("studio_id = ? AND remaining > 0", x.StudioID).Order("expires_at asc").Find(&lots)
			var rules []model.PricingRule
			s.app.DB.Order("feature asc").Find(&rules)
			return map[string]any{"balance": s.app.Balance(x.StudioID), "lots": lots, "pricing": rules}, nil
		}))
		r.Get("/ledger", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			q := s.app.DB.Model(&model.CreditLedger{}).Where("studio_id = ?", x.StudioID)
			if !x.CanViewBilling() {
				q = q.Where("user_id = ?", x.User.ID)
			}
			if k := r.URL.Query().Get("kind"); k != "" {
				q = q.Where("kind = ?", k)
			}
			return paginate[model.CreditLedger](r, q, "created_at desc")
		}))
		r.Get("/usage", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			from := qtime(r, "from", time.Now().AddDate(0, 0, -30))
			to := qtime(r, "to", time.Now()).Add(24 * time.Hour)
			q := s.app.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND created_at BETWEEN ? AND ?", x.StudioID, from, to)
			if !x.CanViewBilling() {
				q = q.Where("user_id = ?", x.User.ID)
			}
			for _, f := range []string{"feature", "book_id", "user_id"} {
				if v := r.URL.Query().Get(strings.ReplaceAll(f, "_id", "Id")); v != "" {
					q = q.Where(f+" = ?", v)
				}
			}
			type agg struct {
				Day     string
				Feature string
				Credits int64
			}
			var rows []agg
			dayExpr := dbx.Day(s.app.DB, "created_at")
			q.Session(&gorm_session).Select(dayExpr + " AS day, feature, COALESCE(SUM(credits),0) AS credits").Group("day, feature").Scan(&rows)
			list, _ := paginate[model.UsageRecord](r, q, "created_at desc")
			return map[string]any{"series": rows, "records": list, "featureNames": core.FeatureNames}, nil
		}))
		r.Post("/promo", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			// kept for older clients: same as POST /redeem with this studio as the credits target
			var in struct{ Code string }
			bind(r, &in)
			return s.app.Redeem(x.User, in.Code, x.StudioID, clientIP(r))
		}))
		// ---- stats & payroll ----
		r.Get("/stats", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			from := qtime(r, "from", time.Now().AddDate(0, 0, -29).Truncate(24*time.Hour))
			to := qtime(r, "to", time.Now()).Add(24*time.Hour - time.Second)
			uid := r.URL.Query().Get("userId")
			if !x.IsManager() && x.Role != model.RoleProducer {
				uid = x.User.ID
			}
			return s.app.StudioStats(x.StudioID, from, to, uid), nil
		}))
		r.Get("/payroll", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() && x.Role != model.RoleProducer {
				return nil, core.Forbidden("")
			}
			if err := s.app.RequireFeature(x.StudioID, model.FeatPayroll, "统计与计薪"); err != nil {
				return nil, err
			}
			from := qtime(r, "from", time.Now().AddDate(0, -1, 0))
			to := qtime(r, "to", time.Now()).Add(24*time.Hour - time.Second)
			rows, totals := s.app.Payroll(x.StudioID, from, to, r.URL.Query().Get("all") == "1")
			return map[string]any{"rows": rows, "totals": totals}, nil
		}))
		r.Get("/payroll/export", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			from := qtime(r, "from", time.Now().AddDate(0, -1, 0))
			to := qtime(r, "to", time.Now()).Add(24*time.Hour - time.Second)
			url, name, err := s.app.PayrollXLSX(x.StudioID, from, to)
			return map[string]any{"url": url, "name": name}, err
		}))
		r.Post("/payroll/settle", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			var in struct {
				TaskIDs []string `json:"taskIds"`
			}
			bind(r, &in)
			return map[string]any{"settled": s.app.SettleTasks(x.StudioID, in.TaskIDs)}, nil
		}))
		crudStudio[model.PayRate](s, r, "/pay-rates", true)
		crudStudio[model.Series](s, r, "/series", false)
		crudStudio[model.EffectPreset](s, r, "/effect-presets", false)
		// ---- workflow templates ----
		r.Get("/workflows", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.WorkflowTemplate
			s.app.DB.Where("scope = ? OR studio_id = ?", model.ScopeSystem, x.StudioID).Order("scope desc, created_at asc").Find(&list)
			return list, nil
		}))
		r.Post("/workflows", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() && x.Role != model.RoleProducer {
				return nil, core.Forbidden("")
			}
			var in model.WorkflowTemplate
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			in.ID, in.Scope, in.StudioID = "", model.ScopeStudio, x.StudioID
			if strings.TrimSpace(in.Name) == "" || len(in.Stages.V) == 0 {
				return nil, core.Bad("请填写模板名称并至少添加一个环节")
			}
			return in, s.app.DB.Create(&in).Error
		}))
		r.Put("/workflows/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var t model.WorkflowTemplate
			if err := s.app.DB.First(&t, "id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Error; err != nil {
				return nil, core.NotFound("工作流模板")
			}
			var in model.WorkflowTemplate
			bind(r, &in)
			return t, s.app.DB.Model(&t).Updates(map[string]any{"name": in.Name, "stages": in.Stages, "note": in.Note}).Error
		}))
		r.Delete("/workflows/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Delete(&model.WorkflowTemplate{}).Error
		}))
		// ---- studio lexicon ----
		r.Get("/lexicon", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			q := s.app.DB.Where("scope = ? AND studio_id = ?", "studio", x.StudioID)
			if kw := r.URL.Query().Get("q"); kw != "" {
				q = q.Where(dbx.Like(q, "term"), "%"+kw+"%")
			}
			var list []model.LexiconEntry
			q.Order("created_at desc").Find(&list)
			return list, nil
		}))
		r.Post("/lexicon", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in model.LexiconEntry
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			if strings.TrimSpace(in.Term) == "" {
				return nil, core.Bad("请填写词条")
			}
			e := model.LexiconEntry{Scope: "studio", StudioID: x.StudioID, Term: strings.TrimSpace(in.Term), Pinyin: in.Pinyin, Replacement: in.Replacement, Note: in.Note, Status: "active", CreatedBy: x.User.ID}
			return e, s.app.DB.Create(&e).Error
		}))
		// ---- BYOK provider accounts ----
		r.Get("/providers", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.ProviderAccount
			s.app.DB.Where("scope = ? AND studio_id = ?", "studio", x.StudioID).Order("created_at asc").Find(&list)
			return list, nil
		}))
		r.Post("/providers", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("只有所有者或管理员可以配置密钥")
			}
			if err := s.app.RequireFeature(x.StudioID, model.FeatBYOK, "自带密钥"); err != nil {
				return nil, err
			}
			var in providerInput
			if err := bind(r, &in); err != nil {
				return nil, err
			}
			acc, err := s.saveProvider(nil, in, "studio", x.StudioID)
			return acc, err
		}))
		r.Patch("/providers/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			var acc model.ProviderAccount
			if err := s.app.DB.First(&acc, "id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Error; err != nil {
				return nil, core.NotFound("账号")
			}
			var in providerInput
			bind(r, &in)
			return s.saveProvider(&acc, in, "studio", x.StudioID)
		}))
		r.Delete("/providers/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Delete(&model.ProviderAccount{}).Error
		}))
		r.Post("/providers/{id}/test", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var acc model.ProviderAccount
			if err := s.app.DB.First(&acc, "id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Error; err != nil {
				return nil, core.NotFound("账号")
			}
			msg, err := s.app.TestAccount(r.Context(), &acc)
			return map[string]any{"message": msg}, err
		}))
		// ---- open platform ----
		r.Get("/api-keys", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.APIKey
			s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at desc").Find(&list)
			return list, nil
		}))
		r.Post("/api-keys", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
				IPs    []string `json:"ipAllowlist"`
				RPM    int      `json:"rateLimit"`
			}
			bind(r, &in)
			k, raw, err := s.app.CreateAPIKey(x, in.Name, in.Scopes, in.IPs, in.RPM)
			if err != nil {
				return nil, err
			}
			return map[string]any{"key": k, "secret": raw}, nil
		}))
		r.Patch("/api-keys/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			var in struct {
				Disabled *bool    `json:"disabled"`
				Name     *string  `json:"name"`
				RPM      *int     `json:"rateLimit"`
				IPs      []string `json:"ipAllowlist"`
			}
			bind(r, &in)
			up := map[string]any{}
			if in.Disabled != nil {
				up["disabled"] = *in.Disabled
			}
			if in.Name != nil {
				up["name"] = *in.Name
			}
			if in.RPM != nil {
				up["rate_limit"] = *in.RPM
			}
			if in.IPs != nil {
				up["ip_allowlist"] = model.Strings(in.IPs...)
			}
			return nil, s.app.DB.Model(&model.APIKey{}).Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Updates(up).Error
		}))
		r.Delete("/api-keys/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Delete(&model.APIKey{}).Error
		}))
		r.Get("/webhooks", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.Webhook
			s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at desc").Find(&list)
			return map[string]any{"items": list, "events": core.WebhookEvents}, nil
		}))
		r.Post("/webhooks", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() {
				return nil, core.Forbidden("")
			}
			var in model.Webhook
			bind(r, &in)
			if !strings.HasPrefix(in.URL, "http") {
				return nil, core.Bad("请填写有效的回调地址")
			}
			wh := model.Webhook{StudioID: x.StudioID, URL: in.URL, Secret: secure.Token(24), Events: in.Events, Enabled: true, Note: in.Note}
			if err := s.app.DB.Create(&wh).Error; err != nil {
				return nil, err
			}
			return map[string]any{"webhook": wh, "secret": wh.Secret}, nil
		}))
		r.Patch("/webhooks/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				URL     *string  `json:"url"`
				Events  []string `json:"events"`
				Enabled *bool    `json:"enabled"`
			}
			bind(r, &in)
			up := map[string]any{}
			if in.URL != nil {
				up["url"] = *in.URL
			}
			if in.Events != nil {
				up["events"] = model.Strings(in.Events...)
			}
			if in.Enabled != nil {
				up["enabled"] = *in.Enabled
			}
			return nil, s.app.DB.Model(&model.Webhook{}).Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Updates(up).Error
		}))
		r.Delete("/webhooks/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Delete(&model.Webhook{}).Error
		}))
		r.Post("/webhooks/{id}/test", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			s.app.FireWebhook(x.StudioID, "ping", map[string]any{"message": "这是一条测试消息"})
			return nil, nil
		}))
		r.Get("/webhooks/{id}/deliveries", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			q := s.app.DB.Model(&model.WebhookDelivery{}).Where("webhook_id = ? AND studio_id = ?", param(r, "id"), x.StudioID)
			return paginate[model.WebhookDelivery](r, q, "created_at desc")
		}))
		// ---- distribution channels ----
		r.Get("/channels", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var list []model.Channel
			s.app.DB.Where("studio_id = ?", x.StudioID).Order("created_at asc").Find(&list)
			return map[string]any{"items": list, "kinds": core.ChannelKinds}, nil
		}))
		r.Post("/channels", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			if !x.IsManager() && x.Role != model.RoleProducer {
				return nil, core.Forbidden("")
			}
			var in model.Channel
			bind(r, &in)
			if _, okk := core.ChannelKinds[in.Kind]; !okk {
				return nil, core.Bad("不支持的渠道类型")
			}
			c := model.Channel{StudioID: x.StudioID, Kind: in.Kind, Name: firstNonEmptyStr(in.Name, core.ChannelKinds[in.Kind]), Config: in.Config, Token: secure.Token(18), Enabled: true}
			if c.Config.V == nil {
				c.Config.V = map[string]any{}
			}
			return c, s.app.DB.Create(&c).Error
		}))
		r.Patch("/channels/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			var in struct {
				Name    *string         `json:"name"`
				Config  *map[string]any `json:"config"`
				Enabled *bool           `json:"enabled"`
			}
			bind(r, &in)
			up := map[string]any{}
			if in.Name != nil {
				up["name"] = *in.Name
			}
			if in.Config != nil {
				up["config"] = model.Map{V: *in.Config}
			}
			if in.Enabled != nil {
				up["enabled"] = *in.Enabled
			}
			return nil, s.app.DB.Model(&model.Channel{}).Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Updates(up).Error
		}))
		r.Delete("/channels/{id}", h(func(r *http.Request) (any, error) {
			x, err := s.studio(r)
			if err != nil {
				return nil, err
			}
			return nil, s.app.DB.Where("id = ? AND studio_id = ?", param(r, "id"), x.StudioID).Delete(&model.Channel{}).Error
		}))
		r.Get("/tts-providers", h(func(r *http.Request) (any, error) {
			if _, err := s.studio(r); err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, name := range tts.Names() {
				p, _ := tts.New(tts.Account{Provider: name})
				out = append(out, map[string]any{"provider": name, "capabilities": p.Capabilities()})
			}
			return out, nil
		}))
	})
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type providerInput struct {
	Kind        *string                `json:"kind"`
	Provider    *string                `json:"provider"`
	Name        *string                `json:"name"`
	BaseURL     *string                `json:"baseUrl"`
	APIKey      *string                `json:"apiKey"`
	Model       *string                `json:"model"`
	Config      *map[string]any        `json:"config"`
	Enabled     *bool                  `json:"enabled"`
	Priority    *int                   `json:"priority"`
	RPM         *int                   `json:"rpm"`
	Concurrency *int                   `json:"concurrency"`
	CostPerUnit *float64               `json:"costPerUnit"`
	Models      *[]model.ProviderModel `json:"models"`
}

func (s *Server) saveProvider(acc *model.ProviderAccount, in providerInput, scope, studioID string) (*model.ProviderAccount, error) {
	defer s.app.InvalidateTTSProviders()
	creating := acc == nil
	if creating {
		if in.Kind == nil || in.Provider == nil {
			return nil, core.Bad("请选择服务类型与平台")
		}
		acc = &model.ProviderAccount{Scope: scope, StudioID: studioID, Enabled: true, Priority: 1, Concurrency: 4, RPM: 120, Config: model.EmptyMap(), HealthStatus: "unknown"}
		acc.Models.V = []model.ProviderModel{}
	}
	if in.Kind != nil {
		acc.Kind = *in.Kind
	}
	if in.Provider != nil {
		acc.Provider = *in.Provider
	}
	if in.Name != nil {
		acc.Name = strings.TrimSpace(*in.Name)
	}
	if in.BaseURL != nil {
		acc.BaseURL = strings.TrimSpace(*in.BaseURL)
	}
	if in.APIKey != nil && *in.APIKey != "" {
		enc, err := secure.Encrypt(s.app.Cfg.SecretKey, strings.TrimSpace(*in.APIKey))
		if err != nil {
			return nil, err
		}
		acc.CredentialsEnc, acc.KeyLast4 = enc, secure.Last4(strings.TrimSpace(*in.APIKey))
	}
	if in.Model != nil {
		acc.Model = strings.TrimSpace(*in.Model)
	}
	if in.Config != nil {
		acc.Config.V = *in.Config
	}
	if in.Enabled != nil {
		acc.Enabled = *in.Enabled
	}
	if in.Priority != nil {
		acc.Priority = *in.Priority
	}
	if in.RPM != nil {
		acc.RPM = *in.RPM
	}
	if in.Concurrency != nil {
		acc.Concurrency = *in.Concurrency
	}
	if in.CostPerUnit != nil {
		acc.CostPerUnit = *in.CostPerUnit
	}
	if in.Models != nil {
		acc.Models.V = *in.Models
	}
	if acc.Name == "" {
		acc.Name = acc.Provider
	}
	if acc.Kind == model.KindLLM {
		switch acc.Provider {
		case model.ProtoOpenAIChat, model.ProtoOpenAIResponses, model.ProtoAnthropic:
		default:
			return nil, core.Bad("大模型协议只支持 openai_chat、openai_responses、anthropic")
		}
	}
	if creating {
		return acc, s.app.DB.Create(acc).Error
	}
	return acc, s.app.DB.Save(acc).Error
}
