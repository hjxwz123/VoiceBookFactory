package core

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"voicebook/internal/model"
)

// ---------------------------------------------------------------------------
// Registration codes (an access control set by administrators) and the invite program (a marketing
// feature: users invite friends and earn credits). Every way of creating an account — email sign-up,
// first sign-in with a phone number, a third-party account — goes through admit and admitted.
// ---------------------------------------------------------------------------

// SignupCodes are the codes a new account comes with.
type SignupCodes struct {
	RegCode  string `json:"regCode"`  // registration code, when the site requires one
	Referral string `json:"referral"` // a friend's invite code
}

// ErrRegCodeRequired is the error code of a sign-up refused for lack of a registration code; the sign-in
// page shows the registration code field when it gets it.
const ErrRegCodeRequired = "registration_code_required"

type admission struct {
	reg     *model.RegistrationCode
	claimed bool
	inviter *model.InviteCode
}

// invitationValid reports whether token is a studio invitation that can still be used. Someone invited to
// a studio may sign up without a registration code.
func (a *App) invitationValid(token string) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	_, _, _, err := a.InvitationInfo(token)
	return err == nil
}

// admit decides whether a new account may be created and, with claim, takes one use of its registration
// code: call it so right before creating the account, and release when that fails. Without claim it only
// checks, so a sign-up can be refused before a verification code is spent.
func (a *App) admit(c SignupCodes, invited, claim bool) (*admission, error) {
	site := a.Site()
	if !site.RegistrationOpen {
		return nil, Forbidden("平台暂未开放注册")
	}
	adm := &admission{}
	rules := a.Referral()
	if ref := NormalizeCode(c.Referral); ref != "" && rules.Enabled {
		ic, err := a.inviteCodeByKey(ref)
		if err != nil {
			return nil, err
		}
		adm.inviter = ic
	}
	if site.RegCodeRequired && !invited && !(adm.inviter != nil && rules.BypassRegCode) {
		key := NormalizeCode(c.RegCode)
		if key == "" {
			return nil, &Error{Status: 403, Code: ErrRegCodeRequired, Msg: "现在需要注册码才能注册，请填写注册码"}
		}
		reg, err := a.claimRegCode(key, claim)
		if err != nil {
			return nil, err
		}
		adm.reg, adm.claimed = reg, claim
	}
	return adm, nil
}

// release gives back a registration code claimed for an account that was not created.
func (a *App) release(adm *admission) {
	if adm != nil && adm.reg != nil && adm.claimed {
		a.DB.Model(&model.RegistrationCode{}).Where("id = ? AND uses > 0", adm.reg.ID).Update("uses", gorm.Expr("uses - 1"))
	}
}

// admitted records how a new account got in: the registration code it used and who invited it.
func (a *App) admitted(u *model.User, adm *admission, ip string) {
	if adm == nil {
		return
	}
	if adm.reg != nil {
		a.DB.Create(&model.RegistrationCodeUse{CodeID: adm.reg.ID, UserID: u.ID, IP: truncateStr(ip, 60)})
	}
	if adm.inviter != nil {
		a.startReferral(u, adm.inviter, ip)
	}
}

// ---------- registration codes ----------

const maxRegCodes = 1000

func (a *App) claimRegCode(key string, claim bool) (*model.RegistrationCode, error) {
	invalid := &Error{Status: 400, Code: "registration_code_invalid", Msg: "注册码不正确或已失效，请检查后重试"}
	var c model.RegistrationCode
	if a.DB.First(&c, "key = ?", key).Error != nil || c.Status != model.RegCodeActive {
		return nil, invalid
	}
	if c.ExpiresAt != nil && c.ExpiresAt.Before(now()) {
		return nil, &Error{Status: 400, Code: "registration_code_invalid", Msg: "注册码已过期"}
	}
	if !claim {
		if c.MaxUses > 0 && c.Uses >= c.MaxUses {
			return nil, &Error{Status: 400, Code: "registration_code_invalid", Msg: "注册码已被用完"}
		}
		return &c, nil
	}
	t := now()
	q := a.DB.Model(&model.RegistrationCode{}).Where("id = ? AND status = ?", c.ID, model.RegCodeActive)
	if c.MaxUses > 0 {
		q = q.Where("uses < ?", c.MaxUses)
	}
	if res := q.Updates(map[string]any{"uses": gorm.Expr("uses + 1"), "last_used_at": &t}); res.Error != nil || res.RowsAffected == 0 {
		return nil, &Error{Status: 400, Code: "registration_code_invalid", Msg: "注册码已被用完"}
	}
	return &c, nil
}

// RegCodeInput is what an administrator fills in to create registration codes.
type RegCodeInput struct {
	Count       int    `json:"count"`       // how many codes to generate
	Code        string `json:"code"`        // or one code of their own choosing
	MaxUses     int    `json:"maxUses"`     // uses per code, 0 = unlimited
	ExpiresDays int    `json:"expiresDays"` // 0 = never expires
	Prefix      string `json:"prefix"`
	Note        string `json:"note"`
}

// CreateRegCodes generates registration codes.
func (a *App) CreateRegCodes(in RegCodeInput, operatorID string) ([]model.RegistrationCode, error) {
	if in.MaxUses < 0 || in.MaxUses > 1_000_000 {
		return nil, Bad("每个注册码的可用次数应在 0~100 万之间，0 表示不限")
	}
	if in.ExpiresDays < 0 || in.ExpiresDays > 3650 {
		return nil, Bad("有效天数应在 0~3650 之间，0 表示长期有效")
	}
	var exp *time.Time
	if in.ExpiresDays > 0 {
		t := now().AddDate(0, 0, in.ExpiresDays)
		exp = &t
	}
	batch := model.NewID()
	mk := func(code, key string) model.RegistrationCode {
		return model.RegistrationCode{Code: code, Key: key, MaxUses: in.MaxUses, ExpiresAt: exp, Note: truncateStr(strings.TrimSpace(in.Note), 250), Batch: batch, Status: model.RegCodeActive, CreatedBy: operatorID}
	}
	var codes []model.RegistrationCode
	if custom := strings.TrimSpace(in.Code); custom != "" {
		key := NormalizeCode(custom)
		if len(key) < 4 || len(key) > 32 {
			return nil, Bad("注册码需要 4~32 个字母或数字")
		}
		var n int64
		a.DB.Model(&model.RegistrationCode{}).Where("key = ?", key).Count(&n)
		if n > 0 {
			return nil, Conflict(fmt.Sprintf("注册码「%s」已经存在", custom))
		}
		codes = append(codes, mk(truncateStr(strings.ToUpper(custom), 40), key))
	} else {
		if in.Count < 1 || in.Count > maxRegCodes {
			return nil, Bad("一次可以生成 1~%d 个注册码", maxRegCodes)
		}
		prefix := NormalizeCode(in.Prefix)
		if len(prefix) > 6 {
			return nil, Bad("前缀最多 6 个字母或数字")
		}
		seen := map[string]bool{}
		for len(codes) < in.Count {
			code, key := newCode(prefix)
			if seen[key] {
				continue
			}
			seen[key] = true
			codes = append(codes, mk(code, key))
		}
	}
	if err := a.DB.CreateInBatches(&codes, 200).Error; err != nil {
		return nil, err
	}
	return codes, nil
}

// RegCodePatch changes a registration code.
type RegCodePatch struct {
	Status    *string    `json:"status"`
	Note      *string    `json:"note"`
	MaxUses   *int       `json:"maxUses"`
	ExpiresAt *time.Time `json:"expiresAt"`
	NoExpiry  bool       `json:"noExpiry"`
}

func (a *App) UpdateRegCode(id string, p RegCodePatch) (*model.RegistrationCode, error) {
	var c model.RegistrationCode
	if a.DB.First(&c, "id = ?", id).Error != nil {
		return nil, NotFound("注册码")
	}
	up := map[string]any{}
	if p.Status != nil {
		if *p.Status != model.RegCodeActive && *p.Status != model.RegCodeDisabled {
			return nil, Bad("状态只能是启用或停用")
		}
		up["status"] = *p.Status
	}
	if p.Note != nil {
		up["note"] = truncateStr(strings.TrimSpace(*p.Note), 250)
	}
	if p.MaxUses != nil {
		if *p.MaxUses < 0 {
			return nil, Bad("可用次数不能为负数")
		}
		up["max_uses"] = *p.MaxUses
	}
	if p.NoExpiry {
		up["expires_at"] = nil
	} else if p.ExpiresAt != nil {
		up["expires_at"] = p.ExpiresAt
	}
	if len(up) > 0 {
		if err := a.DB.Model(&c).Updates(up).Error; err != nil {
			return nil, err
		}
	}
	a.DB.First(&c, "id = ?", id)
	return &c, nil
}

// DeleteRegCode removes a code nobody used; a used code is kept (it shows who signed up with it).
func (a *App) DeleteRegCode(id string) error {
	var c model.RegistrationCode
	if a.DB.First(&c, "id = ?", id).Error != nil {
		return NotFound("注册码")
	}
	if c.Uses > 0 {
		return Conflict("这个注册码已经有人用过，不能删除，可以改为停用")
	}
	return a.DB.Delete(&c).Error
}

// adoptLegacyInvite turns the single invite code of earlier versions into a registration code.
func (a *App) adoptLegacyInvite(s *SiteSettings) {
	if s.InviteOnly {
		s.RegCodeRequired = true
	}
	if code := strings.TrimSpace(s.InviteCode); code != "" {
		key := NormalizeCode(code)
		var n int64
		a.DB.Model(&model.RegistrationCode{}).Where("key = ?", key).Count(&n)
		if n == 0 && key != "" {
			a.DB.Create(&model.RegistrationCode{Code: truncateStr(code, 40), Key: key, Note: "由旧版「邀请码」转来", Batch: model.NewID(), Status: model.RegCodeActive})
		}
	}
	s.InviteOnly, s.InviteCode = false, ""
}

// migrateRegistrationCode runs at start-up: before 0.1.5 the site had one shared "invite code".
func (a *App) migrateRegistrationCode() {
	s := a.Site()
	if !s.InviteOnly && s.InviteCode == "" {
		return
	}
	a.adoptLegacyInvite(&s)
	if err := a.SetSetting("site", s); err != nil {
		slog.Warn("migrate invite code", "err", err)
	}
}

// SaveSite stores the site settings (an older admin page may still send the legacy invite code).
func (a *App) SaveSite(s SiteSettings) error {
	a.adoptLegacyInvite(&s)
	return a.SetSetting("site", s)
}

// ---------- the invite program ----------

// SaveReferral validates and stores the rules of the invite program.
func (a *App) SaveReferral(s ReferralSettings) error {
	switch {
	case s.InviterCredits < 0 || s.InviteeCredits < 0 || s.InviterCredits > 10_000_000 || s.InviteeCredits > 10_000_000:
		return Bad("奖励积分应在 0~1000 万之间")
	case s.CommissionRate < 0 || s.CommissionRate > 100:
		return Bad("返利比例应在 0~100%% 之间")
	case s.CreditsPerYuan < 0 || s.CreditsPerYuan > 1_000_000:
		return Bad("「1 元折合积分」应在 0~100 万之间")
	case s.CommissionRate > 0 && s.CreditsPerYuan == 0:
		return Bad("设置了返利比例时，请填写 1 元折合多少积分")
	case s.CreditDays < 0 || s.DailyLimit < 0 || s.TotalLimit < 0 || s.CommissionDays < 0:
		return Bad("天数和次数不能为负数")
	}
	if s.InviterAfter != "payment" {
		s.InviterAfter = "signup"
	}
	if s.CommissionOn != "first" {
		s.CommissionOn = "all"
	}
	s.PosterTitle = truncateStr(strings.TrimSpace(s.PosterTitle), 24)
	s.PosterText = truncateStr(strings.TrimSpace(s.PosterText), 60)
	return a.SetSetting("referral", s)
}

func (a *App) inviteCodeByKey(key string) (*model.InviteCode, error) {
	bad := &Error{Status: 400, Code: "invite_code_invalid", Msg: "邀请码不存在或已停用，请检查；也可以清空邀请码直接注册"}
	var ic model.InviteCode
	if a.DB.First(&ic, "key = ?", key).Error != nil || ic.Disabled {
		return nil, bad
	}
	var u model.User
	if a.DB.Select("id", "status").First(&u, "id = ?", ic.UserID).Error != nil || u.Status != model.UserActive {
		return nil, bad
	}
	return &ic, nil
}

// inviteAlphabet: letters and digits that read unambiguously.
const inviteAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func randomInviteKey(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(inviteAlphabet))))
		b.WriteByte(inviteAlphabet[k.Int64()])
	}
	return b.String()
}

// InviteCodeOf returns the user's invite code, creating it on first use.
func (a *App) InviteCodeOf(userID string) (*model.InviteCode, error) {
	var ic model.InviteCode
	if a.DB.First(&ic, "user_id = ?", userID).Error == nil {
		return &ic, nil
	}
	for i := 0; i < 8; i++ {
		key := randomInviteKey(6 + i/3)
		ic = model.InviteCode{UserID: userID, Code: key, Key: key}
		if err := a.DB.Create(&ic).Error; err == nil {
			return &ic, nil
		}
		if a.DB.First(&ic, "user_id = ?", userID).Error == nil { // created meanwhile by another request
			return &ic, nil
		}
	}
	return nil, fmt.Errorf("could not allocate an invite code")
}

const maxInviteRenames = 3

// RenameInviteCode lets a user choose their own invite code. Links with the old code stop working.
func (a *App) RenameInviteCode(userID, code string) (*model.InviteCode, error) {
	if !a.Referral().Enabled {
		return nil, Forbidden("邀请活动暂未开放")
	}
	ic, err := a.InviteCodeOf(userID)
	if err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	key := NormalizeCode(code)
	if key != strings.ToUpper(code) || len(key) < 4 || len(key) > 16 {
		return nil, Bad("邀请码需要 4~16 个英文字母或数字")
	}
	if key == ic.Key {
		return ic, nil
	}
	if ic.Renamed >= maxInviteRenames {
		return nil, Bad("邀请码最多修改 %d 次", maxInviteRenames)
	}
	if block, review := a.ScanSensitive(key); len(block)+len(review) > 0 {
		return nil, Bad("这个邀请码不能使用，请换一个")
	}
	var n int64
	a.DB.Model(&model.InviteCode{}).Where("key = ? AND id <> ?", key, ic.ID).Count(&n)
	if n > 0 {
		return nil, Conflict(fmt.Sprintf("邀请码「%s」已被占用，换一个试试", key))
	}
	if err := a.DB.Model(ic).Updates(map[string]any{"code": key, "key": key, "renamed": gorm.Expr("renamed + 1")}).Error; err != nil {
		return nil, Conflict(fmt.Sprintf("邀请码「%s」已被占用，换一个试试", key))
	}
	a.DB.First(ic, "id = ?", ic.ID)
	return ic, nil
}

// InviteLink is where an invite code takes people.
func (a *App) InviteLink(code string) string { return a.Cfg.PublicURL + "/r/" + code }

// PublicInvite describes an invite code to someone who opened the link, and counts the visit.
func (a *App) PublicInvite(code, ip string) map[string]any {
	rules := a.Referral()
	key := NormalizeCode(code)
	if !rules.Enabled || key == "" {
		return map[string]any{"valid": false}
	}
	ic, err := a.inviteCodeByKey(key)
	if err != nil {
		return map[string]any{"valid": false}
	}
	if a.Allow("invite-visit:"+key+":"+ip, 1) {
		a.DB.Model(&model.InviteCode{}).Where("id = ?", ic.ID).Update("visits", gorm.Expr("visits + 1"))
	}
	var u model.User
	a.DB.Select("nickname", "avatar_url").First(&u, "id = ?", ic.UserID)
	return map[string]any{"valid": true, "code": ic.Code, "inviter": u.Nickname, "avatarUrl": u.AvatarURL, "inviteeCredits": rules.InviteeCredits, "signupCredits": a.Site().SignupCredits, "bypassRegCode": rules.BypassRegCode}
}

func (a *App) rewardExpiry(rules ReferralSettings) *time.Time {
	if rules.CreditDays <= 0 {
		return nil
	}
	t := now().AddDate(0, 0, rules.CreditDays)
	return &t
}

// startReferral links a new account to its inviter and grants the sign-up rewards.
func (a *App) startReferral(invitee *model.User, ic *model.InviteCode, ip string) {
	if ic.UserID == invitee.ID {
		return
	}
	rules := a.Referral()
	ref := model.Referral{InviterID: ic.UserID, InviteeID: invitee.ID, Code: ic.Code, IP: truncateStr(ip, 60)}
	if err := a.DB.Create(&ref).Error; err != nil {
		return // already invited by someone
	}
	if rules.InviteeCredits > 0 {
		if err := a.Grant(invitee.PersonalStudio, rules.InviteeCredits, "referral", a.rewardExpiry(rules), "好友邀请注册奖励", ""); err == nil {
			a.DB.Create(&model.ReferralReward{ReferralID: ref.ID, UserID: invitee.ID, OtherID: ic.UserID, StudioID: invitee.PersonalStudio, Kind: model.RewardSignupInvitee, Credits: rules.InviteeCredits, Note: "通过好友邀请注册"})
		}
	}
	if rules.InviterCredits > 0 && rules.InviterAfter == "payment" {
		a.DB.Model(&ref).Update("pending", true)
		a.Notify(ic.UserID, "", "referral", "好友「"+invitee.Nickname+"」接受了你的邀请", fmt.Sprintf("好友完成首次充值后，你将获得 %d 积分。", rules.InviterCredits), "/referral")
		return
	}
	got := a.inviterSignupReward(&ref, invitee, rules)
	body := "感谢你的推荐。"
	if got > 0 {
		body = fmt.Sprintf("你获得了 %d 积分邀请奖励。", got)
	}
	if rules.CommissionRate > 0 && rules.CreditsPerYuan > 0 {
		body += "好友以后充值，你还能获得返利。"
	}
	a.Notify(ic.UserID, "", "referral", "好友「"+invitee.Nickname+"」通过你的邀请注册了", body, "/referral")
}

// inviterSignupReward grants the inviter's reward for one invitee, within the daily and total limits.
// It returns the credits granted (0 when a limit was reached or nothing is configured).
func (a *App) inviterSignupReward(ref *model.Referral, invitee *model.User, rules ReferralSettings) int64 {
	if rules.InviterCredits <= 0 {
		return 0
	}
	var inviter model.User
	if a.DB.Select("id", "personal_studio", "status").First(&inviter, "id = ?", ref.InviterID).Error != nil || inviter.Status != model.UserActive || inviter.PersonalStudio == "" {
		return 0
	}
	unlock := lockBook("referral:" + inviter.ID) // serialize per inviter so limits hold under concurrent sign-ups
	defer unlock()
	count := func(since *time.Time) int64 {
		var n int64
		q := a.DB.Model(&model.ReferralReward{}).Where("user_id = ? AND kind = ?", inviter.ID, model.RewardSignupInviter)
		if since != nil {
			q = q.Where("created_at > ?", *since)
		}
		q.Count(&n)
		return n
	}
	day := now().Add(-24 * time.Hour)
	if (rules.DailyLimit > 0 && count(&day) >= int64(rules.DailyLimit)) || (rules.TotalLimit > 0 && count(nil) >= int64(rules.TotalLimit)) {
		a.DB.Model(ref).Updates(map[string]any{"pending": false})
		return 0
	}
	if err := a.Grant(inviter.PersonalStudio, rules.InviterCredits, "referral", a.rewardExpiry(rules), "邀请好友「"+invitee.Nickname+"」注册奖励", ""); err != nil {
		return 0
	}
	a.DB.Create(&model.ReferralReward{ReferralID: ref.ID, UserID: inviter.ID, OtherID: invitee.ID, StudioID: inviter.PersonalStudio, Kind: model.RewardSignupInviter, Credits: rules.InviterCredits, Note: "邀请好友注册"})
	a.DB.Model(ref).Updates(map[string]any{"signup_paid": true, "pending": false, "earned": gorm.Expr("earned + ?", rules.InviterCredits)})
	return rules.InviterCredits
}

// ---------- payments and commissions ----------

// PaymentInput describes money a user paid. A payment gateway, once connected, records its paid orders
// with Source "order"; administrators record offline payments; priced redeem codes record themselves.
type PaymentInput struct {
	UserID     string
	StudioID   string
	Amount     int64 // fen
	Source     string
	RefID      string
	Summary    string
	OperatorID string
}

// RecordPayment stores a payment and pays the commission to the payer's inviter.
func (a *App) RecordPayment(in PaymentInput) (*model.Payment, error) {
	if in.Amount <= 0 {
		return nil, Bad("金额必须大于 0")
	}
	if in.Amount > 100_000_000_00 {
		return nil, Bad("金额过大")
	}
	p := &model.Payment{UserID: in.UserID, StudioID: in.StudioID, Amount: in.Amount, Source: in.Source, RefID: truncateStr(in.RefID, 64), Summary: truncateStr(in.Summary, 250), OperatorID: in.OperatorID}
	if err := a.DB.Create(p).Error; err != nil {
		return nil, err
	}
	a.referralPayment(p)
	return p, nil
}

// commissionCredits is what the inviter earns from a payment of amount fen.
func commissionCredits(amount int64, rules ReferralSettings) int64 {
	if rules.CommissionRate <= 0 || rules.CreditsPerYuan <= 0 {
		return 0
	}
	return int64(math.Floor(float64(amount) / 100 * rules.CommissionRate / 100 * float64(rules.CreditsPerYuan)))
}

func (a *App) referralPayment(p *model.Payment) {
	var ref model.Referral
	if a.DB.First(&ref, "invitee_id = ?", p.UserID).Error != nil {
		return
	}
	unlock := lockBook("referral-pay:" + ref.ID)
	defer unlock()
	a.DB.Model(&ref).Updates(map[string]any{"payments": gorm.Expr("payments + 1"), "paid": gorm.Expr("paid + ?", p.Amount)})
	a.DB.First(&ref, "id = ?", ref.ID)
	first := ref.Payments == 1
	rules := a.Referral()
	if !rules.Enabled {
		return
	}
	var ic model.InviteCode
	if a.DB.First(&ic, "user_id = ?", ref.InviterID).Error == nil && ic.Disabled {
		return // an administrator stopped this inviter's rewards
	}
	var invitee model.User
	a.DB.Select("id", "nickname").First(&invitee, "id = ?", p.UserID)
	if ref.Pending {
		if got := a.inviterSignupReward(&ref, &invitee, rules); got > 0 {
			a.Notify(ref.InviterID, "", "referral", "好友「"+invitee.Nickname+"」完成了首次充值", fmt.Sprintf("你获得了 %d 积分邀请奖励。", got), "/referral")
		}
	}
	if rules.CommissionOn == "first" && !first {
		return
	}
	if rules.CommissionDays > 0 && p.CreatedAt.After(ref.CreatedAt.AddDate(0, 0, rules.CommissionDays)) {
		return
	}
	credits := commissionCredits(p.Amount, rules)
	if credits <= 0 {
		return
	}
	var inviter model.User
	if a.DB.Select("id", "personal_studio", "status").First(&inviter, "id = ?", ref.InviterID).Error != nil || inviter.Status != model.UserActive || inviter.PersonalStudio == "" {
		return
	}
	if err := a.Grant(inviter.PersonalStudio, credits, "referral", a.rewardExpiry(rules), "好友「"+invitee.Nickname+"」充值返利", ""); err != nil {
		return
	}
	a.DB.Create(&model.ReferralReward{ReferralID: ref.ID, UserID: inviter.ID, OtherID: invitee.ID, StudioID: inviter.PersonalStudio, Kind: model.RewardCommission, Credits: credits, PaymentID: p.ID, Amount: p.Amount, Rate: rules.CommissionRate, Note: "好友充值返利"})
	a.DB.Model(&ref).Updates(map[string]any{"earned": gorm.Expr("earned + ?", credits), "commission": gorm.Expr("commission + 1")})
	a.Notify(ref.InviterID, "", "referral", fmt.Sprintf("获得 %d 积分充值返利", credits), "好友「"+invitee.Nickname+"」充值了，返利已到账。", "/referral")
}

// ---------- what users see ----------

// maskName shows only the first character of a friend's nickname.
func maskName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "新用户"
	}
	r, _ := utf8.DecodeRuneInString(s)
	return string(r) + "**"
}

// ReferralRules is the part of the rules users see.
func (a *App) ReferralRules() map[string]any {
	r := a.Referral()
	return map[string]any{
		"inviterCredits": r.InviterCredits, "inviteeCredits": r.InviteeCredits, "inviterAfter": r.InviterAfter, "creditDays": r.CreditDays,
		"dailyLimit": r.DailyLimit, "totalLimit": r.TotalLimit, "commissionRate": r.CommissionRate, "creditsPerYuan": r.CreditsPerYuan,
		"commissionOn": r.CommissionOn, "commissionDays": r.CommissionDays, "signupCredits": a.Site().SignupCredits,
	}
}

// MyReferral is the invite page of a user.
func (a *App) MyReferral(u *model.User) (map[string]any, error) {
	rules := a.Referral()
	if !rules.Enabled {
		return map[string]any{"enabled": false}, nil
	}
	ic, err := a.InviteCodeOf(u.ID)
	if err != nil {
		return nil, err
	}
	var signups, paying, pending int64
	a.DB.Model(&model.Referral{}).Where("inviter_id = ?", u.ID).Count(&signups)
	a.DB.Model(&model.Referral{}).Where("inviter_id = ? AND payments > 0", u.ID).Count(&paying)
	a.DB.Model(&model.Referral{}).Where("inviter_id = ? AND pending = ?", u.ID, true).Count(&pending)
	var earned int64
	a.DB.Model(&model.ReferralReward{}).Where("user_id = ? AND kind IN ?", u.ID, []string{model.RewardSignupInviter, model.RewardCommission}).Select("COALESCE(SUM(credits),0)").Scan(&earned)
	site := a.Site()
	return map[string]any{
		"enabled": true, "code": ic.Code, "link": a.InviteLink(ic.Code), "disabled": ic.Disabled,
		"renamesLeft": max(maxInviteRenames-ic.Renamed, 0), "rules": a.ReferralRules(),
		"stats":  map[string]any{"visits": ic.Visits, "signups": signups, "paying": paying, "pending": pending, "earned": earned},
		"poster": map[string]any{"title": rules.PosterTitle, "text": rules.PosterText, "siteName": site.Name, "slogan": site.Slogan, "nickname": u.Nickname},
	}, nil
}

// MyInvitees lists the friends a user invited.
func (a *App) MyInvitees(userID string, limit, offset int) (map[string]any, error) {
	var total int64
	a.DB.Model(&model.Referral{}).Where("inviter_id = ?", userID).Count(&total)
	var refs []model.Referral
	a.DB.Where("inviter_id = ?", userID).Order("created_at desc").Limit(limit).Offset(offset).Find(&refs)
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.InviteeID)
	}
	names := map[string]model.User{}
	var users []model.User
	a.DB.Select("id", "nickname", "avatar_url").Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		names[u.ID] = u
	}
	out := make([]map[string]any, 0, len(refs))
	for _, r := range refs {
		u := names[r.InviteeID]
		status := "signed_up"
		switch {
		case r.Payments > 0:
			status = "paid"
		case r.Pending:
			status = "pending"
		}
		out = append(out, map[string]any{"id": r.ID, "name": maskName(u.Nickname), "joinedAt": r.CreatedAt, "status": status, "earned": r.Earned})
	}
	return map[string]any{"items": out, "total": total}, nil
}

// MyReferralRewards lists the credits a user got from the invite program.
func (a *App) MyReferralRewards(userID string, limit, offset int) (map[string]any, error) {
	var total int64
	a.DB.Model(&model.ReferralReward{}).Where("user_id = ?", userID).Count(&total)
	var rows []model.ReferralReward
	a.DB.Where("user_id = ?", userID).Order("created_at desc").Limit(limit).Offset(offset).Find(&rows)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.OtherID)
	}
	names := map[string]string{}
	var users []model.User
	a.DB.Select("id", "nickname").Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		names[u.ID] = u.Nickname
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "kind": r.Kind, "credits": r.Credits, "friend": maskName(names[r.OtherID]), "createdAt": r.CreatedAt})
	}
	return map[string]any{"items": out, "total": total}, nil
}
