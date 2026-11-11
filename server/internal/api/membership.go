package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

// ---------------- member side ----------------

func (s *Server) membershipRoutes(r chi.Router) {
	r.Get("/membership", h(func(r *http.Request) (any, error) {
		return s.membershipView(userOf(r), r.URL.Query().Get("studioId"))
	}))
	r.Patch("/membership", h(func(r *http.Request) (any, error) {
		var in struct {
			CreditStudioID string `json:"creditStudioId"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if err := s.app.SetCreditStudio(userOf(r).ID, in.CreditStudioID); err != nil {
			return nil, err
		}
		return s.membershipView(userOf(r), "")
	}))
	r.Post("/redeem/preview", h(func(r *http.Request) (any, error) {
		var in struct{ Code string }
		bind(r, &in)
		return s.app.PreviewCode(userOf(r).ID, in.Code)
	}))
	r.Post("/redeem", h(func(r *http.Request) (any, error) {
		var in struct {
			Code     string `json:"code"`
			StudioID string `json:"studioId"`
		}
		bind(r, &in)
		return s.app.Redeem(userOf(r), in.Code, in.StudioID, clientIP(r))
	}))
	r.Get("/redeem/history", h(func(r *http.Request) (any, error) {
		var uses []model.RedeemUse
		s.app.DB.Where("user_id = ?", userOf(r).ID).Order("created_at desc").Limit(50).Find(&uses)
		return uses, nil
	}))
	r.Post("/studios/{sid}/credits/transfer", h(func(r *http.Request) (any, error) {
		x, err := s.studio(r)
		if err != nil {
			return nil, err
		}
		var in struct {
			ToStudioID string `json:"toStudioId"`
			Amount     int64  `json:"amount"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return nil, s.app.TransferCredits(x, in.ToStudioID, in.Amount)
	}))
}

func (s *Server) membershipView(u *model.User, studioID string) (map[string]any, error) {
	m := s.app.MembershipRow(u.ID)
	plan := s.app.UserPlan(u.ID)
	def := s.app.DefaultPlanFor()
	var owned []model.Studio
	s.app.DB.Select("id", "name", "type").Where("owner_id = ? AND deleted_at IS NULL", u.ID).Order("created_at asc").Find(&owned)
	studios := make([]map[string]any, 0, len(owned))
	for _, st := range owned {
		studios = append(studios, map[string]any{"id": st.ID, "name": st.Name, "type": st.Type})
	}
	out := map[string]any{
		"plan": plan, "isDefault": plan.ID == def.ID, "defaultPlan": map[string]any{"id": def.ID, "name": def.Name},
		"expiresAt": m.ExpiresAt, "nextCreditAt": m.NextCreditAt, "creditStudioId": firstNonEmptyStr(m.CreditStudioID, u.PersonalStudio),
		"ownedStudios": studios, "periods": s.app.MembershipHistory(u.ID), "everPaid": m.EverPaid,
	}
	if studioID != "" {
		x, err := s.app.StudioAccess(u, studioID)
		if err != nil {
			return nil, err
		}
		var st model.Studio
		s.app.DB.Select("id", "name", "owner_id", "plan_override_id").First(&st, "id = ?", x.StudioID)
		sp := s.app.StudioPlan(x.StudioID)
		out["studio"] = map[string]any{"id": st.ID, "name": st.Name, "plan": sp, "own": st.OwnerID == u.ID, "contract": st.PlanOverrideID != ""}
		out["usage"] = s.app.UsageFor(u.ID, x.StudioID)
	} else {
		out["usage"] = s.app.UsageFor(u.ID, "")
	}
	return out, nil
}

// ---------------- admin side ----------------

func (s *Server) adminMembershipRoutes(r chi.Router) {
	billing := []string{model.AdminFinance, model.AdminOperator}

	// official background-music library (lives here to keep the admin router small)
	r.Get("/music-library", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminOps); err != nil {
			return nil, err
		}
		return s.app.MusicLibraryStatus(), nil
	}))
	r.Post("/music-library/import", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminOps); err != nil {
			return nil, err
		}
		j, err := s.app.StartMusicImport(userOf(r).ID)
		if err == nil {
			s.app.Audit(userOf(r).ID, "music.import", "job", j.ID, "", clientIP(r), nil)
		}
		return j, err
	}))

	// official sound-effect library
	r.Get("/sfx-library", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminOps); err != nil {
			return nil, err
		}
		return s.app.SfxLibraryStatus(), nil
	}))
	r.Post("/sfx-library/import", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOperator, model.AdminOps); err != nil {
			return nil, err
		}
		j, err := s.app.StartSfxImport(userOf(r).ID)
		if err == nil {
			s.app.Audit(userOf(r).ID, "sfx.import", "job", j.ID, "", clientIP(r), nil)
		}
		return j, err
	}))

	r.Get("/membership/overview", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		type row struct {
			PlanID string
			N      int64
		}
		var byPlan []row
		s.app.DB.Model(&model.UserMembership{}).Select("plan_id, COUNT(*) AS n").Group("plan_id").Scan(&byPlan)
		var plans []model.Plan
		s.app.DB.Order("level asc, sort_order asc").Find(&plans)
		counts := map[string]int64{}
		for _, b := range byPlan {
			counts[b.PlanID] = b.N
		}
		dist := []map[string]any{}
		for _, p := range plans {
			dist = append(dist, map[string]any{"planId": p.ID, "name": p.Name, "code": p.Code, "members": counts[p.ID]})
		}
		t := time.Now()
		var expiring, paid, trials, redeemed int64
		def := s.app.DefaultPlanFor()
		s.app.DB.Model(&model.UserMembership{}).Where("expires_at > ? AND expires_at < ?", t, t.Add(7*24*time.Hour)).Count(&expiring)
		s.app.DB.Model(&model.UserMembership{}).Where("plan_id <> ?", def.ID).Count(&paid)
		s.app.DB.Model(&model.UserMembership{}).Where("period_id IN (?)", s.app.DB.Model(&model.MembershipPeriod{}).Select("id").Where("source = ?", model.MemberSourceTrial)).Count(&trials)
		s.app.DB.Model(&model.RedeemUse{}).Where("created_at > ?", t.AddDate(0, 0, -30)).Count(&redeemed)
		var recent []model.RedeemUse
		s.app.DB.Order("created_at desc").Limit(10).Find(&recent)
		return map[string]any{"byPlan": dist, "paid": paid, "expiring7d": expiring, "trials": trials, "redeemed30d": redeemed, "recent": s.useViews(recent)}, nil
	}))

	r.Get("/users/{id}/membership", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var u model.User
		if s.app.DB.First(&u, "id = ?", param(r, "id")).Error != nil {
			return nil, core.NotFound("用户")
		}
		return s.membershipView(&u, "")
	}))
	r.Post("/users/{id}/membership", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var in struct {
			PlanID string `json:"planId"`
			Days   int    `json:"days"`
			Note   string `json:"note"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		var u model.User
		if s.app.DB.First(&u, "id = ?", param(r, "id")).Error != nil {
			return nil, core.NotFound("用户")
		}
		res, err := s.app.GrantMembership(u.ID, in.PlanID, in.Days, model.MemberSourceAdmin, "", firstNonEmptyStr(strings.TrimSpace(in.Note), "管理员开通"), userOf(r).ID)
		if err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "membership.grant", "user", u.ID, in.Note, clientIP(r), map[string]any{"plan": in.PlanID, "days": in.Days})
		s.app.Notify(u.ID, "", "membership", "会员已到账", res.Summary+"。", "/membership")
		return res, nil
	}))
	r.Delete("/membership-periods/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		if err := s.app.RevokePeriod(param(r, "id"), userOf(r).ID); err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "membership.revoke", "membership_period", param(r, "id"), "", clientIP(r), nil)
		return nil, nil
	}))

	// ---- redeem code batches ----
	r.Get("/redeem-batches", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.RedeemBatch{})
		for p, col := range map[string]string{"kind": "kind", "status": "status", "channel": "channel"} {
			if v := r.URL.Query().Get(p); v != "" {
				q = q.Where(col+" = ?", v)
			}
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where("("+dbx.Like(q, "name")+" OR "+dbx.Like(q, "channel")+" OR "+dbx.Like(q, "note")+")", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
		}
		pg, err := paginate[model.RedeemBatch](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		pg.Items = s.batchViews(pg.Items.([]model.RedeemBatch))
		return pg, nil
	}))
	r.Post("/redeem-batches", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var in core.BatchInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		b, err := s.app.CreateBatch(in, userOf(r).ID)
		if err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "redeem.batch.create", "redeem_batch", b.ID, b.Name, clientIP(r), map[string]any{"kind": b.Kind, "count": b.Count, "plan": b.PlanID, "days": b.Days, "credits": b.Credits})
		return s.batchViews([]model.RedeemBatch{*b})[0], nil
	}))
	r.Get("/redeem-batches/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var b model.RedeemBatch
		if s.app.DB.First(&b, "id = ?", param(r, "id")).Error != nil {
			return nil, core.NotFound("批次")
		}
		return s.batchViews([]model.RedeemBatch{b})[0], nil
	}))
	r.Patch("/redeem-batches/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var in struct {
			Name      *string    `json:"name"`
			Note      *string    `json:"note"`
			Channel   *string    `json:"channel"`
			Status    *string    `json:"status"`
			ExpiresAt *time.Time `json:"expiresAt"`
			ClearExp  bool       `json:"clearExpiresAt"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		up := map[string]any{}
		if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
			up["name"] = strings.TrimSpace(*in.Name)
		}
		if in.Note != nil {
			up["note"] = *in.Note
		}
		if in.Channel != nil {
			up["channel"] = strings.TrimSpace(*in.Channel)
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "disabled" {
				return nil, core.Bad("状态只能是 active 或 disabled")
			}
			up["status"] = *in.Status
		}
		if in.ExpiresAt != nil {
			up["expires_at"] = in.ExpiresAt
		} else if in.ClearExp {
			up["expires_at"] = nil
		}
		if len(up) == 0 {
			return nil, nil
		}
		if err := s.app.DB.Model(&model.RedeemBatch{}).Where("id = ?", param(r, "id")).Updates(up).Error; err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "redeem.batch.update", "redeem_batch", param(r, "id"), "", clientIP(r), up)
		return nil, nil
	}))
	r.Get("/redeem-batches/{id}/codes", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.RedeemCode{}).Where("batch_id = ?", param(r, "id"))
		if st := r.URL.Query().Get("status"); st != "" {
			q = q.Where("status = ?", st)
		}
		if kw := core.NormalizeCode(r.URL.Query().Get("q")); kw != "" {
			q = q.Where(dbx.Like(q, "key"), "%"+kw+"%")
		}
		pg, err := paginate[model.RedeemCode](r, q, "created_at asc, code asc")
		if err != nil {
			return nil, err
		}
		pg.Items = s.codeViews(pg.Items.([]model.RedeemCode))
		return pg, nil
	}))
	r.Get("/redeem-batches/{id}/uses", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		q := s.app.DB.Model(&model.RedeemUse{}).Where("batch_id = ?", param(r, "id"))
		pg, err := paginate[model.RedeemUse](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		pg.Items = s.useViews(pg.Items.([]model.RedeemUse))
		return pg, nil
	}))
	r.Get("/redeem-batches/{id}/export", func(w http.ResponseWriter, r *http.Request) {
		if err := requireRole(r, billing...); err != nil {
			fail(w, err)
			return
		}
		name, data, err := s.app.ExportBatchCSV(param(r, "id"), r.URL.Query().Get("status"))
		if err != nil {
			fail(w, err)
			return
		}
		s.app.Audit(userOf(r).ID, "redeem.batch.export", "redeem_batch", param(r, "id"), "", clientIP(r), map[string]any{"status": r.URL.Query().Get("status")})
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlEscape(name)))
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	})
	r.Post("/redeem-codes/{id}/status", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		var in struct{ Status string }
		bind(r, &in)
		var c model.RedeemCode
		if s.app.DB.First(&c, "id = ?", param(r, "id")).Error != nil {
			return nil, core.NotFound("兑换码")
		}
		switch {
		case in.Status == model.CodeDisabled && c.Status != model.CodeUsed:
		case in.Status == model.CodeUnused && c.Status == model.CodeDisabled:
			if c.Uses > 0 { // shared codes keep their use count; reactivating is fine
				in.Status = model.CodeUnused
			}
		default:
			return nil, core.Bad("已使用的兑换码不能修改状态")
		}
		s.app.DB.Model(&c).Update("status", in.Status)
		s.app.Audit(userOf(r).ID, "redeem.code.status", "redeem_code", c.ID, in.Status, clientIP(r), nil)
		return nil, nil
	}))
	r.Get("/redeem-codes/lookup", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, billing...); err != nil {
			return nil, err
		}
		key := core.NormalizeCode(r.URL.Query().Get("code"))
		var c model.RedeemCode
		if key == "" || s.app.DB.First(&c, "key = ?", key).Error != nil {
			return nil, core.NotFound("兑换码")
		}
		var b model.RedeemBatch
		s.app.DB.First(&b, "id = ?", c.BatchID)
		var uses []model.RedeemUse
		s.app.DB.Where("code_id = ?", c.ID).Order("created_at desc").Limit(20).Find(&uses)
		return map[string]any{"code": s.codeViews([]model.RedeemCode{c})[0], "batch": s.batchViews([]model.RedeemBatch{b})[0], "uses": s.useViews(uses)}, nil
	}))
}

func (s *Server) planNames() map[string]string {
	var plans []model.Plan
	s.app.DB.Select("id", "name").Find(&plans)
	m := map[string]string{}
	for _, p := range plans {
		m[p.ID] = p.Name
	}
	return m
}

func (s *Server) batchViews(bs []model.RedeemBatch) []map[string]any {
	names := s.planNames()
	out := make([]map[string]any, 0, len(bs))
	for _, b := range bs {
		type cnt struct {
			Status string
			N      int64
		}
		var counts []cnt
		s.app.DB.Model(&model.RedeemCode{}).Where("batch_id = ?", b.ID).Select("status, COUNT(*) AS n").Group("status").Scan(&counts)
		byStatus := map[string]int64{}
		for _, c := range counts {
			byStatus[c.Status] = c.N
		}
		shared := ""
		if b.Mode == model.RedeemModeShared {
			var c model.RedeemCode
			s.app.DB.Select("code").First(&c, "batch_id = ?", b.ID)
			shared = c.Code
		}
		state := b.Status
		if state == "active" && b.ExpiresAt != nil && b.ExpiresAt.Before(time.Now()) {
			state = "expired"
		}
		out = append(out, map[string]any{
			"id": b.ID, "createdAt": b.CreatedAt, "name": b.Name, "kind": b.Kind, "planId": b.PlanID, "planName": names[b.PlanID], "days": b.Days,
			"credits": b.Credits, "creditDays": b.CreditDays, "mode": b.Mode, "count": b.Count, "maxUses": b.MaxUses, "perUserLimit": b.PerUserLimit,
			"newUsersOnly": b.NewUsersOnly, "prefix": b.Prefix, "startsAt": b.StartsAt, "expiresAt": b.ExpiresAt, "channel": b.Channel, "note": b.Note,
			"status": b.Status, "state": state, "sharedCode": shared, "used": b.Used, "exported": b.Exported, "createdBy": s.userName(b.CreatedBy),
			"codes": map[string]int64{"unused": byStatus[model.CodeUnused], "used": byStatus[model.CodeUsed], "disabled": byStatus[model.CodeDisabled]},
		})
	}
	return out
}

func (s *Server) codeViews(cs []model.RedeemCode) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{"id": c.ID, "code": c.Code, "status": c.Status, "uses": c.Uses, "usedAt": c.UsedAt, "usedBy": c.UsedBy, "user": s.userName(c.UsedBy)})
	}
	return out
}

func (s *Server) useViews(us []model.RedeemUse) []map[string]any {
	out := make([]map[string]any, 0, len(us))
	for _, u := range us {
		var code model.RedeemCode
		s.app.DB.Select("code").First(&code, "id = ?", u.CodeID)
		out = append(out, map[string]any{"id": u.ID, "createdAt": u.CreatedAt, "userId": u.UserID, "user": s.userName(u.UserID), "code": code.Code, "kind": u.Kind, "summary": u.Summary, "credits": u.Credits, "days": u.Days, "batchId": u.BatchID})
	}
	return out
}

func (s *Server) userName(id string) string {
	if id == "" {
		return ""
	}
	var u model.User
	if s.app.DB.Select("nickname", "email", "phone").First(&u, "id = ?", id).Error != nil {
		return ""
	}
	if u.Nickname != "" {
		return u.Nickname
	}
	if u.Email != nil {
		return *u.Email
	}
	if u.Phone != nil {
		return *u.Phone
	}
	return id[:8]
}
