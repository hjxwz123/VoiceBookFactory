package core

import (
	"bytes"
	"strings"
	"testing"

	"voicebook/internal/model"
)

func newUser(t *testing.T, a *App, name string) *model.User {
	t.Helper()
	u := &model.User{Nickname: name}
	a.DB.Create(u)
	st, err := a.createStudioTx(a.DB, u, name, "personal", a.defaultPlan(a.DB))
	if err != nil {
		t.Fatal(err)
	}
	a.DB.Model(u).Update("personal_studio", st.ID)
	u.PersonalStudio = st.ID
	return u
}

func errCode(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

func TestCodeFormat(t *testing.T) {
	code, key := newCode("VIP")
	if !strings.HasPrefix(code, "VIP-") || len(strings.Split(code, "-")) != 4 || key != NormalizeCode(code) {
		t.Fatalf("code %s key %s", code, key)
	}
	if strings.ContainsAny(strings.TrimPrefix(key, "VIP"), "01OIL") {
		t.Fatalf("ambiguous characters in %s", key)
	}
	if NormalizeCode(" vip-7kq2 mx9p_4tjd ") != "VIP7KQ2MX9P4TJD" {
		t.Fatal("normalization should ignore case, spaces and separators")
	}
}

func TestRedeemMembershipBatch(t *testing.T) {
	a, u := testApp(t)
	personal := plan(t, a, "personal")
	b, err := a.CreateBatch(BatchInput{Name: "双十一", Kind: model.RedeemMembership, PlanID: personal.ID, Days: 30, Mode: model.RedeemModeUnique, Count: 3, Prefix: "vip", Channel: "直播"}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	var codes []model.RedeemCode
	a.DB.Where("batch_id = ?", b.ID).Find(&codes)
	if len(codes) != 3 {
		t.Fatalf("generated %d codes", len(codes))
	}
	// preview accepts sloppy input and does not use the code
	o, err := a.PreviewCode(u.ID, strings.ToLower(strings.ReplaceAll(codes[0].Code, "-", " ")))
	if err != nil || o.PlanCode != "personal" || o.Days != 30 {
		t.Fatalf("preview: %+v %v", o, err)
	}
	res, err := a.Redeem(u, codes[0].Code, "", "1.2.3.4")
	if err != nil || res.Membership == nil || a.UserPlan(u.ID).Code != "personal" {
		t.Fatalf("redeem: %+v %v", res, err)
	}
	if _, err := a.Redeem(u, codes[0].Code, "", ""); errCode(err) != "code_used" {
		t.Fatalf("reuse should fail with code_used, got %v", err)
	}
	// one code per user in this batch
	if _, err := a.Redeem(u, codes[1].Code, "", ""); errCode(err) != "code_limit" {
		t.Fatalf("per-user limit: %v", err)
	}
	// another user can use the next code; the batch counts both
	v := newUser(t, a, "乙")
	if _, err := a.Redeem(v, codes[1].Code, "", ""); err != nil {
		t.Fatal(err)
	}
	a.DB.First(b, "id = ?", b.ID)
	if b.Used != 2 {
		t.Fatalf("batch used = %d", b.Used)
	}
	// export: BOM, header, one row per code with its state
	name, data, err := a.ExportBatchCSV(b.ID, "")
	if err != nil || !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) || !strings.Contains(name, "双十一") {
		t.Fatalf("export: %s %v", name, err)
	}
	csv := string(data)
	if strings.Count(csv, "\n") != 4 || strings.Count(csv, "已使用") != 2 || strings.Count(csv, "未使用") != 1 || !strings.Contains(csv, "「个人版」30 天") {
		t.Fatalf("csv:\n%s", csv)
	}
	// disabling the batch stops the remaining code
	a.DB.Model(b).Update("status", "disabled")
	w := newUser(t, a, "丙")
	if _, err := a.Redeem(w, codes[2].Code, "", ""); errCode(err) != "code_disabled" {
		t.Fatalf("disabled batch: %v", err)
	}
}

func TestRedeemSharedCreditsCode(t *testing.T) {
	a, u := testApp(t)
	b, err := a.CreateBatch(BatchInput{Name: "新人礼", Kind: model.RedeemCredits, Credits: 5000, CreditDays: 30, Mode: model.RedeemModeShared, Code: "welcome-2026", MaxUses: 2}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateBatch(BatchInput{Name: "重复", Kind: model.RedeemCredits, Credits: 1, Mode: model.RedeemModeShared, Code: "WELCOME2026"}, "admin"); err == nil {
		t.Fatal("duplicate shared code must be rejected")
	}
	res, err := a.Redeem(u, "WELCOME2026", "", "")
	if err != nil || res.Credits != 5000 || a.Balance(u.PersonalStudio).Total != 5000 {
		t.Fatalf("redeem credits: %+v %v", res, err)
	}
	if _, err := a.Redeem(u, "welcome2026", "", ""); errCode(err) != "code_limit" {
		t.Fatalf("same user twice: %v", err)
	}
	v := newUser(t, a, "乙")
	if _, err := a.Redeem(v, "WELCOME2026", "", ""); err != nil {
		t.Fatal(err)
	}
	w := newUser(t, a, "丙")
	if _, err := a.Redeem(w, "WELCOME2026", "", ""); errCode(err) != "code_exhausted" {
		t.Fatalf("max uses: %v", err)
	}
	// credits can only go to a studio the user manages
	other := newUser(t, a, "丁")
	b2, _ := a.CreateBatch(BatchInput{Name: "补偿", Kind: model.RedeemCredits, Credits: 100, Mode: model.RedeemModeUnique, Count: 1}, "admin")
	var c model.RedeemCode
	a.DB.First(&c, "batch_id = ?", b2.ID)
	if _, err := a.Redeem(other, c.Code, u.PersonalStudio, ""); err == nil {
		t.Fatal("redeeming credits into someone else's studio must fail")
	}
	a.DB.First(&c, "id = ?", c.ID)
	if c.Status != model.CodeUnused {
		t.Fatal("a failed redemption must leave the code unused")
	}
	_ = b
}

func TestNewUsersOnly(t *testing.T) {
	a, u := testApp(t)
	personal := plan(t, a, "personal")
	a.GrantMembership(u.ID, personal.ID, 7, model.MemberSourceAdmin, "", "", "")
	b, _ := a.CreateBatch(BatchInput{Name: "新客", Kind: model.RedeemMembership, PlanID: personal.ID, Days: 7, Mode: model.RedeemModeShared, Code: "NEWBIE", NewUsersOnly: true}, "admin")
	if _, err := a.Redeem(u, "NEWBIE", "", ""); errCode(err) != "code_new_only" {
		t.Fatalf("new-users-only: %v", err)
	}
	v := newUser(t, a, "新人")
	if _, err := a.Redeem(v, "NEWBIE", "", ""); err != nil {
		t.Fatal(err)
	}
	_ = b
}
