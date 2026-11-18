package api

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

func nowUTC() time.Time { return time.Now().UTC() }

// yuanToFen turns an amount typed in yuan into fen.
func yuanToFen(y float64) int64 { return int64(math.Round(y * 100)) }

// growthRoutes: a user's invite code, link and rewards.
func (s *Server) growthRoutes(r chi.Router) {
	r.Get("/referral", h(func(r *http.Request) (any, error) {
		return s.app.MyReferral(userOf(r))
	}))
	r.Put("/referral/code", h(func(r *http.Request) (any, error) {
		var in struct {
			Code string `json:"code"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if _, err := s.app.RenameInviteCode(userOf(r).ID, in.Code); err != nil {
			return nil, err
		}
		return s.app.MyReferral(userOf(r))
	}))
	r.Get("/referral/invitees", h(func(r *http.Request) (any, error) {
		size := min(max(qint(r, "pageSize", 20), 1), 100)
		return s.app.MyInvitees(userOf(r).ID, size, (max(qint(r, "page", 1), 1)-1)*size)
	}))
	r.Get("/referral/rewards", h(func(r *http.Request) (any, error) {
		size := min(max(qint(r, "pageSize", 20), 1), 100)
		return s.app.MyReferralRewards(userOf(r).ID, size, (max(qint(r, "page", 1), 1)-1)*size)
	}))
}

// publicGrowthRoutes: what someone who opened an invite link sees before signing up.
func (s *Server) publicGrowthRoutes(r chi.Router) {
	r.Get("/invite-codes/{code}", h(func(r *http.Request) (any, error) {
		return s.app.PublicInvite(param(r, "code"), clientIP(r)), nil
	}))
}

type userBrief struct {
	ID       string  `json:"id"`
	Nickname string  `json:"nickname"`
	Email    *string `json:"email"`
	Phone    *string `json:"phone"`
}

func (s *Server) briefs(ids []string) map[string]userBrief {
	out := map[string]userBrief{}
	if len(ids) == 0 {
		return out
	}
	var us []model.User
	s.app.DB.Select("id", "nickname", "email", "phone").Where("id IN ?", ids).Find(&us)
	for _, u := range us {
		out[u.ID] = userBrief{ID: u.ID, Nickname: u.Nickname, Email: u.Email, Phone: u.Phone}
	}
	return out
}

// matchingUsers is a subquery of the users whose email, phone, nickname or id matches kw.
func (s *Server) matchingUsers(kw string) any {
	q := s.app.DB.Model(&model.User{})
	return q.Select("id").Where(dbx.Like(q, "email")+" OR "+dbx.Like(q, "phone")+" OR "+dbx.Like(q, "nickname")+" OR id = ?", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%", kw)
}

func (s *Server) adminGrowthRoutes(r chi.Router) {
	// ---------- registration codes ----------
	r.Get("/registration-codes", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.RegistrationCode{})
		if st := r.URL.Query().Get("status"); st != "" {
			switch st {
			case "usable":
				q = q.Where("status = ? AND (max_uses = 0 OR uses < max_uses) AND (expires_at IS NULL OR expires_at > ?)", model.RegCodeActive, nowUTC())
			case "used":
				q = q.Where("uses > 0")
			default:
				q = q.Where("status = ?", st)
			}
		}
		if b := r.URL.Query().Get("batch"); b != "" {
			q = q.Where("batch = ?", b)
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where("key = ? OR "+dbx.Like(q, "note"), core.NormalizeCode(kw), "%"+kw+"%")
		}
		return paginate[model.RegistrationCode](r, q, "created_at desc, code asc")
	}))
	r.Post("/registration-codes", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		var in core.RegCodeInput
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		codes, err := s.app.CreateRegCodes(in, userOf(r).ID)
		if err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "regcode.create", "registration_code", codes[0].Batch, in.Note, clientIP(r), map[string]any{"count": len(codes), "maxUses": in.MaxUses})
		return map[string]any{"codes": codes, "batch": codes[0].Batch}, nil
	}))
	r.Patch("/registration-codes/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		var in core.RegCodePatch
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		c, err := s.app.UpdateRegCode(param(r, "id"), in)
		if err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "regcode.update", "registration_code", c.ID, "", clientIP(r), map[string]any{"status": in.Status, "maxUses": in.MaxUses})
		return c, nil
	}))
	r.Delete("/registration-codes/{id}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		if err := s.app.DeleteRegCode(param(r, "id")); err != nil {
			return nil, err
		}
		s.app.Audit(userOf(r).ID, "regcode.delete", "registration_code", param(r, "id"), "", clientIP(r), nil)
		return nil, nil
	}))
	r.Get("/registration-codes/{id}/uses", h(func(r *http.Request) (any, error) {
		var uses []model.RegistrationCodeUse
		s.app.DB.Where("code_id = ?", param(r, "id")).Order("created_at desc").Limit(500).Find(&uses)
		ids := make([]string, 0, len(uses))
		for _, u := range uses {
			ids = append(ids, u.UserID)
		}
		users := s.briefs(ids)
		out := make([]map[string]any, 0, len(uses))
		for _, u := range uses {
			out = append(out, map[string]any{"user": users[u.UserID], "createdAt": u.CreatedAt, "ip": u.IP})
		}
		return out, nil
	}))

	// ---------- the invite program ----------
	r.Get("/referrals/summary", h(func(r *http.Request) (any, error) {
		db := s.app.DB
		var total, month, paying int64
		db.Model(&model.Referral{}).Count(&total)
		db.Model(&model.Referral{}).Where("created_at > ?", nowUTC().AddDate(0, 0, -30)).Count(&month)
		db.Model(&model.Referral{}).Where("payments > 0").Count(&paying)
		var paid int64
		db.Model(&model.Referral{}).Select("COALESCE(SUM(paid),0)").Scan(&paid)
		rewards := map[string]int64{}
		for _, k := range []string{model.RewardSignupInviter, model.RewardSignupInvitee, model.RewardCommission} {
			var v int64
			db.Model(&model.ReferralReward{}).Where("kind = ?", k).Select("COALESCE(SUM(credits),0)").Scan(&v)
			rewards[k] = v
		}
		var top []struct {
			InviterID string
			Signups   int64
			Paying    int64
			Earned    int64
		}
		db.Model(&model.Referral{}).Select("inviter_id, COUNT(*) AS signups, SUM(CASE WHEN payments > 0 THEN 1 ELSE 0 END) AS paying, COALESCE(SUM(earned),0) AS earned").
			Group("inviter_id").Order("signups desc").Limit(10).Scan(&top)
		ids := make([]string, 0, len(top))
		for _, t := range top {
			ids = append(ids, t.InviterID)
		}
		users := s.briefs(ids)
		codes := map[string]model.InviteCode{}
		var ics []model.InviteCode
		db.Where("user_id IN ?", ids).Find(&ics)
		for _, c := range ics {
			codes[c.UserID] = c
		}
		topOut := make([]map[string]any, 0, len(top))
		for _, t := range top {
			c := codes[t.InviterID]
			topOut = append(topOut, map[string]any{"user": users[t.InviterID], "signups": t.Signups, "paying": t.Paying, "earned": t.Earned, "code": c.Code, "visits": c.Visits, "disabled": c.Disabled})
		}
		return map[string]any{"total": total, "last30": month, "paying": paying, "paidCents": paid, "rewards": rewards, "top": topOut}, nil
	}))
	r.Get("/referrals", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Referral{})
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			sub := s.matchingUsers(kw)
			q = q.Where("inviter_id IN (?) OR invitee_id IN (?) OR code = ?", sub, sub, core.NormalizeCode(kw))
		}
		if id := r.URL.Query().Get("inviterId"); id != "" {
			q = q.Where("inviter_id = ?", id)
		}
		p, err := paginate[model.Referral](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		refs := p.Items.([]model.Referral)
		ids := []string{}
		for _, x := range refs {
			ids = append(ids, x.InviterID, x.InviteeID)
		}
		users := s.briefs(ids)
		disabled := map[string]bool{}
		var ics []model.InviteCode
		s.app.DB.Where("user_id IN ? AND disabled = ?", ids, true).Find(&ics)
		for _, c := range ics {
			disabled[c.UserID] = true
		}
		items := make([]map[string]any, 0, len(refs))
		for _, x := range refs {
			items = append(items, map[string]any{"id": x.ID, "inviter": users[x.InviterID], "invitee": users[x.InviteeID], "code": x.Code, "ip": x.IP, "createdAt": x.CreatedAt,
				"signupPaid": x.SignupPaid, "pending": x.Pending, "paidCents": x.Paid, "payments": x.Payments, "earned": x.Earned, "inviterDisabled": disabled[x.InviterID]})
		}
		p.Items = items
		return p, nil
	}))
	r.Patch("/invite-codes/{userId}", h(func(r *http.Request) (any, error) {
		if err := requireRole(r, model.AdminOps, model.AdminOperator); err != nil {
			return nil, err
		}
		var in struct {
			Disabled bool   `json:"disabled"`
			Reason   string `json:"reason"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		ic, err := s.app.InviteCodeOf(param(r, "userId"))
		if err != nil {
			return nil, err
		}
		s.app.DB.Model(ic).Update("disabled", in.Disabled)
		s.app.Audit(userOf(r).ID, "invitecode.update", "user", ic.UserID, in.Reason, clientIP(r), map[string]any{"disabled": in.Disabled})
		return map[string]any{"disabled": in.Disabled}, nil
	}))
	r.Get("/payments", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Payment{})
		if id := r.URL.Query().Get("userId"); id != "" {
			q = q.Where("user_id = ?", id)
		}
		if kw := strings.TrimSpace(r.URL.Query().Get("q")); kw != "" {
			q = q.Where("user_id IN (?)", s.matchingUsers(kw))
		}
		if src := r.URL.Query().Get("source"); src != "" {
			q = q.Where("source = ?", src)
		}
		p, err := paginate[model.Payment](r, q, "created_at desc")
		if err != nil {
			return nil, err
		}
		ps := p.Items.([]model.Payment)
		ids := []string{}
		for _, x := range ps {
			ids = append(ids, x.UserID, x.OperatorID)
		}
		users := s.briefs(ids)
		// the commission each payment earned
		pids := make([]string, 0, len(ps))
		for _, x := range ps {
			pids = append(pids, x.ID)
		}
		earned := map[string]int64{}
		var rw []model.ReferralReward
		s.app.DB.Where("payment_id IN ?", pids).Find(&rw)
		for _, x := range rw {
			earned[x.PaymentID] += x.Credits
		}
		items := make([]map[string]any, 0, len(ps))
		for _, x := range ps {
			items = append(items, map[string]any{"id": x.ID, "user": users[x.UserID], "amountCents": x.Amount, "source": x.Source, "summary": x.Summary, "operator": users[x.OperatorID].Nickname, "createdAt": x.CreatedAt, "commission": earned[x.ID]})
		}
		p.Items = items
		return p, nil
	}))
}
