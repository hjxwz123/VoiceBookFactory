package core

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/model"
)

// ---------------------------------------------------------------------------
// Membership: plans belong to user accounts.
//
//   - A user holds zero or more MembershipPeriods. The plan in effect is the highest-level plan whose
//     period covers "now"; without one, the default plan (billing.defaultPlan) applies.
//   - Granting a plan the user already has extends that period. Granting a higher plan starts now and
//     pauses lower ones (their remaining time resumes afterwards). Granting a lower plan while a higher
//     one runs queues it to start when the higher one ends. So no purchased day is ever lost.
//   - Every studio the user owns follows the plan in effect, unless an administrator pinned the studio
//     to a contract plan (Studio.PlanOverrideID). Studio.PlanID always holds the resolved plan.
//   - Credits come with the plan: the plan's monthly credits (plus a one-off bonus when a period first
//     takes effect) are granted to the studio the user chose (default: the personal studio).
// ---------------------------------------------------------------------------

const membershipMonth = 30 * 24 * time.Hour

func (a *App) planByID(id string) (model.Plan, bool) {
	var p model.Plan
	if id == "" || a.DB.First(&p, "id = ?", id).Error != nil {
		return p, false
	}
	return p, true
}

// DefaultPlanFor returns the plan users fall back to without a membership.
func (a *App) DefaultPlanFor() model.Plan {
	if p := a.defaultPlan(a.DB); p != nil {
		return *p
	}
	return model.Plan{Name: "免费版", Code: "free"}
}

// effectivePeriod picks the period in effect at t: highest plan level, then the one ending last.
func effectivePeriod(periods []model.MembershipPeriod, level func(planID string) int, t time.Time) *model.MembershipPeriod {
	var best *model.MembershipPeriod
	for i := range periods {
		p := &periods[i]
		if p.Revoked || p.StartsAt.After(t) || (p.ExpiresAt != nil && !p.ExpiresAt.After(t)) {
			continue
		}
		if best == nil {
			best = p
			continue
		}
		lp, lb := level(p.PlanID), level(best.PlanID)
		if lp > lb || (lp == lb && laterEnd(p.ExpiresAt, best.ExpiresAt)) {
			best = p
		}
	}
	return best
}

// laterEnd reports whether a ends after b (nil = never ends).
func laterEnd(a, b *time.Time) bool {
	if a == nil {
		return b != nil
	}
	return b != nil && a.After(*b)
}

func (a *App) planLevels() func(string) int {
	var plans []model.Plan
	a.DB.Select("id", "level").Find(&plans)
	m := map[string]int{}
	for _, p := range plans {
		m[p.ID] = p.Level
	}
	return func(id string) int { return m[id] }
}

func (a *App) livePeriods(userID string) []model.MembershipPeriod {
	var ps []model.MembershipPeriod
	a.DB.Where("user_id = ? AND revoked = ? AND (expires_at IS NULL OR expires_at > ?)", userID, false, now()).Order("starts_at asc").Find(&ps)
	return ps
}

// MembershipRow returns the user's membership summary, creating it on first use.
func (a *App) MembershipRow(userID string) *model.UserMembership {
	var m model.UserMembership
	if a.DB.First(&m, "user_id = ?", userID).Error == nil {
		return &m
	}
	var u model.User
	a.DB.Select("id", "personal_studio").First(&u, "id = ?", userID)
	next := now().Add(membershipMonth)
	m = model.UserMembership{UserID: userID, PlanID: a.DefaultPlanFor().ID, CreditStudioID: u.PersonalStudio, NextCreditAt: &next}
	if err := a.DB.Create(&m).Error; err != nil { // lost a race: read the winner
		a.DB.First(&m, "user_id = ?", userID)
	}
	return &m
}

// UserPlan returns the plan in effect for a user.
func (a *App) UserPlan(userID string) model.Plan {
	m := a.MembershipRow(userID)
	if p, ok := a.planByID(m.PlanID); ok {
		return p
	}
	return a.DefaultPlanFor()
}

// StudioPlan returns the plan in effect for a studio.
func (a *App) StudioPlan(studioID string) model.Plan {
	var st model.Studio
	if a.DB.Select("id", "plan_id", "plan_override_id", "owner_id").First(&st, "id = ?", studioID).Error != nil {
		return a.DefaultPlanFor()
	}
	if p, ok := a.planByID(firstNonEmpty(st.PlanOverrideID, st.PlanID)); ok {
		return p
	}
	return a.UserPlan(st.OwnerID)
}

// GrantResult describes what a membership grant did, for user-facing messages.
type GrantResult struct {
	Plan      model.Plan `json:"plan"`
	Days      int        `json:"days"`
	StartsAt  time.Time  `json:"startsAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Extended  bool       `json:"extended"` // added to an existing period of the same plan
	Queued    bool       `json:"queued"`   // starts after a higher plan ends
	Summary   string     `json:"summary"`
}

// GrantMembership gives a user `days` of a plan (0 = permanent).
func (a *App) GrantMembership(userID, planID string, days int, source, refID, note, operatorID string) (*GrantResult, error) {
	plan, ok := a.planByID(planID)
	if !ok {
		return nil, Bad("会员套餐不存在")
	}
	if days < 0 {
		return nil, Bad("天数不能为负数")
	}
	a.MembershipRow(userID)
	level := a.planLevels()
	t := now()
	res := &GrantResult{Plan: plan, Days: days}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var live []model.MembershipPeriod
		tx.Where("user_id = ? AND revoked = ? AND (expires_at IS NULL OR expires_at > ?)", userID, false, t).Order("starts_at asc").Find(&live)
		dur := time.Duration(days) * 24 * time.Hour

		// 1. same plan: extend the latest-ending period
		var same *model.MembershipPeriod
		for i := range live {
			if live[i].PlanID == planID && (same == nil || laterEnd(live[i].ExpiresAt, same.ExpiresAt)) {
				same = &live[i]
			}
		}
		if same != nil {
			if same.ExpiresAt == nil {
				return Bad("你已是永久「%s」，无需重复开通", plan.Name)
			}
			up := map[string]any{"days": same.Days + days}
			if days == 0 {
				up["expires_at"] = nil
				res.ExpiresAt = nil
			} else {
				end := same.ExpiresAt.Add(dur)
				up["expires_at"] = end
				res.ExpiresAt = &end
			}
			res.Extended, res.StartsAt = true, same.StartsAt
			if same.StartsAt.After(t) {
				res.Queued = true
			}
			return tx.Model(same).Updates(up).Error
		}

		// 2. a higher (or equal-level, different) plan is running: queue after it
		start := t
		for _, p := range live {
			if p.PlanID == planID || level(p.PlanID) < plan.Level {
				continue
			}
			if p.ExpiresAt == nil {
				return Bad("你已拥有永久的更高等级会员，无需开通「%s」", plan.Name)
			}
			if p.ExpiresAt.After(start) {
				start = *p.ExpiresAt
			}
		}
		period := model.MembershipPeriod{UserID: userID, PlanID: planID, StartsAt: start, Days: days, Source: source, RefID: refID, Note: truncateStr(note, 250), OperatorID: operatorID}
		if days > 0 {
			end := start.Add(dur)
			period.ExpiresAt = &end
		}
		if err := tx.Create(&period).Error; err != nil {
			return err
		}
		res.StartsAt, res.ExpiresAt, res.Queued = start, period.ExpiresAt, start.After(t)

		// 3. it takes effect now: pause lower plans by pushing them back
		if !res.Queued {
			for _, p := range live {
				if level(p.PlanID) >= plan.Level {
					continue
				}
				up := map[string]any{}
				if days == 0 { // permanent higher plan: lower ones would never resume, leave them to run out
					continue
				}
				if p.StartsAt.After(t) {
					up["starts_at"] = p.StartsAt.Add(dur)
				}
				if p.ExpiresAt != nil {
					up["expires_at"] = p.ExpiresAt.Add(dur)
				}
				if len(up) > 0 {
					if err := tx.Model(&model.MembershipPeriod{}).Where("id = ?", p.ID).Updates(up).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Summary = grantSummary(res)
	a.RefreshMembership(userID)
	return res, nil
}

func grantSummary(r *GrantResult) string {
	length := fmt.Sprintf("%d 天", r.Days)
	if r.Days == 0 {
		length = "永久"
	}
	switch {
	case r.Queued:
		return fmt.Sprintf("「%s」%s，将在当前更高等级会员到期后（%s）自动生效", r.Plan.Name, length, r.StartsAt.Local().Format("2006-01-02"))
	case r.Extended && r.ExpiresAt == nil:
		return fmt.Sprintf("「%s」已升级为永久", r.Plan.Name)
	case r.Extended:
		return fmt.Sprintf("「%s」已续期 %s，有效期至 %s", r.Plan.Name, length, r.ExpiresAt.Local().Format("2006-01-02"))
	case r.ExpiresAt == nil:
		return fmt.Sprintf("已开通永久「%s」", r.Plan.Name)
	default:
		return fmt.Sprintf("已开通「%s」%s，有效期至 %s", r.Plan.Name, length, r.ExpiresAt.Local().Format("2006-01-02"))
	}
}

// RevokePeriod cancels a membership period (administrators only; does not refund other periods' shifts).
func (a *App) RevokePeriod(periodID, operatorID string) error {
	var p model.MembershipPeriod
	if a.DB.First(&p, "id = ?", periodID).Error != nil {
		return NotFound("会员记录")
	}
	if err := a.DB.Model(&p).Updates(map[string]any{"revoked": true, "operator_id": operatorID}).Error; err != nil {
		return err
	}
	a.RefreshMembership(p.UserID)
	return nil
}

// RefreshMembership re-evaluates the plan in effect, syncs the user's studios, delivers the first-month
// credits of a newly effective period and schedules the next boundary check.
func (a *App) RefreshMembership(userID string) {
	m := a.MembershipRow(userID)
	oldPlanID, oldPeriodID := m.PlanID, m.PeriodID // Updates below writes the new values into m
	periods := a.livePeriods(userID)
	level := a.planLevels()
	t := now()
	cur := effectivePeriod(periods, level, t)
	def := a.DefaultPlanFor()
	planID, periodID := def.ID, ""
	var expires *time.Time
	if cur != nil {
		planID, periodID, expires = cur.PlanID, cur.ID, cur.ExpiresAt
	}
	// next boundary: the earliest future start or end
	var next *time.Time
	for _, p := range periods {
		for _, b := range []*time.Time{&p.StartsAt, p.ExpiresAt} {
			if b != nil && b.After(t) && (next == nil || b.Before(*next)) {
				v := *b
				next = &v
			}
		}
	}
	up := map[string]any{"plan_id": planID, "period_id": periodID, "expires_at": expires, "next_check_at": next}
	changed := oldPlanID != planID || oldPeriodID != periodID
	if cur != nil && planID != def.ID && cur.Source != model.MemberSourceTrial { // a trial keeps "new user" offers available
		up["ever_paid"] = true
	}
	a.DB.Model(m).Updates(up)
	a.syncStudioPlans(userID)

	if !changed {
		return
	}
	plan, _ := a.planByID(planID)
	old, _ := a.planByID(oldPlanID)
	// a period taking effect for the first time brings its first month of credits and the bonus
	if cur != nil && periodID != "" {
		// claim the first activation atomically so concurrent refreshes cannot deliver the credits twice;
		// trials bring the plan's rights but not its credits (otherwise new accounts could farm them)
		res := a.DB.Model(&model.MembershipPeriod{}).Where("id = ? AND activated_at IS NULL", periodID).Update("activated_at", t)
		if res.RowsAffected > 0 && cur.Source != model.MemberSourceTrial {
			a.grantPlanCredits(userID, plan, true)
		}
		if old.ID != "" && old.ID != plan.ID {
			a.Notify(userID, "", "membership", "会员已生效："+plan.Name, membershipNotice(plan, expires), "/membership")
		}
		return
	}
	if old.ID != "" && old.ID != def.ID {
		a.Notify(userID, "", "membership", old.Name+"已到期", "已恢复为「"+def.Name+"」。已有的图书和音频都会保留；超出免费额度的部分暂时不能新增，续费后立即恢复。", "/membership")
	}
}

func membershipNotice(p model.Plan, exp *time.Time) string {
	s := fmt.Sprintf("每月 %d 积分", p.MonthlyCredits)
	if p.BonusCredits > 0 {
		s += fmt.Sprintf("，另赠 %d 积分", p.BonusCredits)
	}
	if exp != nil {
		s += "，有效期至 " + exp.Local().Format("2006-01-02")
	}
	return s + "。"
}

// grantPlanCredits delivers one month of plan credits (and the activation bonus) to the user's credit studio.
func (a *App) grantPlanCredits(userID string, p model.Plan, activation bool) {
	m := a.MembershipRow(userID)
	target := a.creditTarget(m)
	if target == "" {
		return
	}
	months := p.CreditMonths
	if months <= 0 {
		months = 2
	}
	exp := now().AddDate(0, months, 0)
	if p.MonthlyCredits > 0 {
		a.Grant(target, p.MonthlyCredits, "plan", &exp, p.Name+"月度积分", "")
	}
	if activation {
		if p.BonusCredits > 0 {
			a.Grant(target, p.BonusCredits, "plan", nil, p.Name+"开通赠送", "")
		}
		next := now().Add(membershipMonth)
		a.DB.Model(&model.UserMembership{}).Where("user_id = ?", userID).Update("next_credit_at", next)
	}
}

// creditTarget returns the studio receiving plan credits; falls back to the personal studio when the
// chosen one was deleted or is no longer owned by the user.
func (a *App) creditTarget(m *model.UserMembership) string {
	if m.CreditStudioID != "" {
		var n int64
		a.DB.Model(&model.Studio{}).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", m.CreditStudioID, m.UserID).Count(&n)
		if n > 0 {
			return m.CreditStudioID
		}
	}
	var u model.User
	a.DB.Select("personal_studio").First(&u, "id = ?", m.UserID)
	if u.PersonalStudio != "" && u.PersonalStudio != m.CreditStudioID {
		a.DB.Model(m).Update("credit_studio_id", u.PersonalStudio)
	}
	return u.PersonalStudio
}

// SetCreditStudio chooses which owned studio receives the monthly plan credits.
func (a *App) SetCreditStudio(userID, studioID string) error {
	var n int64
	a.DB.Model(&model.Studio{}).Where("id = ? AND owner_id = ? AND deleted_at IS NULL", studioID, userID).Count(&n)
	if n == 0 {
		return Bad("只能选择你拥有的工作室")
	}
	a.MembershipRow(userID)
	return a.DB.Model(&model.UserMembership{}).Where("user_id = ?", userID).Update("credit_studio_id", studioID).Error
}

// syncStudioPlans writes the resolved plan onto every studio the user owns.
func (a *App) syncStudioPlans(userID string) {
	var m model.UserMembership
	if a.DB.First(&m, "user_id = ?", userID).Error != nil {
		return
	}
	a.DB.Model(&model.Studio{}).Where("owner_id = ? AND (plan_override_id = '' OR plan_override_id IS NULL)", userID).Update("plan_id", m.PlanID)
}

// SyncStudioPlan resolves one studio's plan (after an ownership change or override edit).
func (a *App) SyncStudioPlan(studioID string) {
	var st model.Studio
	if a.DB.Select("id", "owner_id", "plan_override_id").First(&st, "id = ?", studioID).Error != nil {
		return
	}
	planID := st.PlanOverrideID
	if planID == "" {
		planID = a.MembershipRow(st.OwnerID).PlanID
	}
	a.DB.Model(&model.Studio{}).Where("id = ?", studioID).Update("plan_id", planID)
}

// MembershipTick runs every minute from the scheduler: period boundaries, monthly credits, reminders.
func (a *App) MembershipTick() {
	t := now()
	var due []model.UserMembership
	a.DB.Where("next_check_at IS NOT NULL AND next_check_at <= ?", t).Limit(500).Find(&due)
	for _, m := range due {
		a.RefreshMembership(m.UserID)
	}
	var credit []model.UserMembership
	a.DB.Where("next_credit_at IS NOT NULL AND next_credit_at <= ?", t).Limit(500).Find(&credit)
	for _, m := range credit {
		next := m.NextCreditAt.Add(membershipMonth)
		for !next.After(t) {
			next = next.Add(membershipMonth)
		}
		// claim the cycle first so a slow grant can never run twice
		res := a.DB.Model(&model.UserMembership{}).Where("id = ? AND next_credit_at = ?", m.ID, m.NextCreditAt).Update("next_credit_at", next)
		if res.RowsAffected == 0 {
			continue
		}
		var u model.User
		if a.DB.Select("status").First(&u, "id = ?", m.UserID).Error != nil || u.Status == model.UserDisabled {
			continue
		}
		a.grantPlanCredits(m.UserID, a.UserPlan(m.UserID), false)
	}
	a.remindExpiring()
}

// remindExpiring notifies members 7 days and 1 day before their plan ends (once each).
func (a *App) remindExpiring() {
	t := now()
	var rows []model.UserMembership
	a.DB.Where("expires_at IS NOT NULL AND expires_at > ? AND expires_at < ?", t, t.Add(7*24*time.Hour+time.Minute)).Limit(500).Find(&rows)
	for _, m := range rows {
		left := m.ExpiresAt.Sub(t)
		days := 7
		if left <= 24*time.Hour+time.Minute {
			days = 1
		}
		key := fmt.Sprintf("%s:%d", m.PeriodID, days)
		if m.Reminded == key {
			continue
		}
		// a queued period of the same plan continues seamlessly: nothing to remind
		var follow int64
		a.DB.Model(&model.MembershipPeriod{}).Where("user_id = ? AND plan_id = ? AND revoked = ? AND starts_at <= ? AND id <> ?", m.UserID, m.PlanID, false, *m.ExpiresAt, m.PeriodID).Count(&follow)
		a.DB.Model(&model.UserMembership{}).Where("id = ?", m.ID).Update("reminded", key)
		if follow > 0 {
			continue
		}
		p, _ := a.planByID(m.PlanID)
		a.Notify(m.UserID, "", "membership", fmt.Sprintf("「%s」将在 %d 天内到期", p.Name, days),
			"到期后会恢复为免费版：已有内容保留，但超出免费额度的部分不能新增。可在会员中心续费或使用兑换码。", "/membership")
	}
}

// ---------- limits ----------

// planLimit builds the error returned when a plan limit is reached; the client offers an upgrade.
func planLimit(kind string, limit, used int64, msg string, args ...any) *Error {
	return &Error{Status: 403, Code: "plan_limit", Msg: fmt.Sprintf(msg, args...), Data: map[string]any{"kind": kind, "limit": limit, "used": used}}
}

func (a *App) ownedStudios(userID string) int64 {
	var n int64
	a.DB.Model(&model.Studio{}).Where("owner_id = ? AND deleted_at IS NULL", userID).Count(&n)
	return n
}

// CheckStudioLimit is called before a user creates (or receives) a studio.
func (a *App) CheckStudioLimit(userID string) error {
	p := a.UserPlan(userID)
	if p.MaxStudios <= 0 {
		return nil
	}
	if n := a.ownedStudios(userID); n >= int64(p.MaxStudios) {
		return planLimit("studios", int64(p.MaxStudios), n, "「%s」最多可拥有 %d 个工作室（含个人工作室），升级会员可创建更多", p.Name, p.MaxStudios)
	}
	return nil
}

// Usage reports resource use against the plan of a studio and of its owner.
type Usage struct {
	Studios     [2]int64 `json:"studios"` // used, limit (0 = unlimited)
	Books       [2]int64 `json:"books"`
	Seats       [2]int64 `json:"seats"`
	Storage     [2]int64 `json:"storage"` // bytes
	CloneVoices [2]int64 `json:"cloneVoices"`
}

func (a *App) UsageFor(userID, studioID string) Usage {
	var u Usage
	up := a.UserPlan(userID)
	u.Studios = [2]int64{a.ownedStudios(userID), int64(up.MaxStudios)}
	if studioID == "" {
		return u
	}
	sp := a.StudioPlan(studioID)
	var books int64
	a.DB.Model(&model.Book{}).Where("studio_id = ? AND deleted_at IS NULL", studioID).Count(&books)
	clones := a.clonedVoices(studioID)
	var st model.Studio
	a.DB.Select("storage_used").First(&st, "id = ?", studioID)
	u.Books = [2]int64{books, int64(sp.MaxBooks)}
	u.Seats = [2]int64{int64(a.SeatsUsed(studioID)), int64(a.SeatLimit(studioID))}
	u.Storage = [2]int64{st.StorageUsed, a.StorageLimit(studioID)}
	u.CloneVoices = [2]int64{clones, int64(sp.CloneVoices)}
	return u
}

// PeriodView is a membership period for display.
type PeriodView struct {
	model.MembershipPeriod
	PlanName string `json:"planName"`
	PlanCode string `json:"planCode"`
	State    string `json:"state"` // active | paused | queued | ended | revoked
}

func (a *App) MembershipHistory(userID string) []PeriodView {
	var ps []model.MembershipPeriod
	a.DB.Where("user_id = ?", userID).Order("created_at desc").Limit(100).Find(&ps)
	m := a.MembershipRow(userID)
	t := now()
	names := map[string]model.Plan{}
	out := make([]PeriodView, 0, len(ps))
	for _, p := range ps {
		pl, ok := names[p.PlanID]
		if !ok {
			pl, _ = a.planByID(p.PlanID)
			names[p.PlanID] = pl
		}
		state := "ended"
		switch {
		case p.Revoked:
			state = "revoked"
		case p.ExpiresAt != nil && !p.ExpiresAt.After(t):
			state = "ended"
		case p.ID == m.PeriodID:
			state = "active"
		case p.StartsAt.After(t):
			state = "queued"
		default:
			state = "paused"
		}
		out = append(out, PeriodView{MembershipPeriod: p, PlanName: pl.Name, PlanCode: pl.Code, State: state})
	}
	sort.SliceStable(out, func(i, j int) bool { return stateOrder(out[i].State) < stateOrder(out[j].State) })
	return out
}

func stateOrder(s string) int {
	return map[string]int{"active": 0, "paused": 1, "queued": 2, "ended": 3, "revoked": 4}[s]
}

// ---------- migration from studio-level plans ----------

// migrateMemberships runs once: every user gets a membership row; studios an administrator put on a
// non-default plan keep it as a pinned contract plan; plain studios stop their own monthly grants.
func (a *App) migrateMemberships() {
	var done model.SystemSetting
	if a.DB.First(&done, "key = ?", "migration_membership_v1").Error == nil {
		return
	}
	def := a.DefaultPlanFor()
	var studios []model.Studio
	a.DB.Where("deleted_at IS NULL").Find(&studios)
	for _, st := range studios {
		if st.PlanID != "" && st.PlanID != def.ID && st.PlanOverrideID == "" {
			a.DB.Model(&model.Studio{}).Where("id = ?", st.ID).Update("plan_override_id", st.PlanID)
		} else if st.PlanOverrideID == "" {
			a.DB.Model(&model.Studio{}).Where("id = ?", st.ID).Update("plan_renew_at", nil)
		}
	}
	var users []model.User
	a.DB.Select("id", "personal_studio").Find(&users)
	for _, u := range users {
		m := a.MembershipRow(u.ID)
		var st model.Studio
		if u.PersonalStudio != "" && a.DB.Select("plan_renew_at").First(&st, "id = ?", u.PersonalStudio).Error == nil && st.PlanRenewAt != nil {
			a.DB.Model(m).Update("next_credit_at", *st.PlanRenewAt)
		}
		a.syncStudioPlans(u.ID)
	}
	a.migrateLegacyPromos()
	a.SetSetting("migration_membership_v1", map[string]any{"at": now()})
	slog.Info("membership migration done", "users", len(users), "studios", len(studios))
}

// SeedPlanDefaults fills the new plan fields on plans created before memberships existed.
func (a *App) seedPlanDefaults() {
	for _, col := range []string{"features", "benefits", "export_formats"} { // rows from before these columns existed
		a.DB.Model(&model.Plan{}).Where(col+" IS NULL").Update(col, model.Strings())
	}
	var plans []model.Plan
	a.DB.Find(&plans)
	defaults := map[string]model.Plan{
		"free":       {Level: 0, MaxStudios: 1, CreditMonths: 1, Benefits: model.Strings("每月 1 万积分", "3 本图书", "多角色 AI 配音", "MP3 导出"), ExportFormats: model.Strings("mp3")},
		"personal":   {Level: 10, MaxStudios: 2, PriceYearly: 69000, OriginalMonthly: 9900, BonusCredits: 50_000, CreditRate: 95, QueuePriority: 3, Badge: "最受欢迎", Highlight: true, Benefits: model.Strings("每月 15 万积分，开通再送 5 万", "图书数量不限", "精品音色与商用授权", "积分消耗 95 折", "智能对轨与审听")},
		"team":       {Level: 20, MaxStudios: 5, PriceYearly: 299000, BonusCredits: 200_000, CreditRate: 90, QueuePriority: 6, MaxUploadMB: 500, Benefits: model.Strings("每月 80 万积分，开通再送 20 万", "10 人协作、生产管理与计薪", "自带密钥（BYOK）", "分轨导出与多平台分发", "积分消耗 9 折")},
		"enterprise": {Level: 30, MaxStudios: 0, ContactSales: true, CreditRate: 85, QueuePriority: 10, MaxUploadMB: 2048, Benefits: model.Strings("按需定制积分与席位", "开放 API 与批量生产", "私有化部署", "专属客户成功经理")},
	}
	for _, p := range plans {
		d, ok := defaults[p.Code]
		if !ok || p.Level != 0 || len(p.Benefits.V) > 0 { // already configured
			continue
		}
		up := map[string]any{"level": d.Level, "max_studios": d.MaxStudios, "price_yearly": d.PriceYearly, "original_monthly": d.OriginalMonthly,
			"bonus_credits": d.BonusCredits, "credit_rate": d.CreditRate, "queue_priority": d.QueuePriority, "badge": d.Badge, "highlight": d.Highlight,
			"contact_sales": d.ContactSales, "credit_months": d.CreditMonths, "max_upload_mb": d.MaxUploadMB, "benefits": d.Benefits}
		if len(d.ExportFormats.V) > 0 {
			up["export_formats"] = d.ExportFormats
		}
		if p.Code != "free" {
			feats := append([]string{}, p.Features.V...)
			if p.Code == "team" || p.Code == "enterprise" {
				feats = appendUnique(feats, model.FeatStems)
			}
			up["features"] = model.Strings(feats...)
		}
		a.DB.Model(&model.Plan{}).Where("id = ?", p.ID).Updates(up)
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// checkPremiumVoice blocks premium-tier platform voices for plans without that benefit.
// (Trying a voice in the library stays free for everyone.)
func (a *App) checkPremiumVoice(studioID string, v *model.Voice) error {
	if v == nil || v.PriceTier != model.TierPremium || v.Scope != model.ScopeSystem {
		return nil
	}
	if p := a.StudioPlan(studioID); !p.HighQuality {
		return &Error{Status: 403, Code: "plan_required", Msg: "「" + v.Name + "」是精品音色，「" + p.Name + "」暂不包含，升级会员或换一个音色后再试", Data: map[string]any{"kind": "premiumVoice"}}
	}
	return nil
}
