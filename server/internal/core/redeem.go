package core

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"voicebook/internal/model"
)

// ---------------------------------------------------------------------------
// Redeem codes: administrators generate batches of codes that grant a membership (plan + days) or
// credits. "unique" batches hold N single-use codes (export and distribute them); "shared" batches have
// one public code (e.g. WELCOME2026) that many users can redeem, each up to the per-user limit.
// ---------------------------------------------------------------------------

// codeAlphabet leaves out look-alikes (0/O, 1/I/L) so codes survive being read aloud or retyped.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const maxBatchCodes = 20000

// NormalizeCode turns user input into the lookup key: upper case, letters and digits only.
func NormalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// newCode returns a random code like "VIP-7KQ2-MX9P-4TJD" (prefix optional) and its key.
func newCode(prefix string) (string, string) {
	const groups, size = 3, 4
	parts := make([]string, 0, groups+1)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	for g := 0; g < groups; g++ {
		var b strings.Builder
		for i := 0; i < size; i++ {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
			b.WriteByte(codeAlphabet[n.Int64()])
		}
		parts = append(parts, b.String())
	}
	code := strings.Join(parts, "-")
	return code, NormalizeCode(code)
}

// BatchInput is what an administrator fills in to create a batch.
type BatchInput struct {
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	PlanID       string     `json:"planId"`
	Days         int        `json:"days"`
	Credits      int64      `json:"credits"`
	CreditDays   int        `json:"creditDays"`
	Mode         string     `json:"mode"`
	Count        int        `json:"count"`
	Code         string     `json:"code"` // shared mode: the public code
	MaxUses      int        `json:"maxUses"`
	PerUserLimit *int       `json:"perUserLimit"`
	NewUsersOnly bool       `json:"newUsersOnly"`
	Prefix       string     `json:"prefix"`
	StartsAt     *time.Time `json:"startsAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
	Channel      string     `json:"channel"`
	Note         string     `json:"note"`
}

// CreateBatch validates the input and generates the codes.
func (a *App) CreateBatch(in BatchInput, operatorID string) (*model.RedeemBatch, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, Bad("请填写批次名称")
	}
	b := &model.RedeemBatch{Name: truncateStr(in.Name, 100), Kind: in.Kind, Mode: in.Mode, StartsAt: in.StartsAt, ExpiresAt: in.ExpiresAt,
		Channel: truncateStr(strings.TrimSpace(in.Channel), 60), Note: truncateStr(in.Note, 250), Status: "active", CreatedBy: operatorID,
		NewUsersOnly: in.NewUsersOnly, MaxUses: max(in.MaxUses, 0), PerUserLimit: 1}
	if in.PerUserLimit != nil {
		b.PerUserLimit = max(*in.PerUserLimit, 0)
	}
	switch in.Kind {
	case model.RedeemMembership:
		p, ok := a.planByID(in.PlanID)
		if !ok {
			return nil, Bad("请选择要兑换的会员套餐")
		}
		if p.ID == a.DefaultPlanFor().ID {
			return nil, Bad("「%s」是默认套餐，无需兑换", p.Name)
		}
		if in.Days < 0 || in.Days > 3650 {
			return nil, Bad("会员天数应在 1~3650 之间，0 表示永久")
		}
		b.PlanID, b.Days = p.ID, in.Days
	case model.RedeemCredits:
		if in.Credits <= 0 || in.Credits > 100_000_000 {
			return nil, Bad("积分数量应在 1~1 亿之间")
		}
		if in.CreditDays < 0 {
			return nil, Bad("积分有效期不能为负数")
		}
		b.Credits, b.CreditDays = in.Credits, in.CreditDays
	default:
		return nil, Bad("请选择兑换内容：会员或积分")
	}
	if b.StartsAt != nil && b.ExpiresAt != nil && !b.ExpiresAt.After(*b.StartsAt) {
		return nil, Bad("截止时间必须晚于开始时间")
	}
	b.Prefix = NormalizeCode(in.Prefix)
	if len(b.Prefix) > 6 {
		return nil, Bad("前缀最多 6 个字母或数字")
	}

	var codes []model.RedeemCode
	switch in.Mode {
	case model.RedeemModeUnique:
		if in.Count < 1 || in.Count > maxBatchCodes {
			return nil, Bad("生成数量应在 1~%d 之间", maxBatchCodes)
		}
		b.Count = in.Count
		seen := map[string]bool{}
		for len(codes) < in.Count {
			code, key := newCode(b.Prefix)
			if seen[key] {
				continue
			}
			seen[key] = true
			codes = append(codes, model.RedeemCode{Code: code, Key: key, Status: model.CodeUnused})
		}
	case model.RedeemModeShared:
		key := NormalizeCode(in.Code)
		if len(key) < 4 || len(key) > 32 {
			return nil, Bad("通用兑换码需 4~32 个字母或数字")
		}
		var n int64
		a.DB.Model(&model.RedeemCode{}).Where("key = ?", key).Count(&n)
		if n > 0 {
			return nil, Bad("兑换码 %s 已存在", key)
		}
		b.Count = 1
		codes = append(codes, model.RedeemCode{Code: key, Key: key, Status: model.CodeUnused})
	default:
		return nil, Bad("请选择生成方式：一码一用或通用码")
	}

	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(b).Error; err != nil {
			return err
		}
		for i := range codes {
			codes[i].BatchID = b.ID
		}
		return tx.CreateInBatches(codes, 500).Error
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") { // astronomically rare collision with an older code
			return nil, Bad("生成时与已有兑换码重复，请重试")
		}
		return nil, err
	}
	return b, nil
}

// RedeemOffer describes what a code grants, shown to the user before confirming.
type RedeemOffer struct {
	Kind       string `json:"kind"`
	PlanName   string `json:"planName,omitempty"`
	PlanCode   string `json:"planCode,omitempty"`
	Days       int    `json:"days"`
	Credits    int64  `json:"credits"`
	CreditDays int    `json:"creditDays"`
	Batch      string `json:"batch"`
	Summary    string `json:"summary"`
}

func offerSummary(b *model.RedeemBatch, planName string) string {
	if b.Kind == model.RedeemCredits {
		s := fmt.Sprintf("%d 积分", b.Credits)
		if b.CreditDays > 0 {
			s += fmt.Sprintf("（%d 天内有效）", b.CreditDays)
		}
		return s
	}
	if b.Days == 0 {
		return "永久「" + planName + "」"
	}
	return fmt.Sprintf("「%s」%d 天", planName, b.Days)
}

// lookupCode finds a code and checks everything that does not depend on the redeeming user.
func (a *App) lookupCode(tx *gorm.DB, raw string) (*model.RedeemCode, *model.RedeemBatch, error) {
	key := NormalizeCode(raw)
	if key == "" {
		return nil, nil, Bad("请输入兑换码")
	}
	var c model.RedeemCode
	if tx.First(&c, "key = ?", key).Error != nil {
		return nil, nil, &Error{Status: 400, Code: "code_invalid", Msg: "兑换码不存在，请检查是否输入正确"}
	}
	var b model.RedeemBatch
	if tx.First(&b, "id = ?", c.BatchID).Error != nil {
		return nil, nil, &Error{Status: 400, Code: "code_invalid", Msg: "兑换码不存在，请检查是否输入正确"}
	}
	t := now()
	switch {
	case c.Status == model.CodeDisabled || b.Status != "active":
		return nil, nil, &Error{Status: 400, Code: "code_disabled", Msg: "该兑换码已停用"}
	case b.Mode == model.RedeemModeUnique && c.Status == model.CodeUsed:
		return nil, nil, &Error{Status: 400, Code: "code_used", Msg: "该兑换码已被使用"}
	case b.StartsAt != nil && t.Before(*b.StartsAt):
		return nil, nil, &Error{Status: 400, Code: "code_not_started", Msg: "活动尚未开始，" + b.StartsAt.Local().Format("2006-01-02 15:04") + " 起可兑换"}
	case b.ExpiresAt != nil && !t.Before(*b.ExpiresAt):
		return nil, nil, &Error{Status: 400, Code: "code_expired", Msg: "该兑换码已过期"}
	case b.Mode == model.RedeemModeShared && b.MaxUses > 0 && b.Used >= b.MaxUses:
		return nil, nil, &Error{Status: 400, Code: "code_exhausted", Msg: "该兑换码已被领完"}
	}
	return &c, &b, nil
}

func (a *App) checkUserEligible(tx *gorm.DB, b *model.RedeemBatch, userID string) error {
	if b.PerUserLimit > 0 {
		var n int64
		tx.Model(&model.RedeemUse{}).Where("batch_id = ? AND user_id = ?", b.ID, userID).Count(&n)
		if n >= int64(b.PerUserLimit) {
			if b.PerUserLimit == 1 {
				return &Error{Status: 400, Code: "code_limit", Msg: "你已经参加过这个活动了，每人限兑 1 次"}
			}
			return &Error{Status: 400, Code: "code_limit", Msg: fmt.Sprintf("这个活动每人最多兑换 %d 次", b.PerUserLimit)}
		}
	}
	if b.NewUsersOnly {
		var m model.UserMembership
		if tx.First(&m, "user_id = ?", userID).Error == nil && m.EverPaid {
			return &Error{Status: 400, Code: "code_new_only", Msg: "该兑换码仅限从未开通过会员的新用户使用"}
		}
	}
	return nil
}

// guardRedeem limits attempts per user so codes cannot be guessed by brute force.
func (a *App) guardRedeem(userID string) error {
	if !a.Allow("redeem:"+userID, 10) {
		return TooMany("尝试太频繁了，请一分钟后再试")
	}
	return nil
}

// PreviewCode previews a code for the user without using it.
func (a *App) PreviewCode(userID, raw string) (*RedeemOffer, error) {
	if err := a.guardRedeem(userID); err != nil {
		return nil, err
	}
	_, b, err := a.lookupCode(a.DB, raw)
	if err != nil {
		return nil, err
	}
	if err := a.checkUserEligible(a.DB, b, userID); err != nil {
		return nil, err
	}
	o := &RedeemOffer{Kind: b.Kind, Days: b.Days, Credits: b.Credits, CreditDays: b.CreditDays, Batch: b.Name}
	if b.Kind == model.RedeemMembership {
		p, _ := a.planByID(b.PlanID)
		o.PlanName, o.PlanCode = p.Name, p.Code
	}
	o.Summary = offerSummary(b, o.PlanName)
	return o, nil
}

// RedeemResult is returned after a successful redemption.
type RedeemResult struct {
	Kind       string       `json:"kind"`
	Summary    string       `json:"summary"`
	Credits    int64        `json:"credits,omitempty"`
	StudioID   string       `json:"studioId,omitempty"`
	Membership *GrantResult `json:"membership,omitempty"`
}

// Redeem uses a code. Credits go to studioID (a studio the user manages; default: personal studio).
func (a *App) Redeem(u *model.User, raw, studioID, ip string) (*RedeemResult, error) {
	if err := a.guardRedeem(u.ID); err != nil {
		return nil, err
	}
	a.MembershipRow(u.ID)
	var code model.RedeemCode
	var batch model.RedeemBatch
	var use model.RedeemUse
	if studioID == "" {
		studioID = u.PersonalStudio
	}
	// claim the code atomically; the grant happens after commit (it uses its own transactions)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		c, b, err := a.lookupCode(tx, raw)
		if err != nil {
			return err
		}
		if err := a.checkUserEligible(tx, b, u.ID); err != nil {
			return err
		}
		if b.Kind == model.RedeemCredits {
			var m model.StudioMember
			if tx.First(&m, "studio_id = ? AND user_id = ? AND status = ?", studioID, u.ID, "active").Error != nil || (m.Role != model.RoleOwner && m.Role != model.RoleAdmin) {
				return Bad("积分只能兑换到你管理的工作室")
			}
		}
		t := now()
		up := map[string]any{"uses": gorm.Expr("uses + 1"), "used_by": u.ID, "used_at": t}
		q := tx.Model(&model.RedeemCode{}).Where("id = ?", c.ID)
		if b.Mode == model.RedeemModeUnique {
			up["status"] = model.CodeUsed
			q = q.Where("status = ?", model.CodeUnused)
		}
		if res := q.Updates(up); res.Error != nil || res.RowsAffected == 0 {
			return &Error{Status: 400, Code: "code_used", Msg: "该兑换码已被使用"}
		}
		bq := tx.Model(&model.RedeemBatch{}).Where("id = ?", b.ID)
		if b.Mode == model.RedeemModeShared && b.MaxUses > 0 {
			bq = bq.Where("used < ?", b.MaxUses)
		}
		if res := bq.Update("used", gorm.Expr("used + 1")); res.RowsAffected == 0 {
			return &Error{Status: 400, Code: "code_exhausted", Msg: "该兑换码已被领完"}
		}
		code, batch = *c, *b
		// the use record is written in the same transaction so per-user limits hold under concurrency
		use = model.RedeemUse{BatchID: b.ID, CodeID: c.ID, UserID: u.ID, Kind: b.Kind, PlanID: b.PlanID, Days: b.Days, IP: truncateStr(ip, 60)}
		if b.Kind == model.RedeemCredits {
			use.StudioID, use.Credits = studioID, b.Credits
		}
		return tx.Create(&use).Error
	})
	if err != nil {
		return nil, err
	}

	res := &RedeemResult{Kind: batch.Kind}
	var grantErr error
	switch batch.Kind {
	case model.RedeemMembership:
		g, err := a.GrantMembership(u.ID, batch.PlanID, batch.Days, model.MemberSourceRedeem, code.ID, "兑换码 "+code.Code, "")
		if err != nil {
			grantErr = err
			break
		}
		res.Membership, res.Summary = g, g.Summary
	case model.RedeemCredits:
		var exp *time.Time
		if batch.CreditDays > 0 {
			e := now().AddDate(0, 0, batch.CreditDays)
			exp = &e
		}
		if err := a.Grant(studioID, batch.Credits, "promo", exp, "兑换码 "+code.Code, u.ID); err != nil {
			grantErr = err
			break
		}
		res.Credits, res.StudioID = batch.Credits, studioID
		res.Summary = fmt.Sprintf("已到账 %d 积分", batch.Credits)
	}
	if grantErr != nil { // nothing was granted: give the code back
		a.DB.Delete(&model.RedeemUse{}, "id = ?", use.ID)
		a.releaseCode(&code, &batch)
		return nil, grantErr
	}
	a.DB.Model(&model.RedeemUse{}).Where("id = ?", use.ID).Update("summary", truncateStr(res.Summary, 250))
	a.Audit(u.ID, "redeem", "redeem_code", code.ID, "", ip, map[string]any{"batch": batch.ID, "kind": batch.Kind})
	return res, nil
}

func (a *App) releaseCode(c *model.RedeemCode, b *model.RedeemBatch) {
	up := map[string]any{"uses": gorm.Expr("CASE WHEN uses > 0 THEN uses - 1 ELSE 0 END")}
	if b.Mode == model.RedeemModeUnique {
		up["status"], up["used_by"], up["used_at"] = model.CodeUnused, "", nil
	}
	a.DB.Model(&model.RedeemCode{}).Where("id = ?", c.ID).Updates(up)
	a.DB.Model(&model.RedeemBatch{}).Where("id = ? AND used > 0", b.ID).Update("used", gorm.Expr("used - 1"))
}

// ExportBatchCSV renders a batch's codes as CSV (with a BOM so Excel opens Chinese text correctly).
func (a *App) ExportBatchCSV(batchID, status string) (string, []byte, error) {
	var b model.RedeemBatch
	if a.DB.First(&b, "id = ?", batchID).Error != nil {
		return "", nil, NotFound("批次")
	}
	q := a.DB.Model(&model.RedeemCode{}).Where("batch_id = ?", b.ID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var codes []model.RedeemCode
	q.Order("created_at asc, code asc").Find(&codes)
	planName := ""
	if b.Kind == model.RedeemMembership {
		p, _ := a.planByID(b.PlanID)
		planName = p.Name
	}
	users := map[string]string{}
	var ids []string
	for _, c := range codes {
		if c.UsedBy != "" {
			ids = append(ids, c.UsedBy)
		}
	}
	if len(ids) > 0 {
		var us []model.User
		a.DB.Select("id", "nickname", "email", "phone").Where("id IN ?", ids).Find(&us)
		for _, u := range us {
			users[u.ID] = firstNonEmpty(u.Nickname, deref(u.Email), deref(u.Phone))
		}
	}
	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM so Excel detects the encoding
	w := csv.NewWriter(&buf)
	w.Write([]string{"兑换码", "内容", "状态", "使用次数", "使用者", "使用时间", "截止时间", "批次", "渠道"})
	stateName := map[string]string{model.CodeUnused: "未使用", model.CodeUsed: "已使用", model.CodeDisabled: "已停用"}
	deadline := ""
	if b.ExpiresAt != nil {
		deadline = b.ExpiresAt.Local().Format("2006-01-02 15:04")
	}
	for _, c := range codes {
		usedAt := ""
		if c.UsedAt != nil {
			usedAt = c.UsedAt.Local().Format("2006-01-02 15:04")
		}
		st := stateName[c.Status]
		if b.Mode == model.RedeemModeShared && c.Status != model.CodeDisabled {
			st = "通用码"
		}
		w.Write([]string{c.Code, offerSummary(&b, planName), st, fmt.Sprint(c.Uses), users[c.UsedBy], usedAt, deadline, b.Name, b.Channel})
	}
	w.Flush()
	a.DB.Model(&model.RedeemBatch{}).Where("id = ?", b.ID).Update("exported", gorm.Expr("exported + 1"))
	name := fmt.Sprintf("兑换码-%s-%s.csv", safeFileName(b.Name), now().Local().Format("20060102"))
	return name, buf.Bytes(), nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
}

// migrateLegacyPromos turns old single promo codes into shared credit batches (runs once).
func (a *App) migrateLegacyPromos() {
	var promos []model.PromoCode
	a.DB.Find(&promos)
	for _, p := range promos {
		key := NormalizeCode(p.Code)
		var n int64
		a.DB.Model(&model.RedeemCode{}).Where("key = ?", key).Count(&n)
		if n > 0 || key == "" || p.Credits <= 0 {
			continue
		}
		status := "active"
		if !p.Enabled {
			status = "disabled"
		}
		b := model.RedeemBatch{Name: "通用码 " + key, Kind: model.RedeemCredits, Credits: p.Credits, CreditDays: 365, Mode: model.RedeemModeShared, Count: 1,
			MaxUses: p.MaxUses, PerUserLimit: 1, ExpiresAt: p.ExpiresAt, Note: p.Note, Status: status, Used: p.Uses}
		if a.DB.Create(&b).Error == nil {
			a.DB.Create(&model.RedeemCode{BatchID: b.ID, Code: key, Key: key, Status: model.CodeUnused, Uses: p.Uses})
		}
	}
}
