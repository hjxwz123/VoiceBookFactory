package core

import (
	"testing"
	"time"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

func register(a *App, email string, codes SignupCodes) (*model.User, error) {
	return a.Register(RegisterInput{Email: email, Password: "Passw0rd!", Nickname: email[:3], Agree: true, RegCode: codes.RegCode, Referral: codes.Referral, IP: "10.0.0.1"})
}

func balanceOf(a *App, u *model.User) int64 { return a.Balance(u.PersonalStudio).Available }

func TestLegacyInviteCodeBecomesRegistrationCode(t *testing.T) {
	a, _ := testApp(t)
	a.SetSetting("site", map[string]any{"registrationOpen": true, "inviteOnly": true, "inviteCode": "spring2026"})
	a.migrateRegistrationCode()
	s := a.Site()
	if !s.RegCodeRequired || s.InviteOnly || s.InviteCode != "" {
		t.Fatalf("site after migration: %+v", s)
	}
	if _, err := register(a, "aa1@example.com", SignupCodes{}); errCode(err) != ErrRegCodeRequired {
		t.Fatalf("sign-up without a code: %v", err)
	}
	if _, err := register(a, "aa2@example.com", SignupCodes{RegCode: "Spring-2026"}); err != nil {
		t.Fatalf("the old code keeps working (any case, separators ignored): %v", err)
	}
	a.migrateRegistrationCode() // idempotent
	var n int64
	a.DB.Model(&model.RegistrationCode{}).Count(&n)
	if n != 1 {
		t.Fatalf("want one code, got %d", n)
	}
}

func TestRegistrationCodes(t *testing.T) {
	a, _ := testApp(t)
	a.SetSetting("site", SiteSettings{RegistrationOpen: true, RegCodeRequired: true})
	codes, err := a.CreateRegCodes(RegCodeInput{Count: 3, MaxUses: 1, Prefix: "vb"}, "")
	if err != nil || len(codes) != 3 {
		t.Fatalf("create: %v %d", err, len(codes))
	}
	if _, err := register(a, "bb1@example.com", SignupCodes{RegCode: "nope-nope"}); errCode(err) != "registration_code_invalid" {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := register(a, "bb1@example.com", SignupCodes{RegCode: codes[0].Code}); err != nil {
		t.Fatal(err)
	}
	if _, err := register(a, "bb2@example.com", SignupCodes{RegCode: codes[0].Code}); errCode(err) != "registration_code_invalid" {
		t.Fatalf("a single-use code works once: %v", err)
	}
	// a failed sign-up (email taken) does not use up a code
	if _, err := register(a, "bb1@example.com", SignupCodes{RegCode: codes[1].Code}); err == nil {
		t.Fatal("duplicate email must fail")
	}
	var c model.RegistrationCode
	a.DB.First(&c, "id = ?", codes[1].ID)
	if c.Uses != 0 {
		t.Fatalf("code used by a failed sign-up: %d", c.Uses)
	}
	// disabled and expired codes are refused
	off := model.RegCodeDisabled
	a.UpdateRegCode(codes[1].ID, RegCodePatch{Status: &off})
	if _, err := register(a, "bb3@example.com", SignupCodes{RegCode: codes[1].Code}); err == nil {
		t.Fatal("disabled code accepted")
	}
	past := time.Now().Add(-time.Hour)
	a.UpdateRegCode(codes[2].ID, RegCodePatch{ExpiresAt: &past})
	if _, err := register(a, "bb4@example.com", SignupCodes{RegCode: codes[2].Code}); err == nil {
		t.Fatal("expired code accepted")
	}
	if err := a.DeleteRegCode(codes[0].ID); err == nil {
		t.Fatal("a used code cannot be deleted")
	}
	if err := a.DeleteRegCode(codes[2].ID); err != nil {
		t.Fatal(err)
	}
	var uses []model.RegistrationCodeUse
	a.DB.Find(&uses)
	if len(uses) != 1 || uses[0].CodeID != codes[0].ID {
		t.Fatalf("uses: %+v", uses)
	}
	// a shared code with a chosen name
	shared, err := a.CreateRegCodes(RegCodeInput{Code: "BETA-TEST", MaxUses: 0}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"bb5@example.com", "bb6@example.com"} {
		if _, err := register(a, e, SignupCodes{RegCode: "betatest"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.CreateRegCodes(RegCodeInput{Code: "beta test"}, ""); err == nil {
		t.Fatal("duplicate code accepted")
	}
	_ = shared

	// a new account through the phone: refused before the verification code is spent
	phone := "+8613800000001"
	a.DB.Create(&model.VerificationCode{Target: phone, Purpose: "login", CodeHash: secure.SHA256("123456"), Expires: time.Now().Add(time.Minute)})
	if _, _, err := a.LoginWithCode(phone, "123456", "studio", "", "", SignupCodes{}); errCode(err) != ErrRegCodeRequired {
		t.Fatalf("phone sign-up without a code: %v", err)
	}
	if _, _, err := a.LoginWithCode(phone, "123456", "studio", "", "", SignupCodes{RegCode: "BETATEST"}); err != nil {
		t.Fatalf("same verification code, now with a registration code: %v", err)
	}
}

func referralSetup(t *testing.T, rules ReferralSettings) (*App, *model.User, string) {
	a, _ := testApp(t)
	a.SetSetting("site", SiteSettings{RegistrationOpen: true, SignupCredits: 100})
	rules.Enabled = true
	if err := a.SaveReferral(rules); err != nil {
		t.Fatal(err)
	}
	inviter, err := register(a, "inviter@example.com", SignupCodes{})
	if err != nil {
		t.Fatal(err)
	}
	ic, err := a.InviteCodeOf(inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a, inviter, ic.Code
}

func TestReferralSignupRewardsAndCommission(t *testing.T) {
	a, inviter, code := referralSetup(t, ReferralSettings{InviterCredits: 50, InviteeCredits: 30, InviterAfter: "signup", DailyLimit: 2, CommissionRate: 10, CreditsPerYuan: 100, CommissionOn: "all"})
	start := balanceOf(a, inviter)
	b, err := register(a, "friend1@example.com", SignupCodes{Referral: code})
	if err != nil {
		t.Fatal(err)
	}
	if got := balanceOf(a, b); got != 130 {
		t.Fatalf("invitee gets sign-up credits plus the invitee reward: %d", got)
	}
	if got := balanceOf(a, inviter) - start; got != 50 {
		t.Fatalf("inviter reward: %d", got)
	}
	// the daily limit: the third invitee today brings no sign-up reward, but still counts as invited
	register(a, "friend2@example.com", SignupCodes{Referral: code})
	register(a, "friend3@example.com", SignupCodes{Referral: code})
	if got := balanceOf(a, inviter) - start; got != 100 {
		t.Fatalf("daily limit of 2: %d", got)
	}
	var refs int64
	a.DB.Model(&model.Referral{}).Where("inviter_id = ?", inviter.ID).Count(&refs)
	if refs != 3 {
		t.Fatalf("referrals: %d", refs)
	}
	// 10% of ¥68 at 100 credits per yuan = 680 credits, on every payment
	before := balanceOf(a, inviter)
	if _, err := a.RecordPayment(PaymentInput{UserID: b.ID, StudioID: b.PersonalStudio, Amount: 6800, Source: model.PaymentAdmin}); err != nil {
		t.Fatal(err)
	}
	a.RecordPayment(PaymentInput{UserID: b.ID, StudioID: b.PersonalStudio, Amount: 1000, Source: model.PaymentAdmin})
	if got := balanceOf(a, inviter) - before; got != 680+100 {
		t.Fatalf("commission: %d", got)
	}
	var ref model.Referral
	a.DB.First(&ref, "invitee_id = ?", b.ID)
	if ref.Paid != 7800 || ref.Payments != 2 || ref.Earned != 50+780 {
		t.Fatalf("referral totals: %+v", ref)
	}
	// a payment by someone nobody invited pays no commission
	if _, err := a.RecordPayment(PaymentInput{UserID: inviter.ID, Amount: 5000, Source: model.PaymentAdmin}); err != nil {
		t.Fatal(err)
	}
	// what the inviter sees hides the friends' names and amounts
	v, _ := a.MyReferral(inviter)
	if st := v["stats"].(map[string]any); st["signups"].(int64) != 3 || st["paying"].(int64) != 1 || st["earned"].(int64) != 50+50+780 {
		t.Fatalf("stats: %+v", st)
	}
	list, _ := a.MyInvitees(inviter.ID, 10, 0)
	for _, it := range list["items"].([]map[string]any) {
		if n := it["name"].(string); n == "fri" {
			t.Fatalf("names are masked: %q", n)
		}
	}
	// an administrator stops this inviter's code: no new sign-ups with it, no more commission
	a.DB.Model(&model.InviteCode{}).Where("user_id = ?", inviter.ID).Update("disabled", true)
	if _, err := register(a, "friend4@example.com", SignupCodes{Referral: code}); errCode(err) != "invite_code_invalid" {
		t.Fatalf("disabled code: %v", err)
	}
	before = balanceOf(a, inviter)
	a.RecordPayment(PaymentInput{UserID: b.ID, Amount: 10000, Source: model.PaymentAdmin})
	if balanceOf(a, inviter) != before {
		t.Fatal("no commission after the code was disabled")
	}
}

func TestReferralRewardAfterFirstPayment(t *testing.T) {
	a, inviter, code := referralSetup(t, ReferralSettings{InviterCredits: 50, InviterAfter: "payment", CommissionRate: 5, CreditsPerYuan: 10, CommissionOn: "first"})
	start := balanceOf(a, inviter)
	b, err := register(a, "friend@example.com", SignupCodes{Referral: code})
	if err != nil {
		t.Fatal(err)
	}
	if balanceOf(a, inviter) != start {
		t.Fatal("the reward waits for the first payment")
	}
	a.RecordPayment(PaymentInput{UserID: b.ID, Amount: 10000, Source: model.PaymentRedeem}) // ¥100 × 5% × 10 = 50
	a.RecordPayment(PaymentInput{UserID: b.ID, Amount: 10000, Source: model.PaymentRedeem}) // first payment only
	if got := balanceOf(a, inviter) - start; got != 50+50 {
		t.Fatalf("after payments: %d", got)
	}
	var ref model.Referral
	a.DB.First(&ref, "invitee_id = ?", b.ID)
	if ref.Pending || !ref.SignupPaid {
		t.Fatalf("referral: %+v", ref)
	}
}

func TestReferralCodesAndRegistrationCodeBypass(t *testing.T) {
	a, inviter, code := referralSetup(t, ReferralSettings{InviteeCredits: 10})
	a.SetSetting("site", SiteSettings{RegistrationOpen: true, RegCodeRequired: true})
	if _, err := register(a, "f1@example.com", SignupCodes{Referral: code}); errCode(err) != ErrRegCodeRequired {
		t.Fatalf("an invite link alone is not enough by default: %v", err)
	}
	r := a.Referral()
	r.BypassRegCode = true
	a.SaveReferral(r)
	if _, err := register(a, "f1@example.com", SignupCodes{Referral: code}); err != nil {
		t.Fatalf("with bypass on: %v", err)
	}
	if _, err := register(a, "f2@example.com", SignupCodes{Referral: "WRONG1"}); errCode(err) != "invite_code_invalid" {
		t.Fatalf("unknown invite code: %v", err)
	}
	// choosing one's own code
	if _, err := a.RenameInviteCode(inviter.ID, "ab"); err == nil {
		t.Fatal("too short")
	}
	if _, err := a.RenameInviteCode(inviter.ID, "书友"); err == nil {
		t.Fatal("letters and digits only")
	}
	ic, err := a.RenameInviteCode(inviter.ID, "reader88")
	if err != nil || ic.Code != "READER88" {
		t.Fatalf("rename: %v %+v", err, ic)
	}
	other, _ := register(a, "f3@example.com", SignupCodes{Referral: "reader88"})
	if other == nil {
		t.Fatal("the new code works")
	}
	if _, err := a.RenameInviteCode(other.ID, "READER88"); err == nil {
		t.Fatal("taken")
	}
	// the program switched off: invite codes are ignored, sign-up still works with a registration code
	r.Enabled = false
	a.SaveReferral(r)
	codes, _ := a.CreateRegCodes(RegCodeInput{Count: 1, MaxUses: 1}, "")
	u, err := register(a, "f4@example.com", SignupCodes{Referral: "reader88", RegCode: codes[0].Code})
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	a.DB.Model(&model.Referral{}).Where("invitee_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatal("no referral while the program is off")
	}
	if err := a.SaveReferral(ReferralSettings{CommissionRate: 120}); err == nil {
		t.Fatal("rate over 100%")
	}
	if err := a.SaveReferral(ReferralSettings{CommissionRate: 10}); err == nil {
		t.Fatal("a rate needs credits per yuan")
	}
}

func TestPricedRedeemCodeIsAPayment(t *testing.T) {
	a, inviter, code := referralSetup(t, ReferralSettings{CommissionRate: 20, CreditsPerYuan: 100, CommissionOn: "all"})
	b, _ := register(a, "buyer@example.com", SignupCodes{Referral: code})
	batch, err := a.CreateBatch(BatchInput{Name: "淘宝 500 积分", Kind: model.RedeemCredits, Credits: 500, Mode: model.RedeemModeUnique, Count: 1, PriceCents: 990}, "")
	if err != nil {
		t.Fatal(err)
	}
	var c model.RedeemCode
	a.DB.First(&c, "batch_id = ?", batch.ID)
	before := balanceOf(a, inviter)
	if _, err := a.Redeem(b, c.Code, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := balanceOf(a, inviter) - before; got != 198 { // ¥9.90 × 20% × 100
		t.Fatalf("commission on a priced code: %d", got)
	}
	var p model.Payment
	if a.DB.First(&p, "user_id = ?", b.ID).Error != nil || p.Amount != 990 || p.Source != model.PaymentRedeem {
		t.Fatalf("payment: %+v", p)
	}
}
