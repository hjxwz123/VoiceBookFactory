package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"voicebook/internal/ai"
	"voicebook/internal/audio"
	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
	"voicebook/internal/secure"
	"voicebook/internal/version"
)

func requireRole(r *http.Request, roles ...string) error {
	if userOf(r).HasAdminRole(roles...) {
		return nil
	}
	return core.Forbidden("你的管理员角色无权执行此操作")
}

func (s *Server) adminRoutes(r chi.Router) {
	r.Get("/events", s.events)
	r.Get("/me", h(func(r *http.Request) (any, error) { return userView(userOf(r)), nil }))
	r.Post("/me/password", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var in struct{ Old, New string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if !secure.CheckPassword(u.PasswordHash, in.Old) {
			return nil, core.Bad("当前密码不正确")
		}
		if err := core.ValidatePassword(in.New); err != nil {
			return nil, err
		}
		hash, _ := secure.HashPassword(in.New)
		s.app.DB.Model(u).Update("password_hash", hash)
		s.app.RevokeAll(u.ID, sessionOf(r))
		s.app.Audit(u.ID, "admin.password", "user", u.ID, "修改管理员密码", clientIP(r), nil)
		return nil, nil
	}))
	r.Get("/launch-check", h(func(r *http.Request) (any, error) { return s.app.LaunchCheck(), nil }))

	// ---------------- dashboard ----------------
	r.Get("/dashboard", h(func(r *http.Request) (any, error) {
		db := s.app.DB
		now := time.Now()
		dayAgo, weekAgo, monthAgo := now.Add(-24*time.Hour), now.AddDate(0, 0, -7), now.AddDate(0, 0, -30)
		count := func(m any, where string, args ...any) int64 {
			var n int64
			db.Model(m).Where(where, args...).Count(&n)
			return n
		}
		sum := func(m any, col, where string, args ...any) float64 {
			var v float64
			db.Model(m).Where(where, args...).Select("COALESCE(SUM(" + col + "),0)").Scan(&v)
			return v
		}
		var dau int64
		db.Model(&model.Session{}).Where("last_used_at > ?", dayAgo).Distinct("user_id").Count(&dau)
		var mau int64
		db.Model(&model.Session{}).Where("last_used_at > ?", monthAgo).Distinct("user_id").Count(&mau)
		var paying int64
		db.Model(&model.Studio{}).Where("plan_id IN (?)", db.Model(&model.Plan{}).Select("id").Where("price_monthly > 0")).Count(&paying)
		// daily series for the last 30 days
		type dayRow struct {
			Day     string
			Credits float64
			Chars   float64
			Cost    float64
		}
		dayExpr := dbx.Day(db, "created_at")
		usage := []dayRow{}
		db.Model(&model.UsageRecord{}).Where("created_at > ?", monthAgo).Select(dayExpr + " AS day, COALESCE(SUM(credits),0) AS credits, COALESCE(SUM(CASE WHEN feature = 'tts' THEN quantity ELSE 0 END),0) AS chars, COALESCE(SUM(cost_cny),0) AS cost").Group("day").Order("day").Scan(&usage)
		type signupRow struct {
			Day string
			N   int64
		}
		signups := []signupRow{}
		db.Model(&model.User{}).Where("created_at > ?", monthAgo).Select(dayExpr + " AS day, COUNT(*) AS n").Group("day").Order("day").Scan(&signups)
		var providers []model.ProviderAccount
		db.Where("scope = ?", "platform").Order("kind, priority desc").Find(&providers)
		pv := []map[string]any{}
		for _, p := range providers {
			rate := 1.0
			if p.Calls > 0 {
				rate = 1 - float64(p.Failures)/float64(p.Calls)
			}
			lat := int64(0)
			if p.Calls > 0 {
				lat = p.TotalLatencyMs / p.Calls
			}
			pv = append(pv, map[string]any{"id": p.ID, "name": p.Name, "kind": p.Kind, "provider": p.Provider, "enabled": p.Enabled, "calls": p.Calls, "successRate": rate, "avgLatencyMs": lat, "health": p.HealthStatus, "note": p.HealthNote})
		}
		type laneRow struct {
			Lane   string
			Status string
			N      int64
		}
		lanes := []laneRow{}
		db.Model(&model.Job{}).Where("status IN ?", []string{model.JobQueued, model.JobRunning}).Select("lane, status, COUNT(*) AS n").Group("lane, status").Scan(&lanes)
		alerts := []map[string]any{}
		if n := count(&model.Job{}, "status = ? AND finished_at > ?", model.JobFailed, dayAgo); n > 0 {
			alerts = append(alerts, map[string]any{"level": "warning", "title": "近 24 小时有任务失败", "detail": itoa64(n) + " 个任务失败", "link": "/admin/jobs?status=failed"})
		}
		for _, p := range providers {
			if p.Enabled && p.HealthStatus == "error" {
				alerts = append(alerts, map[string]any{"level": "danger", "title": "供应商异常：" + p.Name, "detail": p.HealthNote, "link": "/admin/providers"})
			}
		}
		var llm int64
		db.Model(&model.ProviderAccount{}).Where("kind = ? AND enabled = ?", model.KindLLM, true).Count(&llm)
		if llm == 0 {
			alerts = append(alerts, map[string]any{"level": "danger", "title": "尚未配置大语言模型", "detail": "角色分析、AI 画本等核心功能不可用", "link": "/admin/providers"})
		}
		if n := count(&model.VoiceConsent{}, "status = ?", "pending"); n > 0 {
			alerts = append(alerts, map[string]any{"level": "info", "title": "克隆授权待审核", "detail": itoa64(n) + " 条", "link": "/admin/moderation?tab=consents"})
		}
		if n := count(&model.ModerationRecord{}, "status = ?", "pending"); n > 0 {
			alerts = append(alerts, map[string]any{"level": "info", "title": "内容待审核", "detail": itoa64(n) + " 条", "link": "/admin/moderation"})
		}
		return map[string]any{
			"users":   map[string]any{"total": count(&model.User{}, "1 = 1"), "new7d": count(&model.User{}, "created_at > ?", weekAgo), "dau": dau, "mau": mau},
			"studios": map[string]any{"total": count(&model.Studio{}, "deleted_at IS NULL"), "paying": paying, "team": count(&model.Studio{}, "type = ? AND deleted_at IS NULL", "team")},
			"content": map[string]any{"books": count(&model.Book{}, "deleted_at IS NULL"), "chapters": count(&model.Chapter{}, "1 = 1"), "renders24h": count(&model.Render{}, "created_at > ?", dayAgo),
				"chars24h": sum(&model.UsageRecord{}, "quantity", "feature = ? AND created_at > ?", core.FeatTTS, dayAgo), "audioMs": sum(&model.Chapter{}, "duration_ms", "1 = 1")},
			"credits": map[string]any{"consumed30d": sum(&model.UsageRecord{}, "credits", "created_at > ?", monthAgo), "cost30d": sum(&model.UsageRecord{}, "cost_cny", "created_at > ?", monthAgo),
				"granted30d": sum(&model.CreditLedger{}, "delta", "delta > 0 AND created_at > ?", monthAgo)},
			"usageSeries": usage, "signupSeries": signups, "providers": pv, "lanes": lanes, "alerts": alerts, "online": s.app.Hub.Online(),
		}, nil
	}))

	// ---------------- users ----------------
	r.Get("/users", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.User{})
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where(dbx.Like(q, "email")+" OR "+dbx.Like(q, "nickname")+" OR id = ?", "%"+kw+"%", "%"+kw+"%", kw)
		}
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if r.URL.Query().Get("admins") == "1" {
			q = q.Where("admin_roles <> '[]' AND admin_roles <> 'null'")
		}
		if rn := r.URL.Query().Get("realName"); rn != "" {
			q = q.Where("real_name_status = ?", rn)
		}
		p, err := paginate[model.User](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		us := p.Items.([]model.User)
		items := make([]map[string]any, 0, len(us))
		for i := range us {
			v := userView(&us[i])
			var studios int64
			s.app.DB.Model(&model.StudioMember{}).Where("user_id = ?", us[i].ID).Count(&studios)
			v["studios"] = studios
			v["balance"] = s.app.Balance(us[i].PersonalStudio).Available
			v["realName"] = us[i].RealName
			v["idNumberMasked"] = us[i].IDNumberMasked
			items = append(items, v)
		}
		p.Items = items
		return p, nil
	}))
	r.Get("/users/{id}", h(func(r *http.Request) (any, error) {
		var u model.User
		if err := s.app.DB.First(&u, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("用户")
		}
		var ms []model.StudioMember
		s.app.DB.Where("user_id = ?", u.ID).Find(&ms)
		var studios []map[string]any
		for _, m := range ms {
			var st model.Studio
			if s.app.DB.First(&st, "id = ?", m.StudioID).Error == nil {
				studios = append(studios, map[string]any{"id": st.ID, "name": st.Name, "type": st.Type, "role": m.Role, "balance": s.app.Balance(st.ID).Available})
			}
		}
		var sessions []model.Session
		s.app.DB.Where("user_id = ?", u.ID).Order("last_used_at desc").Limit(10).Find(&sessions)
		var voices []model.Voice
		s.app.DB.Where("owner_user_id = ?", u.ID).Find(&voices)
		var logs []model.ActivityLog
		s.app.DB.Where("actor_id = ?", u.ID).Order("created_at desc").Limit(20).Find(&logs)
		v := userView(&u)
		v["realName"], v["idNumberMasked"] = u.RealName, u.IDNumberMasked
		return map[string]any{"user": v, "studios": studios, "sessions": sessions, "voices": voices, "activity": logs}, nil
	}))
	r.Patch("/users/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		var u model.User
		if err := s.app.DB.First(&u, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("用户")
		}
		var in struct {
			Status     *string   `json:"status"`
			AdminRoles *[]string `json:"adminRoles"`
			Nickname   *string   `json:"nickname"`
			Reason     string    `json:"reason"`
		}
		bind(r, &in)
		up := map[string]any{}
		if in.Status != nil {
			if u.ID == userOf(r).ID {
				return nil, core.Bad("不能停用自己的账号")
			}
			up["status"] = *in.Status
			if *in.Status == model.UserDisabled {
				s.app.RevokeAll(u.ID, "")
			}
		}
		if in.AdminRoles != nil {
			if err := requireRole(r); err != nil || !userOf(r).HasAdminRole(model.AdminSuper) {
				return nil, core.Forbidden("只有超级管理员可以分配管理员角色")
			}
			up["admin_roles"] = model.Strings(*in.AdminRoles...)
		}
		if in.Nickname != nil {
			up["nickname"] = *in.Nickname
		}
		if err := s.app.DB.Model(&u).Updates(up).Error; err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "user.update", "user", u.ID, in.Reason, clientIP(r), up)
		return nil, nil
	}))
	r.Post("/users/{id}/reset-password", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		pw := "Vb" + secure.Digits(6) + "x"
		hash, _ := secure.HashPassword(pw)
		if err := s.app.DB.Model(&model.User{}).Where("id = ?", param(r, "id")).Updates(map[string]any{"password_hash": hash, "failed_logins": 0, "locked_until": nil}).Error; err != nil {
			return nil, err
		}
		s.app.RevokeAll(param(r, "id"), "")
		s.app.Audit(userOf(r).ID, "user.reset_password", "user", param(r, "id"), "", clientIP(r), nil)
		return map[string]any{"password": pw}, nil
	}))
	r.Post("/users/{id}/logout", h(func(r *http.Request) (any, error) {
		s.app.RevokeAll(param(r, "id"), "")
		s.app.Audit(userOf(r).ID, "user.logout_all", "user", param(r, "id"), "", clientIP(r), nil)
		return nil, nil
	}))
	r.Post("/users/{id}/realname", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor); err != nil {
			return nil, err
		}
		var in struct {
			Approve bool
			Note    string
		}
		bind(r, &in)
		st := "rejected"
		if in.Approve {
			st = "verified"
		}
		s.app.DB.Model(&model.User{}).Where("id = ?", param(r, "id")).Updates(map[string]any{"real_name_status": st, "real_name_note": in.Note})
		s.app.Notify(param(r, "id"), "", "realname", map[bool]string{true: "实名认证已通过", false: "实名认证未通过"}[in.Approve], in.Note, "/settings/account")
		s.app.Audit(userOf(r).ID, "user.realname", "user", param(r, "id"), in.Note, clientIP(r), map[string]any{"approve": in.Approve})
		return nil, nil
	}))

	// ---------------- studios ----------------
	r.Get("/studios", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Studio{}).Where("deleted_at IS NULL")
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where(dbx.Like(q, "name")+" OR id = ?", "%"+kw+"%", kw)
		}
		if t := r.URL.Query().Get("type"); t != "" {
			q = q.Where("type = ?", t)
		}
		if p := r.URL.Query().Get("planId"); p != "" {
			q = q.Where("plan_id = ?", p)
		}
		p, err := paginate[model.Studio](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.Studio)
		items := make([]map[string]any, 0, len(list))
		for i := range list {
			v := s.studioView(&list[i], nil)
			var owner model.User
			s.app.DB.Select("nickname", "email").First(&owner, "id = ?", list[i].OwnerID)
			v["owner"] = owner.Nickname
			items = append(items, v)
		}
		p.Items = items
		return p, nil
	}))
	r.Get("/studios/{id}", h(func(r *http.Request) (any, error) {
		var st model.Studio
		if err := s.app.DB.First(&st, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("工作室")
		}
		var members []model.StudioMember
		s.app.DB.Where("studio_id = ?", st.ID).Find(&members)
		var mv []map[string]any
		for _, m := range members {
			var u model.User
			s.app.DB.Select("id", "nickname", "email").First(&u, "id = ?", m.UserID)
			mv = append(mv, map[string]any{"userId": u.ID, "nickname": u.Nickname, "email": u.Email, "role": m.Role, "status": m.Status})
		}
		var books []model.Book
		s.app.DB.Select("id", "title", "status", "word_count", "created_at").Where("studio_id = ?", st.ID).Order("created_at desc").Limit(50).Find(&books)
		var ledger []model.CreditLedger
		s.app.DB.Where("studio_id = ?", st.ID).Order("created_at desc").Limit(30).Find(&ledger)
		return map[string]any{"studio": s.studioView(&st, nil), "members": mv, "books": books, "ledger": ledger}, nil
	}))
	r.Patch("/studios/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminFinance); err != nil {
			return nil, err
		}
		var in struct {
			PlanID       *string   `json:"planId"`
			Status       *string   `json:"status"`
			SeatLimit    *int      `json:"seatLimit"`
			StorageLimit *int64    `json:"storageLimit"`
			Features     *[]string `json:"features"`
			Reason       string    `json:"reason"`
		}
		bind(r, &in)
		var cur model.Studio
		if s.app.DB.Select("id", "plan_override_id").First(&cur, "id = ?", param(r, "id")).Error != nil {
			return nil, core.NotFound("工作室")
		}
		if in.PlanID != nil && *in.PlanID == cur.PlanOverrideID {
			in.PlanID = nil // unchanged: never re-grant contract credits on an unrelated save
		}
		up := map[string]any{}
		if in.PlanID != nil { // contract plan pinned to this studio ("" = follow the owner's membership)
			up["plan_override_id"] = *in.PlanID
			if *in.PlanID != "" {
				renew := time.Now().AddDate(0, 1, 0)
				up["plan_renew_at"] = &renew
			} else {
				up["plan_renew_at"] = nil
			}
		}
		if in.Status != nil {
			up["status"] = *in.Status
		}
		if in.SeatLimit != nil {
			up["seat_limit"] = *in.SeatLimit
		}
		if in.StorageLimit != nil {
			up["storage_limit"] = *in.StorageLimit
		}
		if in.Features != nil {
			up["features"] = model.Strings(*in.Features...)
		}
		if err := s.app.DB.Model(&model.Studio{}).Where("id = ?", param(r, "id")).Updates(up).Error; err != nil {
			return nil, err
		}
		if in.PlanID != nil {
			s.app.SyncStudioPlan(param(r, "id"))
			var p model.Plan
			if *in.PlanID != "" && s.app.DB.First(&p, "id = ?", *in.PlanID).Error == nil && p.MonthlyCredits > 0 {
				exp := time.Now().AddDate(0, 2, 0)
				s.app.Grant(param(r, "id"), p.MonthlyCredits, "plan", &exp, p.Name+"月度积分（合同开通）", userOf(r).ID)
			}
		}
		s.app.Audit(userOf(r).ID, "studio.update", "studio", param(r, "id"), in.Reason, clientIP(r), up)
		return nil, nil
	}))
	r.Post("/studios/{id}/credits", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminFinance, model.AdminOperator); err != nil {
			return nil, err
		}
		var in struct {
			Amount      int64  `json:"amount"`
			Reason      string `json:"reason"`
			ExpiresDays int    `json:"expiresDays"`
		}
		bind(r, &in)
		if strings.TrimSpace(in.Reason) == "" {
			return nil, core.Bad("请填写调整原因")
		}
		var err error
		if in.Amount > 0 {
			var exp *time.Time
			if in.ExpiresDays > 0 {
				t := time.Now().AddDate(0, 0, in.ExpiresDays)
				exp = &t
			}
			err = s.app.Grant(param(r, "id"), in.Amount, "grant", exp, in.Reason, userOf(r).ID)
		} else if in.Amount < 0 {
			err = s.app.Deduct(param(r, "id"), -in.Amount, in.Reason, userOf(r).ID)
		}
		if err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "credits.adjust", "studio", param(r, "id"), in.Reason, clientIP(r), map[string]any{"amount": in.Amount})
		return s.app.Balance(param(r, "id")), nil
	}))

	// ---------------- voices & assets & templates (official) ----------------
	r.Get("/voices", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Voice{})
		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = model.ScopeSystem
		}
		if scope != "all" {
			q = q.Where("scope = ?", scope)
		}
		for p, col := range map[string]string{"gender": "gender", "provider": "provider", "status": "status", "tier": "price_tier", "model": "model"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		if r.URL.Query().Get("crowd") == "1" {
			q = q.Where("crowd_pool = ?", true)
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where(dbx.Like(q, "name")+" OR "+dbx.Like(q, "tags"), "%"+kw+"%", "%"+kw+"%")
		}
		p, err := paginate[model.Voice](r, q, "sort_order asc, created_at desc")
		if err != nil {
			return nil, err
		}
		vs := p.Items.([]model.Voice)
		items := make([]map[string]any, 0, len(vs))
		for i := range vs {
			items = append(items, s.app.VoiceView(&vs[i], ""))
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/voices", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		var v model.Voice
		if err := bind(r, &v); err != nil {
			return nil, err
		}
		v.ID, v.Scope, v.Origin, v.Status = "", model.ScopeSystem, firstNonEmptyStr(v.Origin, model.VoicePreset), model.VoiceReady
		if v.Tags.V == nil {
			v.Tags.V = []string{}
		}
		if v.Emotions.V == nil {
			v.Emotions.V = []string{}
		}
		if v.Defaults.V.Speed == 0 {
			v.Defaults.V.Speed = 1
		}
		if v.Provider == "" {
			return nil, core.Bad("请选择语音平台")
		}
		if err := s.app.DB.Create(&v).Error; err != nil {
			return nil, err
		}
		s.regenSample(r.Context(), &v)
		s.app.Audit(userOf(r).ID, "voice.create", "voice", v.ID, "", clientIP(r), nil)
		return s.app.VoiceView(&v, ""), nil
	}))
	r.Put("/voices/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		var v model.Voice
		if err := s.app.DB.First(&v, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("音色")
		}
		id, scope := v.ID, v.Scope
		if err := bind(r, &v); err != nil {
			return nil, err
		}
		v.ID, v.Scope = id, scope
		if err := s.app.DB.Save(&v).Error; err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "voice.update", "voice", v.ID, "", clientIP(r), nil)
		return s.app.VoiceView(&v, ""), nil
	}))
	r.Delete("/voices/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminAuditor); err != nil {
			return nil, err
		}
		var v model.Voice
		if err := s.app.DB.First(&v, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("音色")
		}
		if r.URL.Query().Get("mode") == "disable" {
			s.app.DB.Model(&v).Updates(map[string]any{"status": model.VoiceDisabled, "listed": false, "status_note": "已被平台停用"})
			var chars []model.Character
			s.app.DB.Where("voice_id = ?", v.ID).Find(&chars)
			notified := map[string]bool{}
			for _, c := range chars {
				var b model.Book
				if s.app.DB.Select("studio_id", "title").First(&b, "id = ?", c.BookID).Error == nil && !notified[b.StudioID] {
					notified[b.StudioID] = true
					s.app.NotifyManagers(b.StudioID, "voice_disabled", "音色「"+v.Name+"」已被平台停用", "请为使用该音色的角色重新选角", "/books/"+c.BookID+"/characters")
				}
			}
		} else if err := s.app.DeleteVoice(r.Context(), &v); err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "voice.delete", "voice", v.ID, r.URL.Query().Get("reason"), clientIP(r), nil)
		return nil, nil
	}))
	r.Post("/voices/{id}/sample", h(func(r *http.Request) (any, error) {
		var v model.Voice
		if err := s.app.DB.First(&v, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("音色")
		}
		if err := s.regenSample(r.Context(), &v); err != nil {
			return nil, err
		}
		return s.app.VoiceView(&v, ""), nil
	}))
	r.Post("/voices/{id}/sample-upload", h(func(r *http.Request) (any, error) {
		var v model.Voice
		if err := s.app.DB.First(&v, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("音色")
		}
		as, err := s.saveUpload(r, "file", core.AssetInput{Scope: model.ScopeSystem, Kind: model.AssetPreview, Name: v.Name + " 试听", Origin: "upload", CreatedBy: userOf(r).ID})
		if err != nil {
			return nil, err
		}
		s.app.DB.Model(&v).Update("sample_asset_id", as.ID)
		return map[string]any{"url": s.app.AssetURL(as)}, nil
	}))
	r.Get("/assets", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Asset{}).Where("scope = ? AND kind IN ?", model.ScopeSystem, model.LibraryKinds)
		if k := r.URL.Query().Get("kind"); k != "" {
			q = q.Where("kind = ?", k)
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where(dbx.Like(q, "name")+" OR "+dbx.Like(q, "tags"), "%"+kw+"%", "%"+kw+"%")
		}
		p, err := paginate[model.Asset](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.Asset)
		items := make([]map[string]any, 0, len(list))
		for i := range list {
			items = append(items, s.app.AssetView(&list[i]))
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/assets", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		kind := r.FormValue("kind")
		if kind != model.AssetBGM && kind != model.AssetAmbience && kind != model.AssetImage {
			kind = model.AssetSFX
		}
		var tags []string
		if t := r.FormValue("tags"); t != "" {
			tags = strings.Split(t, ",")
		}
		as, err := s.saveUpload(r, "file", core.AssetInput{Scope: model.ScopeSystem, Kind: kind, Name: r.FormValue("name"), Origin: "system", License: firstNonEmptyStr(r.FormValue("license"), "commercial"), Tags: tags, Listed: true, CreatedBy: userOf(r).ID})
		if err != nil {
			return nil, err
		}
		return s.app.AssetView(as), nil
	}))
	r.Patch("/assets/{id}", h(func(r *http.Request) (any, error) {
		var as model.Asset
		if err := s.app.DB.First(&as, "id = ? AND scope = ?", param(r, "id"), model.ScopeSystem).Error; err != nil {
			return nil, core.NotFound("素材")
		}
		var in struct {
			Name        *string   `json:"name"`
			Tags        *[]string `json:"tags"`
			Listed      *bool     `json:"listed"`
			License     *string   `json:"license"`
			Description *string   `json:"description"`
		}
		bind(r, &in)
		up := map[string]any{}
		if in.Name != nil {
			up["name"] = *in.Name
		}
		if in.Tags != nil {
			up["tags"] = model.Strings(*in.Tags...)
		}
		if in.Listed != nil {
			up["listed"] = *in.Listed
		}
		if in.License != nil {
			up["license"] = *in.License
		}
		if in.Description != nil {
			up["description"] = *in.Description
		}
		return nil, s.app.DB.Model(&as).Updates(up).Error
	}))
	r.Delete("/assets/{id}", h(func(r *http.Request) (any, error) {
		var as model.Asset
		if err := s.app.DB.First(&as, "id = ? AND scope = ?", param(r, "id"), model.ScopeSystem).Error; err != nil {
			return nil, core.NotFound("素材")
		}
		s.app.DeleteAssetFile(&as)
		return nil, s.app.DB.Delete(&as).Error
	}))
	crudAdmin[model.AudioTemplate](s, r, "/templates", adminCrudOpts{Roles: []string{model.AdminOperator}, Where: func(q *gorm.DB) *gorm.DB { return q.Where("scope = ?", model.ScopeSystem) },
		Before: func(item any) error {
			t := item.(*model.AudioTemplate)
			t.Scope, t.StudioID = model.ScopeSystem, ""
			return s.app.NormalizeTemplate("", t)
		}, Audit: "template"})

	// ---------------- AI providers ----------------
	r.Get("/providers/presets", h(func(r *http.Request) (any, error) { return ai.Presets, nil }))
	r.Get("/providers", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.ProviderAccount{})
		if r.URL.Query().Get("scope") == "studio" {
			q = q.Where("scope = ?", "studio")
		} else {
			q = q.Where("scope = ?", "platform")
		}
		if k := r.URL.Query().Get("kind"); k != "" {
			q = q.Where("kind = ?", k)
		}
		var list []model.ProviderAccount
		q.Order("kind asc, priority desc, created_at asc").Find(&list)
		return list, nil
	}))
	r.Post("/providers", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		var in providerInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		acc, err := s.saveProvider(nil, in, "platform", "")
		if err == nil {
			s.app.Audit(userOf(r).ID, "provider.create", "provider", acc.ID, "", clientIP(r), map[string]any{"kind": acc.Kind, "provider": acc.Provider})
		}
		return acc, err
	}))
	r.Patch("/providers/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		var acc model.ProviderAccount
		if err := s.app.DB.First(&acc, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("账号")
		}
		var in providerInput
		bind(r, &in)
		out, err := s.saveProvider(&acc, in, acc.Scope, acc.StudioID)
		if err == nil {
			s.app.Audit(userOf(r).ID, "provider.update", "provider", acc.ID, "", clientIP(r), nil)
		}
		return out, err
	}))
	r.Delete("/providers/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "provider.delete", "provider", param(r, "id"), "", clientIP(r), nil)
		defer s.app.InvalidateTTSProviders()
		return nil, s.app.DB.Where("id = ?", param(r, "id")).Delete(&model.ProviderAccount{}).Error
	}))
	r.Post("/providers/{id}/test", h(func(r *http.Request) (any, error) {
		var acc model.ProviderAccount
		if err := s.app.DB.First(&acc, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("账号")
		}
		msg, err := s.app.TestAccount(r.Context(), &acc)
		if err != nil {
			return map[string]any{"ok": false, "message": err.Error()}, nil
		}
		return map[string]any{"ok": true, "message": msg}, nil
	}))
	r.Post("/providers/{id}/sync-voices", h(func(r *http.Request) (any, error) {
		var acc model.ProviderAccount
		if err := s.app.DB.First(&acc, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("账号")
		}
		n, err := s.app.SyncProviderVoices(r.Context(), &acc)
		return map[string]any{"imported": n}, err
	}))
	r.Post("/providers/{id}/import-previews", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		var in struct {
			URL string `json:"url"`
		}
		bind(r, &in)
		return s.app.StartPreviewPackImport(userOf(r).ID, in.URL)
	}))
	crudAdmin[model.RoutingRule](s, r, "/routing", adminCrudOpts{Roles: []string{model.AdminOps}, Filters: map[string]string{"feature": "feature"}, Order: "feature asc, tier asc", Audit: "routing"})
	crudAdmin[model.PricingRule](s, r, "/pricing", adminCrudOpts{Roles: []string{model.AdminFinance}, Order: "feature asc, tier asc", Audit: "pricing"})

	// ---------------- prompts & evals ----------------
	r.Get("/prompts", h(func(r *http.Request) (any, error) {
		var list []model.PromptTemplate
		s.app.DB.Order("key asc, version desc").Find(&list)
		grouped := map[string][]model.PromptTemplate{}
		for _, p := range list {
			grouped[p.Key] = append(grouped[p.Key], p)
		}
		var out []map[string]any
		for _, d := range core.DefaultPrompts {
			out = append(out, map[string]any{"key": d.Key, "name": d.Name, "versions": grouped[d.Key], "default": d.Content})
		}
		return out, nil
	}))
	r.Post("/prompts", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		var in struct {
			Key, Content, Note string
			Activate           bool
		}
		bind(r, &in)
		if strings.TrimSpace(in.Content) == "" {
			return nil, core.Bad("提示词不能为空")
		}
		var maxV int
		s.app.DB.Model(&model.PromptTemplate{}).Where("key = ?", in.Key).Select("COALESCE(MAX(version),0)").Scan(&maxV)
		name := in.Key
		for _, d := range core.DefaultPrompts {
			if d.Key == in.Key {
				name = d.Name
			}
		}
		p := model.PromptTemplate{Key: in.Key, Name: name, Version: maxV + 1, Content: in.Content, Note: in.Note, Rollout: 100, CreatedBy: userOf(r).ID}
		if err := s.app.DB.Create(&p).Error; err != nil {
			return nil, err
		}
		if in.Activate {
			s.app.DB.Model(&model.PromptTemplate{}).Where("key = ?", in.Key).Update("active", false)
			s.app.DB.Model(&p).Update("active", true)
		}
		s.app.Audit(userOf(r).ID, "prompt.create", "prompt", p.ID, in.Note, clientIP(r), map[string]any{"key": in.Key, "version": p.Version})
		return p, nil
	}))
	r.Post("/prompts/{id}/activate", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		var p model.PromptTemplate
		if err := s.app.DB.First(&p, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("提示词版本")
		}
		var in struct {
			Rollout int `json:"rollout"`
		}
		bind(r, &in)
		if in.Rollout <= 0 || in.Rollout > 100 {
			in.Rollout = 100
		}
		if in.Rollout == 100 {
			s.app.DB.Model(&model.PromptTemplate{}).Where("key = ?", p.Key).Update("active", false)
		}
		s.app.Audit(userOf(r).ID, "prompt.activate", "prompt", p.ID, "", clientIP(r), map[string]any{"key": p.Key, "version": p.Version, "rollout": in.Rollout})
		return nil, s.app.DB.Model(&p).Updates(map[string]any{"active": true, "rollout": in.Rollout}).Error
	}))
	r.Post("/prompts/{id}/deactivate", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps); err != nil {
			return nil, err
		}
		return nil, s.app.DB.Model(&model.PromptTemplate{}).Where("id = ?", param(r, "id")).Update("active", false).Error
	}))
	r.Post("/prompts/test", h(func(r *http.Request) (any, error) {
		var in struct{ Key, Content, Sample, AccountID string }
		bind(r, &in)
		if strings.TrimSpace(in.Sample) == "" {
			return nil, core.Bad("请输入测试文本")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		defer cancel()
		return s.app.TestPrompt(ctx, in.Key, in.Content, in.Sample, in.AccountID)
	}))
	crudAdmin[model.EvalSet](s, r, "/evals", adminCrudOpts{Roles: []string{model.AdminOps}, Audit: "eval", Before: func(item any) error {
		e := item.(*model.EvalSet)
		if e.Kind == "" {
			e.Kind = "script_annotation"
		}
		if e.LastRun.V == nil {
			e.LastRun.V = map[string]any{}
		}
		if e.History.V == nil {
			e.History.V = []map[string]any{}
		}
		return nil
	}})
	r.Post("/evals/{id}/run", h(func(r *http.Request) (any, error) {
		var set model.EvalSet
		if err := s.app.DB.First(&set, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("评测集")
		}
		var in struct {
			AccountID     string `json:"accountId"`
			Prompt        string `json:"prompt"`
			PromptVersion int    `json:"promptVersion"`
		}
		bind(r, &in)
		j := &model.Job{Type: "eval_run", RefType: "eval", RefID: set.ID, Title: "评测 · " + set.Name, CreatedBy: userOf(r).ID, MaxAttempts: 1,
			Payload: model.Map{V: map[string]any{"accountId": in.AccountID, "prompt": in.Prompt, "promptVersion": in.PromptVersion}}}
		return j, s.app.Q.Enqueue(j)
	}))
	r.Get("/jobs/{id}", h(func(r *http.Request) (any, error) {
		var j model.Job
		if err := s.app.DB.First(&j, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("任务")
		}
		return j, nil
	}))

	// ---------------- platform lexicon ----------------
	crudAdmin[model.LexiconEntry](s, r, "/lexicon", adminCrudOpts{Roles: []string{model.AdminOperator, model.AdminAuditor}, Search: []string{"term", "pinyin"}, Filters: map[string]string{"status": "status"},
		Where: func(q *gorm.DB) *gorm.DB { return q.Where("scope = ?", "platform") }, Order: "status desc, created_at desc", Audit: "lexicon",
		Before: func(item any) error {
			e := item.(*model.LexiconEntry)
			e.Scope = "platform"
			e.StudioID, e.BookID = "", ""
			if e.Status == "" {
				e.Status = "active"
			}
			return nil
		}})
	r.Post("/lexicon/{id}/review", h(func(r *http.Request) (any, error) {
		var in struct{ Approve bool }
		bind(r, &in)
		st := "rejected"
		if in.Approve {
			st = "active"
		}
		s.app.Audit(userOf(r).ID, "lexicon.review", "lexicon", param(r, "id"), "", clientIP(r), map[string]any{"approve": in.Approve})
		return nil, s.app.DB.Model(&model.LexiconEntry{}).Where("id = ? AND scope = ?", param(r, "id"), "platform").Update("status", st).Error
	}))

	// ---------------- billing ----------------
	crudAdmin[model.Plan](s, r, "/plans", adminCrudOpts{Roles: []string{model.AdminFinance}, Order: "sort_order asc, level asc", Audit: "plan", Before: func(item any) error {
		p := item.(*model.Plan)
		for _, l := range []*model.StringList{&p.Features, &p.Benefits, &p.ExportFormats} {
			if l.V == nil {
				l.V = []string{}
			}
		}
		p.Code = strings.ToLower(strings.TrimSpace(p.Code))
		p.Name = strings.TrimSpace(p.Name)
		if p.Code == "" || p.Name == "" {
			return core.Bad("请填写会员编码与名称")
		}
		if p.CreditRate < 0 || p.CreditRate > 100 {
			return core.Bad("积分折扣应在 1~100 之间（100 表示不打折）")
		}
		if p.QueuePriority < 0 || p.QueuePriority > 10 {
			return core.Bad("排队优先级应在 0~10 之间")
		}
		for _, v := range []int{p.MaxStudios, p.Seats, p.MaxBooks, p.StorageGB, p.CloneVoices, p.MaxBookWords, p.MaxUploadMB, p.CreditMonths} {
			if v < 0 {
				return core.Bad("限额不能为负数（0 表示不限）")
			}
		}
		if p.MonthlyCredits < 0 || p.BonusCredits < 0 || p.PriceMonthly < 0 || p.PriceYearly < 0 || p.OriginalMonthly < 0 {
			return core.Bad("价格和积分不能为负数")
		}
		var dup int64
		s.app.DB.Model(&model.Plan{}).Where("code = ? AND id <> ?", p.Code, p.ID).Count(&dup)
		if dup > 0 {
			return core.Bad("编码 %s 已被其他会员使用", p.Code)
		}
		return nil
	}, BeforeDelete: func(id string) error {
		var p model.Plan
		if s.app.DB.First(&p, "id = ?", id).Error != nil {
			return core.NotFound("会员套餐")
		}
		if p.ID == s.app.DefaultPlanFor().ID {
			return core.Bad("「%s」是默认套餐，不能删除；可先在「系统设置 → 计费」更换默认套餐", p.Name)
		}
		var members, periods, batches, studios int64
		s.app.DB.Model(&model.UserMembership{}).Where("plan_id = ?", id).Count(&members)
		s.app.DB.Model(&model.MembershipPeriod{}).Where("plan_id = ? AND revoked = ? AND (expires_at IS NULL OR expires_at > ?)", id, false, time.Now()).Count(&periods)
		s.app.DB.Model(&model.RedeemBatch{}).Where("plan_id = ? AND status = ?", id, "active").Count(&batches)
		s.app.DB.Model(&model.Studio{}).Where("plan_override_id = ? AND deleted_at IS NULL", id).Count(&studios)
		if members+periods+batches+studios > 0 {
			return core.Bad("「%s」仍在使用中（会员 %d 人、有效期记录 %d 条、兑换码批次 %d 个、合同工作室 %d 个），可先取消「公开」让新用户看不到", p.Name, max(members, periods), periods, batches, studios)
		}
		return nil
	}})
	r.Get("/ledger", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.CreditLedger{})
		for p, col := range map[string]string{"studioId": "studio_id", "kind": "kind", "feature": "feature"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		return paginate[model.CreditLedger](r, q, "created_at desc")
	}))
	r.Get("/finance", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminFinance); err != nil {
			return nil, err
		}
		from := qtime(r, "from", time.Now().AddDate(0, -1, 0))
		to := qtime(r, "to", time.Now()).Add(24 * time.Hour)
		type row struct {
			Feature string
			Credits float64
			Cost    float64
			Qty     float64
		}
		var byFeature []row
		s.app.DB.Model(&model.UsageRecord{}).Where("created_at BETWEEN ? AND ?", from, to).Select("feature, COALESCE(SUM(credits),0) AS credits, COALESCE(SUM(cost_cny),0) AS cost, COALESCE(SUM(quantity),0) AS qty").Group("feature").Scan(&byFeature)
		type srow struct {
			StudioID string
			Credits  float64
			Cost     float64
		}
		var byStudio []srow
		s.app.DB.Model(&model.UsageRecord{}).Where("created_at BETWEEN ? AND ?", from, to).Select("studio_id, COALESCE(SUM(credits),0) AS credits, COALESCE(SUM(cost_cny),0) AS cost").Group("studio_id").Order("credits desc").Limit(30).Scan(&byStudio)
		var sv []map[string]any
		for _, x := range byStudio {
			var st model.Studio
			s.app.DB.Select("name").First(&st, "id = ?", x.StudioID)
			sv = append(sv, map[string]any{"studioId": x.StudioID, "name": st.Name, "credits": x.Credits, "cost": x.Cost})
		}
		type prow struct {
			Provider string
			Cost     float64
			Qty      float64
		}
		var byProvider []prow
		s.app.DB.Model(&model.UsageRecord{}).Where("created_at BETWEEN ? AND ?", from, to).Select("provider, COALESCE(SUM(cost_cny),0) AS cost, COALESCE(SUM(quantity),0) AS qty").Group("provider").Scan(&byProvider)
		var granted float64
		s.app.DB.Model(&model.CreditLedger{}).Where("delta > 0 AND kind IN ? AND created_at BETWEEN ? AND ?", []string{"grant", "plan", "promo"}, from, to).Select("COALESCE(SUM(delta),0)").Scan(&granted)
		return map[string]any{"byFeature": byFeature, "byStudio": sv, "byProvider": byProvider, "granted": granted, "featureNames": core.FeatureNames}, nil
	}))
	r.Get("/withdrawals", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Withdrawal{})
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		return paginate[model.Withdrawal](r, q, "created_at desc")
	}))
	r.Post("/withdrawals/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminFinance); err != nil {
			return nil, err
		}
		var w model.Withdrawal
		if err := s.app.DB.First(&w, "id = ? AND status = ?", param(r, "id"), "pending").Error; err != nil {
			return nil, core.NotFound("提现申请")
		}
		var in struct {
			Approve bool
			Note    string
		}
		bind(r, &in)
		if in.Approve {
			s.app.DB.Model(&w).Updates(map[string]any{"status": "done", "note": in.Note})
		} else {
			var u model.User
			s.app.DB.First(&u, "id = ?", w.UserID)
			s.app.Grant(u.PersonalStudio, w.Amount, "refund", nil, "提现驳回退回", userOf(r).ID)
			s.app.DB.Model(&w).Updates(map[string]any{"status": "rejected", "note": in.Note})
		}
		s.app.Audit(userOf(r).ID, "withdrawal.review", "withdrawal", w.ID, in.Note, clientIP(r), map[string]any{"approve": in.Approve})
		return nil, nil
	}))
	r.Get("/listings", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.VoiceListing{})
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		p, err := paginate[model.VoiceListing](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.VoiceListing)
		var items []map[string]any
		for _, l := range list {
			var v model.Voice
			s.app.DB.First(&v, "id = ?", l.VoiceID)
			var u model.User
			s.app.DB.Select("nickname").First(&u, "id = ?", l.OwnerUserID)
			items = append(items, map[string]any{"listing": l, "voice": s.app.VoiceView(&v, ""), "owner": u.Nickname})
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/listings/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor, model.AdminOperator); err != nil {
			return nil, err
		}
		var in struct {
			Approve bool
			Note    string
		}
		bind(r, &in)
		st := "rejected"
		if in.Approve {
			st = "listed"
		}
		var l model.VoiceListing
		if err := s.app.DB.First(&l, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("上架申请")
		}
		s.app.DB.Model(&l).Update("status", st)
		s.app.Notify(l.OwnerUserID, "", "listing", map[bool]string{true: "你的音色已上架声音市场", false: "音色上架申请未通过"}[in.Approve], in.Note, "/voices")
		s.app.Audit(userOf(r).ID, "listing.review", "listing", l.ID, in.Note, clientIP(r), nil)
		return nil, nil
	}))

	// ---------------- compliance ----------------
	r.Get("/moderation", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.ModerationRecord{})
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if res := r.URL.Query().Get("result"); res != "" {
			q = q.Where("result = ?", res)
		}
		p, err := paginate[model.ModerationRecord](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.ModerationRecord)
		var items []map[string]any
		for _, m := range list {
			var b model.Book
			s.app.DB.Select("id", "title", "status").First(&b, "id = ?", m.BookID)
			items = append(items, map[string]any{"record": m, "bookTitle": b.Title, "bookStatus": b.Status})
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/moderation/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor); err != nil {
			return nil, err
		}
		var in struct {
			Approve bool
			Note    string
		}
		bind(r, &in)
		s.app.Audit(userOf(r).ID, "moderation.review", "moderation", param(r, "id"), in.Note, clientIP(r), map[string]any{"approve": in.Approve})
		return nil, s.app.ReviewModeration(userOf(r).ID, param(r, "id"), in.Approve, in.Note)
	}))
	r.Get("/consents", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.VoiceConsent{})
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if t := r.URL.Query().Get("type"); t != "" {
			q = q.Where("type = ?", t)
		}
		p, err := paginate[model.VoiceConsent](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.VoiceConsent)
		var items []map[string]any
		for _, c := range list {
			var v model.Voice
			s.app.DB.First(&v, "id = ?", c.VoiceID)
			var u model.User
			s.app.DB.Select("nickname", "email").First(&u, "id = ?", c.UserID)
			items = append(items, map[string]any{"consent": c, "voiceName": v.Name, "voiceStatus": v.Status, "user": u.Nickname, "sampleUrl": s.app.AssetURLByID(v.SampleAssetID),
				"evidenceUrl": s.app.AssetURLByID(c.EvidenceAsset), "documentUrl": s.app.AssetURLByID(c.DocumentAsset)})
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/consents/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor); err != nil {
			return nil, err
		}
		var in struct {
			Approve bool
			Note    string
		}
		bind(r, &in)
		s.app.Audit(userOf(r).ID, "consent.review", "consent", param(r, "id"), in.Note, clientIP(r), map[string]any{"approve": in.Approve})
		return nil, s.app.ReviewConsent(userOf(r).ID, param(r, "id"), in.Approve, in.Note)
	}))
	r.Get("/reports", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Report{})
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		p, err := paginate[model.Report](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.Report)
		var items []map[string]any
		for _, rep := range list {
			var u model.User
			s.app.DB.Select("nickname").First(&u, "id = ?", rep.ReporterID)
			target := ""
			switch rep.TargetType {
			case "voice":
				var v model.Voice
				s.app.DB.Select("name").First(&v, "id = ?", rep.TargetID)
				target = v.Name
			case "book":
				var b model.Book
				s.app.DB.Select("title").First(&b, "id = ?", rep.TargetID)
				target = b.Title
			case "user":
				var t model.User
				s.app.DB.Select("nickname").First(&t, "id = ?", rep.TargetID)
				target = t.Nickname
			}
			items = append(items, map[string]any{"report": rep, "reporter": u.Nickname, "targetName": target})
		}
		p.Items = items
		return p, nil
	}))
	r.Post("/reports/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor); err != nil {
			return nil, err
		}
		var rep model.Report
		if err := s.app.DB.First(&rep, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("举报")
		}
		var in struct {
			Status, Resolution, Action string
		}
		bind(r, &in)
		switch in.Action {
		case "disable_voice":
			s.app.DB.Model(&model.Voice{}).Where("id = ?", rep.TargetID).Updates(map[string]any{"status": model.VoiceDisabled, "listed": false, "status_note": "因举报被停用"})
			s.app.DB.Model(&model.VoiceListing{}).Where("voice_id = ?", rep.TargetID).Update("status", "delisted")
		case "archive_book":
			s.app.DB.Model(&model.Book{}).Where("id = ?", rep.TargetID).Update("status", model.BookArchived)
		case "disable_user":
			s.app.DB.Model(&model.User{}).Where("id = ?", rep.TargetID).Update("status", model.UserDisabled)
			s.app.RevokeAll(rep.TargetID, "")
		}
		s.app.DB.Model(&rep).Updates(map[string]any{"status": firstNonEmptyStr(in.Status, "resolved"), "resolution": in.Resolution, "handler_id": userOf(r).ID})
		s.app.Audit(userOf(r).ID, "report.resolve", "report", rep.ID, in.Resolution, clientIP(r), map[string]any{"action": in.Action})
		return nil, nil
	}))
	crudAdmin[model.SensitiveWord](s, r, "/sensitive-words", adminCrudOpts{Roles: []string{model.AdminAuditor}, Search: []string{"word", "category"}, Filters: map[string]string{"level": "level"}, Audit: "sensitive"})
	r.Post("/sensitive-words/import", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminAuditor); err != nil {
			return nil, err
		}
		var in struct{ Words, Level, Category string }
		bind(r, &in)
		n := 0
		for _, w := range strings.FieldsFunc(in.Words, func(r rune) bool { return r == '\n' || r == ',' || r == '，' }) {
			w = strings.TrimSpace(w)
			if w == "" {
				continue
			}
			if s.app.DB.Create(&model.SensitiveWord{Word: w, Level: firstNonEmptyStr(in.Level, "review"), Category: in.Category}).Error == nil {
				n++
			}
		}
		return map[string]any{"imported": n}, nil
	}))

	// ---------------- jobs ----------------
	r.Get("/jobs", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Job{})
		for p, col := range map[string]string{"status": "status", "type": "type", "studioId": "studio_id", "lane": "lane"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		return paginate[model.Job](r, q, "created_at desc")
	}))
	r.Post("/jobs/{id}/retry", h(func(r *http.Request) (any, error) { return nil, s.app.Q.Retry(param(r, "id")) }))
	r.Post("/jobs/{id}/cancel", h(func(r *http.Request) (any, error) { return nil, s.app.Q.Cancel(param(r, "id")) }))
	r.Post("/jobs/retry-failed", h(func(r *http.Request) (any, error) {
		var in struct{ Type string }
		bind(r, &in)
		var ids []string
		q := s.app.DB.Model(&model.Job{}).Where("status = ?", model.JobFailed)
		if in.Type != "" {
			q = q.Where("type = ?", in.Type)
		}
		q.Limit(200).Pluck("id", &ids)
		for _, id := range ids {
			s.app.Q.Retry(id)
		}
		s.app.Audit(userOf(r).ID, "jobs.retry_failed", "job", "", in.Type, clientIP(r), map[string]any{"count": len(ids)})
		return map[string]any{"retried": len(ids)}, nil
	}))

	// ---------------- operations ----------------
	crudAdmin[model.Announcement](s, r, "/announcements", adminCrudOpts{Roles: []string{model.AdminOperator}, Audit: "announcement", Before: func(item any) error {
		a := item.(*model.Announcement)
		if a.Audience.V == nil {
			a.Audience.V = map[string]any{}
		}
		if a.Kind == "" {
			a.Kind = "banner"
		}
		return nil
	}})
	crudAdmin[model.Article](s, r, "/articles", adminCrudOpts{Roles: []string{model.AdminOperator}, Filters: map[string]string{"kind": "kind"}, Search: []string{"title"}, Order: "kind asc, sort_order asc, created_at desc", Audit: "article",
		BeforeDelete: func(id string) error {
			var a model.Article
			if s.app.DB.Select("kind").First(&a, "id = ?", id).Error == nil && a.Kind == "legal" {
				return core.Bad("协议与政策不能删除，可以修改内容或取消发布")
			}
			return nil
		}})
	crudAdmin[model.Feedback](s, r, "/feedback", adminCrudOpts{Roles: []string{model.AdminOperator}, Filters: map[string]string{"status": "status", "kind": "kind"}, Search: []string{"content"}})
	r.Post("/feedback/{id}/reply", h(func(r *http.Request) (any, error) {
		var f model.Feedback
		if err := s.app.DB.First(&f, "id = ?", param(r, "id")).Error; err != nil {
			return nil, core.NotFound("反馈")
		}
		var in struct{ Reply string }
		bind(r, &in)
		s.app.DB.Model(&f).Updates(map[string]any{"reply": in.Reply, "status": "replied"})
		s.app.Notify(f.UserID, "", "feedback", "你的反馈有了回复", in.Reply, "/help")
		return nil, nil
	}))
	r.Post("/messages", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator); err != nil {
			return nil, err
		}
		var in struct {
			Title, Body, Link string
			PlanCodes         []string `json:"planCodes"`
			UserIDs           []string `json:"userIds"`
		}
		bind(r, &in)
		if strings.TrimSpace(in.Title) == "" {
			return nil, core.Bad("请填写标题")
		}
		var ids []string
		switch {
		case len(in.UserIDs) > 0:
			ids = in.UserIDs
		case len(in.PlanCodes) > 0:
			s.app.DB.Model(&model.StudioMember{}).Where("studio_id IN (?)", s.app.DB.Model(&model.Studio{}).Select("id").Where("plan_id IN (?)", s.app.DB.Model(&model.Plan{}).Select("id").Where("code IN ?", in.PlanCodes))).Distinct().Pluck("user_id", &ids)
		default:
			s.app.DB.Model(&model.User{}).Where("status = ?", model.UserActive).Pluck("id", &ids)
		}
		go s.app.NotifyMany(ids, "", "broadcast", in.Title, in.Body, in.Link)
		s.app.Audit(userOf(r).ID, "message.broadcast", "message", "", in.Title, clientIP(r), map[string]any{"recipients": len(ids)})
		return map[string]any{"recipients": len(ids)}, nil
	}))

	// ---------------- system ----------------
	settingKeys := map[string]any{"site": &core.SiteSettings{}, "smtp": &core.SMTPSettings{}, "ai_label": &core.AILabelSettings{}, "moderation": &core.ModerationSettings{}, "billing": &core.BillingSettings{}, "export_presets": &[]core.ExportPreset{}, "casting_policy": &map[string]model.CastingPolicy{}, "sms": &core.SMSSettings{}, "oauth": &core.OAuthSettings{}, "storage": &core.StorageSettings{}}
	r.Get("/settings/{key}", h(func(r *http.Request) (any, error) {
		key := param(r, "key")
		proto, okk := settingKeys[key]
		if !okk {
			return nil, core.NotFound("设置项")
		}
		b, _ := json.Marshal(proto)
		out := map[string]any{}
		_ = b
		var v any
		switch key {
		case "site":
			var x core.SiteSettings
			s.app.Setting(key, &x)
			v = x
		case "smtp":
			var x core.SMTPSettings
			s.app.Setting(key, &x)
			x.PassEnc = ""
			v = x
		case "ai_label":
			var x core.AILabelSettings
			s.app.Setting(key, &x)
			v = x
		case "moderation":
			var x core.ModerationSettings
			s.app.Setting(key, &x)
			v = x
		case "billing":
			var x core.BillingSettings
			s.app.Setting(key, &x)
			v = x
		case "export_presets":
			v = s.app.Presets()
		case "casting_policy":
			m := map[string]model.CastingPolicy{}
			s.app.Setting(key, &m)
			v = m
		case "sms":
			var x core.SMSSettings
			s.app.Setting(key, &x)
			x.SecretEnc = ""
			v = x
		case "storage":
			x := s.app.StorageSettings()
			hasSecret := x.OSS.SecretEnc != ""
			x.OSS.SecretEnc = ""
			out["hasSecret"] = hasSecret
			out["status"] = s.app.StorageStatus()
			v = x
		case "oauth":
			list := []map[string]any{}
			for _, p := range s.app.OAuthConfig() {
				hasSecret := p.SecretEnc != ""
				p.SecretEnc = ""
				var m map[string]any
				b, _ := json.Marshal(p)
				json.Unmarshal(b, &m)
				m["hasSecret"] = hasSecret
				m["callbackUrl"] = s.app.OAuthCallbackURL(p.ID)
				list = append(list, m)
			}
			v = map[string]any{"providers": list}
			out["presets"] = core.OAuthPresets
			out["callbackBase"] = s.app.Cfg.PublicURL + "/api/v1/auth/oauth/"
		}
		out["value"] = v
		return out, nil
	}))
	r.Put("/settings/{key}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		key := param(r, "key")
		if _, okk := settingKeys[key]; !okk {
			return nil, core.NotFound("设置项")
		}
		var raw json.RawMessage
		if err := bind(r, &raw); err != nil {
			return nil, err
		}
		var val any
		switch key {
		case "site":
			var x core.SiteSettings
			json.Unmarshal(raw, &x)
			val = x
		case "smtp":
			var x struct {
				core.SMTPSettings
				Password string `json:"password"`
			}
			json.Unmarshal(raw, &x)
			var cur core.SMTPSettings
			s.app.Setting("smtp", &cur)
			x.PassEnc = cur.PassEnc
			if x.Password != "" {
				x.PassEnc = s.app.Encrypt(x.Password)
			}
			val = x.SMTPSettings
		case "ai_label":
			var x core.AILabelSettings
			json.Unmarshal(raw, &x)
			val = x
		case "moderation":
			var x core.ModerationSettings
			json.Unmarshal(raw, &x)
			val = x
		case "billing":
			var x core.BillingSettings
			json.Unmarshal(raw, &x)
			val = x
		case "export_presets":
			var x []core.ExportPreset
			json.Unmarshal(raw, &x)
			val = x
		case "casting_policy":
			m := map[string]model.CastingPolicy{}
			json.Unmarshal(raw, &m)
			val = m
		case "sms":
			var x struct {
				core.SMSSettings
				Secret string `json:"secret"`
			}
			json.Unmarshal(raw, &x)
			var cur core.SMSSettings
			s.app.Setting("sms", &cur)
			x.SecretEnc = cur.SecretEnc
			if x.Secret != "" {
				x.SecretEnc = s.app.Encrypt(x.Secret)
			}
			val = x.SMSSettings
		case "storage":
			x, err := s.storageInput(raw)
			if err != nil {
				return nil, err
			}
			if x.Driver == "oss" {
				if steps, passed := s.app.CheckStorage(r.Context(), x, s.app.Decrypt(x.OSS.SecretEnc)); !passed {
					last := steps[len(steps)-1]
					return nil, core.Bad("OSS 连接测试没有通过（%s）：%s", last.Name, last.Message)
				}
			}
			val = x
		case "oauth":
			var x struct {
				Providers []struct {
					core.OAuthProvider
					Secret string `json:"secret"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return nil, core.Bad("设置格式不正确")
			}
			saved := map[string]string{}
			for _, p := range s.app.OAuthConfig() {
				saved[p.ID+"\x00"+p.ClientID] = p.SecretEnc
			}
			list := []core.OAuthProvider{}
			for _, p := range x.Providers {
				q := p.OAuthProvider
				q.ID, q.ClientID = strings.TrimSpace(q.ID), strings.TrimSpace(q.ClientID)
				q.SecretEnc = saved[q.ID+"\x00"+q.ClientID] // a blank secret keeps the saved one (same id and client)
				if sec := strings.TrimSpace(p.Secret); sec != "" {
					q.SecretEnc = s.app.Encrypt(sec)
				}
				list = append(list, q)
			}
			if err := s.app.ValidateOAuthProviders(list); err != nil {
				return nil, err
			}
			val = core.OAuthSettings{Providers: list}
		}
		if err := s.app.SetSetting(key, val); err != nil {
			return nil, err
		}
		if key == "storage" {
			s.app.ApplyStorage()
		}
		s.app.Audit(userOf(r).ID, "settings.update", "setting", key, "", clientIP(r), nil)
		return nil, nil
	}))
	r.Post("/settings/oauth/discover", h(func(r *http.Request) (any, error) {
		var in struct{ Issuer string }
		bind(r, &in)
		d, err := s.app.Discover(r.Context(), in.Issuer)
		if err != nil {
			return nil, core.Bad("%v", err)
		}
		return d, nil
	}))
	r.Post("/settings/storage/check", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := bind(r, &raw); err != nil {
			return nil, err
		}
		x, err := s.storageInput(raw)
		if err != nil {
			return nil, err
		}
		steps, passed := s.app.CheckStorage(r.Context(), x, s.app.Decrypt(x.OSS.SecretEnc))
		return map[string]any{"ok": passed, "steps": steps}, nil
	}))
	r.Post("/settings/storage/sync", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		if err := s.app.StartStorageSync(); err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "storage.sync", "setting", "storage", "", clientIP(r), nil)
		return s.app.StorageStatus(), nil
	}))
	r.Post("/settings/smtp/test", h(func(r *http.Request) (any, error) {
		var in struct{ To string }
		bind(r, &in)
		if err := s.app.SendMail(in.To, s.app.Site().Name+" 测试邮件", "<p>邮件服务配置成功 🎉</p>"); err != nil {
			return nil, core.Bad("发送失败：%v", err)
		}
		return nil, nil
	}))
	r.Get("/audit-logs", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.AuditLog{})
		for p, col := range map[string]string{"actorId": "actor_id", "targetType": "target_type", "targetId": "target_id"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		if a := r.URL.Query().Get("action"); a != "" {
			q = q.Where(dbx.Like(q, "action"), a+"%")
		}
		p, err := paginate[model.AuditLog](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		list := p.Items.([]model.AuditLog)
		var items []map[string]any
		names := map[string]string{}
		for _, l := range list {
			if _, okk := names[l.ActorID]; !okk {
				var u model.User
				s.app.DB.Select("nickname").First(&u, "id = ?", l.ActorID)
				names[l.ActorID] = u.Nickname
			}
			items = append(items, map[string]any{"log": l, "actor": names[l.ActorID]})
		}
		p.Items = items
		return p, nil
	}))
	r.Get("/system", h(func(r *http.Request) (any, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		var dbSize int64
		if s.app.Cfg.DBDriver == "postgres" {
			s.app.DB.Raw("SELECT pg_database_size(current_database())").Scan(&dbSize)
		} else if st, err := os.Stat(s.app.Cfg.DBDSN); err == nil {
			dbSize = st.Size()
		}
		var objects struct{ Bytes int64 }
		s.app.DB.Model(&model.StoredObject{}).Select("coalesce(sum(size), 0) as bytes").Scan(&objects)
		return map[string]any{
			"version": version.Version, "commit": version.Commit, "buildTime": version.BuildTime, "bus": s.app.Bus.Kind(), "go": runtime.Version(), "dbDriver": s.app.Cfg.DBDriver, "ffmpeg": s.app.FF.Available(), "online": s.app.Hub.Online(),
			"storageBytes": s.app.Store.DirSize("studios"), "objectStorageBytes": objects.Bytes, "dbBytes": dbSize, "goroutines": runtime.NumGoroutine(), "memoryMb": m.Alloc / (1 << 20),
			"devMode": s.app.Cfg.DevMode, "publicUrl": s.app.Cfg.PublicURL, "smtp": s.app.SMTPConfigured(), "startedAt": startedAt,
		}, nil
	}))
}

var startedAt = time.Now()

func (s *Server) regenSample(ctx context.Context, v *model.Voice) error {
	p, _, err := s.app.TTSFor(v)
	if err != nil {
		return err
	}
	txt := firstNonEmptyStr(v.SampleText, "你好，很高兴认识你。这是我的声音，希望能为你的故事增添色彩。")
	out, err := p.Synthesize(ctx, ttsReq(v, txt))
	if err != nil {
		return err
	}
	var pcm *audio.PCM
	if out.Format == "wav" {
		pcm, _ = audio.DecodeWAV(strings.NewReader(string(out.Data)))
	}
	ext := out.Format
	data := out.Data
	if pcm != nil {
		ext = "wav"
	}
	as, err := s.app.SaveAssetBytes(ctx, core.AssetInput{Scope: model.ScopeSystem, Kind: model.AssetPreview, Name: v.Name + " 试听", Ext: firstNonEmptyStr(ext, "mp3"), Origin: "system"}, data)
	if err != nil {
		return err
	}
	v.SampleAssetID = as.ID
	return s.app.DB.Model(v).Update("sample_asset_id", as.ID).Error
}

func itoa64(n int64) string {
	return strings.TrimSpace(strings.Replace(jsonNum(n), "\n", "", -1))
}

func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// storageInput reads storage settings from the admin form: a blank secret keeps the saved one.
func (s *Server) storageInput(raw json.RawMessage) (core.StorageSettings, error) {
	var x struct {
		core.StorageSettings
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &x); err != nil {
		return core.StorageSettings{}, core.Bad("设置格式不正确")
	}
	out := x.StorageSettings
	cur := s.app.StorageSettings()
	out.OSS.SecretEnc = cur.OSS.SecretEnc
	if sec := strings.TrimSpace(x.Secret); sec != "" {
		out.OSS.SecretEnc = s.app.Encrypt(sec)
	} else if strings.TrimSpace(out.OSS.AccessKeyID) != strings.TrimSpace(cur.OSS.AccessKeyID) {
		out.OSS.SecretEnc = "" // a different key needs its own secret
	}
	if out.Driver != "oss" {
		out.Driver = "local"
	}
	if out.ServeMode != "proxy" {
		out.ServeMode = "redirect"
	}
	out.KeepLocalDays = max(0, min(out.KeepLocalDays, 3650))
	if out.Driver == "oss" && out.OSS.SecretEnc == "" {
		return out, core.Bad("请填写 AccessKey Secret")
	}
	return out, nil
}
