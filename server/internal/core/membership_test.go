package core

import (
	"strings"
	"testing"
	"time"

	"voicebook/internal/config"
	"voicebook/internal/db/dbtest"
	"voicebook/internal/model"
)

// testApp returns an App on an in-memory database with the standard plans and one user.
func testApp(t *testing.T) (*App, *model.User) {
	t.Helper()
	db := dbtest.Open(t) // SQLite in memory, or PostgreSQL in CI
	a := New(&config.Config{Workers: 2}, db, nil)
	a.seedPlans()
	a.seedPlanDefaults()
	u := &model.User{Nickname: "测试"}
	db.Create(u)
	st, err := a.createStudioTx(db, u, "个人", "personal", a.defaultPlan(db))
	if err != nil {
		t.Fatal(err)
	}
	db.Model(u).Update("personal_studio", st.ID)
	u.PersonalStudio = st.ID
	return a, u
}

func plan(t *testing.T, a *App, code string) model.Plan {
	t.Helper()
	var p model.Plan
	if err := a.DB.First(&p, "code = ?", code).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// at moves the package clock and restores it after the test.
func at(t *testing.T, ts time.Time) {
	t.Helper()
	old := clock
	clock = func() time.Time { return ts }
	t.Cleanup(func() { clock = old })
}

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func TestMembershipStacking(t *testing.T) {
	a, u := testApp(t)
	t0 := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	at(t, t0)
	personal, team := plan(t, a, "personal"), plan(t, a, "team")

	if got := a.UserPlan(u.ID).Code; got != "free" {
		t.Fatalf("default plan = %s", got)
	}
	// 30 days of personal: takes effect now, studio follows, first month + bonus credits arrive
	r, err := a.GrantMembership(u.ID, personal.ID, 30, model.MemberSourceRedeem, "", "", "")
	if err != nil || r.Queued || r.Extended {
		t.Fatalf("grant personal: %+v %v", r, err)
	}
	if a.UserPlan(u.ID).Code != "personal" || a.StudioPlan(u.PersonalStudio).Code != "personal" {
		t.Fatal("personal not in effect for user/studio")
	}
	if bal := a.Balance(u.PersonalStudio).Total; bal != personal.MonthlyCredits+personal.BonusCredits {
		t.Fatalf("credits after activation = %d", bal)
	}
	// same plan again: extends, no extra credits
	r, _ = a.GrantMembership(u.ID, personal.ID, 30, model.MemberSourceRedeem, "", "", "")
	if !r.Extended || !r.ExpiresAt.Equal(t0.Add(day(60))) {
		t.Fatalf("extend: %+v", r)
	}
	if bal := a.Balance(u.PersonalStudio).Total; bal != personal.MonthlyCredits+personal.BonusCredits {
		t.Fatalf("extension must not grant credits again: %d", bal)
	}
	// 10 days later a higher plan for 7 days: personal is paused (pushed back by 7 days)
	at(t, t0.Add(day(10)))
	r, err = a.GrantMembership(u.ID, team.ID, 7, model.MemberSourceAdmin, "", "", "")
	if err != nil || r.Queued {
		t.Fatalf("grant team: %+v %v", r, err)
	}
	if a.UserPlan(u.ID).Code != "team" {
		t.Fatal("team should be in effect")
	}
	var p model.MembershipPeriod
	a.DB.First(&p, "user_id = ? AND plan_id = ?", u.ID, personal.ID)
	if !p.ExpiresAt.Equal(t0.Add(day(67))) {
		t.Fatalf("personal should be paused to day 67, ends %v", p.ExpiresAt)
	}
	// a lower plan granted meanwhile extends the paused personal period instead of being lost
	r, _ = a.GrantMembership(u.ID, personal.ID, 3, model.MemberSourceRedeem, "", "", "")
	if !r.Extended || !r.ExpiresAt.Equal(t0.Add(day(70))) {
		t.Fatalf("extend paused: %+v", r)
	}
	// team ends: back to personal without another activation bonus
	before := a.Balance(u.PersonalStudio).Total
	at(t, t0.Add(day(17)+time.Minute))
	a.MembershipTick()
	if a.UserPlan(u.ID).Code != "personal" {
		t.Fatalf("after team ends: %s", a.UserPlan(u.ID).Code)
	}
	if got := a.Balance(u.PersonalStudio).Total; got != before {
		t.Fatalf("resuming a paused period must not grant activation credits again (%d -> %d)", before, got)
	}
	// everything ends: free again, and the user is told
	at(t, t0.Add(day(70)+time.Minute))
	a.MembershipTick()
	if a.UserPlan(u.ID).Code != "free" || a.StudioPlan(u.PersonalStudio).Code != "free" {
		t.Fatal("should fall back to free")
	}
	var n int64
	a.DB.Model(&model.Notification{}).Where("user_id = ? AND title LIKE ?", u.ID, "%到期%").Count(&n)
	if n == 0 {
		t.Fatal("expiry notification missing")
	}
	a.DB.Model(&model.Notification{}).Where("user_id = ? AND title LIKE ?", u.ID, "会员已生效%").Count(&n)
	if n == 0 {
		t.Fatal("upgrade notification missing")
	}
}

func TestLowerPlanQueuesBehindHigher(t *testing.T) {
	a, u := testApp(t)
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	at(t, t0)
	a.GrantMembership(u.ID, plan(t, a, "team").ID, 10, model.MemberSourceAdmin, "", "", "")
	r, err := a.GrantMembership(u.ID, plan(t, a, "personal").ID, 30, model.MemberSourceRedeem, "", "", "")
	if err != nil || !r.Queued || !r.StartsAt.Equal(t0.Add(day(10))) || !r.ExpiresAt.Equal(t0.Add(day(40))) {
		t.Fatalf("queued: %+v %v", r, err)
	}
	if !strings.Contains(r.Summary, "到期后") {
		t.Fatalf("summary should explain queueing: %s", r.Summary)
	}
	at(t, t0.Add(day(10)+time.Second))
	a.MembershipTick()
	if a.UserPlan(u.ID).Code != "personal" {
		t.Fatal("queued plan should start when the higher one ends")
	}
}

func TestMonthlyCreditsAndCreditStudio(t *testing.T) {
	a, u := testApp(t)
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	at(t, t0)
	team := plan(t, a, "team")
	a.GrantMembership(u.ID, team.ID, 90, model.MemberSourceAdmin, "", "", "")
	st, err := a.CreateStudio(u, "剧组", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.StudioPlan(st.ID).Code != "team" {
		t.Fatal("new studio should follow the owner's plan")
	}
	if a.Balance(st.ID).Total != 0 {
		t.Fatal("creating a studio must not mint credits")
	}
	if err := a.SetCreditStudio(u.ID, st.ID); err != nil {
		t.Fatal(err)
	}
	at(t, t0.Add(day(30)+time.Minute))
	a.MembershipTick()
	a.MembershipTick() // the cycle is claimed: a second tick grants nothing
	if got := a.Balance(st.ID).Total; got != team.MonthlyCredits {
		t.Fatalf("second month credits in team studio = %d", got)
	}
}

func TestStudioLimitAndOverride(t *testing.T) {
	a, u := testApp(t)
	if _, err := a.CreateStudio(u, "第二个", ""); err == nil {
		t.Fatal("free plan allows only the personal studio")
	} else if e, ok := err.(*Error); !ok || e.Code != "plan_limit" {
		t.Fatalf("want plan_limit, got %v", err)
	}
	// an administrator pins a contract plan: the studio keeps it whatever the owner's membership
	ent := plan(t, a, "enterprise")
	a.DB.Model(&model.Studio{}).Where("id = ?", u.PersonalStudio).Update("plan_override_id", ent.ID)
	a.SyncStudioPlan(u.PersonalStudio)
	a.RefreshMembership(u.ID)
	if a.StudioPlan(u.PersonalStudio).Code != "enterprise" {
		t.Fatal("override should win")
	}
}

func TestDiscount(t *testing.T) {
	for _, c := range []struct {
		in   int64
		rate int
		want int64
	}{{100, 0, 100}, {100, 100, 100}, {100, 90, 90}, {7, 95, 7}, {1, 50, 1}, {1000, 85, 850}} {
		if got := discounted(c.in, c.rate); got != c.want {
			t.Errorf("discounted(%d, %d) = %d, want %d", c.in, c.rate, got, c.want)
		}
	}
}

func TestTrialGivesRightsNotCredits(t *testing.T) {
	a, u := testApp(t)
	personal := plan(t, a, "personal")
	if _, err := a.GrantMembership(u.ID, personal.ID, 7, model.MemberSourceTrial, "", "新用户试用", ""); err != nil {
		t.Fatal(err)
	}
	if a.UserPlan(u.ID).Code != "personal" {
		t.Fatal("trial plan should be in effect")
	}
	if bal := a.Balance(u.PersonalStudio).Total; bal != 0 {
		t.Fatalf("a trial must not grant plan credits, got %d", bal)
	}
	if a.MembershipRow(u.ID).EverPaid {
		t.Fatal("a trial must not count as having paid (new-user offers stay available)")
	}
	// refreshing again never double-grants
	a.RefreshMembership(u.ID)
	r, _ := a.GrantMembership(u.ID, plan(t, a, "team").ID, 30, model.MemberSourceRedeem, "", "", "")
	_ = r
	a.RefreshMembership(u.ID)
	team := plan(t, a, "team")
	if bal := a.Balance(u.PersonalStudio).Total; bal != team.MonthlyCredits+team.BonusCredits {
		t.Fatalf("activation credits delivered once: %d", bal)
	}
}
