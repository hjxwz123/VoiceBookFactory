package core

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func ValidatePassword(pw string) error {
	if len([]rune(pw)) < 8 {
		return Bad("密码至少需要 8 位")
	}
	var letter, digit bool
	for _, r := range pw {
		if unicode.IsLetter(r) {
			letter = true
		}
		if unicode.IsDigit(r) {
			digit = true
		}
	}
	if !letter || !digit {
		return Bad("密码需要同时包含字母和数字")
	}
	return nil
}

// EmailVerificationRequired reports whether registration needs an emailed code.
func (a *App) EmailVerificationRequired() bool { return a.SMTPConfigured() }

// SendCode issues a verification code. When email delivery is not configured and the server
// runs in dev mode, the code is returned so the flow can be completed locally.
func (a *App) SendCode(target, purpose string) (string, error) {
	target = NormalizeEmail(target)
	if !emailRe.MatchString(target) {
		return "", Bad("请输入有效的邮箱地址")
	}
	if !a.Allow("code:"+target, 3) {
		return "", TooMany("验证码发送过于频繁，请稍后再试")
	}
	if purpose == "register" {
		var n int64
		a.DB.Model(&model.User{}).Where("email = ?", target).Count(&n)
		if n > 0 {
			return "", Conflict("该邮箱已注册，请直接登录")
		}
	}
	code := secure.Digits(6)
	a.DB.Create(&model.VerificationCode{Target: target, Purpose: purpose, CodeHash: secure.SHA256(code), Expires: now().Add(10 * time.Minute)})
	site := a.Site()
	if a.SMTPConfigured() {
		body := fmt.Sprintf(`<div style="font-family:system-ui;padding:24px"><h2>%s</h2><p>你的验证码是：</p><p style="font-size:28px;letter-spacing:6px;font-weight:600">%s</p><p style="color:#888">10 分钟内有效。如非本人操作请忽略。</p></div>`, site.Name, code)
		if err := a.SendMail(target, site.Name+" 验证码："+code, body); err != nil {
			slog.Warn("send code mail failed", "err", err)
			if !a.Cfg.DevMode {
				return "", Bad("邮件发送失败，请稍后重试")
			}
		} else if !a.Cfg.DevMode {
			return "", nil
		}
	}
	slog.Info("verification code", "target", target, "purpose", purpose, "code", code)
	if a.Cfg.DevMode {
		return code, nil
	}
	return "", nil
}

func (a *App) CheckCode(target, purpose, code string) error {
	target = NormalizeEmail(target)
	var vc model.VerificationCode
	if err := a.DB.Where("target = ? AND purpose = ? AND used_at IS NULL AND expires > ?", target, purpose, now()).Order("created_at desc").First(&vc).Error; err != nil {
		return Bad("验证码无效或已过期，请重新获取")
	}
	if vc.Attempts >= 5 {
		return Bad("验证码错误次数过多，请重新获取")
	}
	if vc.CodeHash != secure.SHA256(strings.TrimSpace(code)) {
		a.DB.Model(&vc).Update("attempts", vc.Attempts+1)
		return Bad("验证码不正确")
	}
	t := now()
	a.DB.Model(&vc).Update("used_at", &t)
	return nil
}

type RegisterInput struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	Nickname   string `json:"nickname"`
	Code       string `json:"code"`
	InviteCode string `json:"inviteCode"`
	Invitation string `json:"invitation"` // studio invitation token
	Identity   string `json:"identity"`
	Agree      bool   `json:"agree"` // accepted the user agreement, privacy policy and copyright statement
}

func (a *App) Register(in RegisterInput) (*model.User, error) {
	site := a.Site()
	if !site.RegistrationOpen {
		return nil, Forbidden("平台暂未开放注册")
	}
	if !in.Agree {
		return nil, Bad("请先阅读并同意《用户服务协议》《隐私政策》和《版权与素材使用声明》")
	}
	email := NormalizeEmail(in.Email)
	if !emailRe.MatchString(email) {
		return nil, Bad("请输入有效的邮箱地址")
	}
	if err := ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	if site.InviteOnly && in.Invitation == "" && (site.InviteCode == "" || in.InviteCode != site.InviteCode) {
		return nil, Forbidden("当前为邀请制注册，请填写正确的邀请码")
	}
	if a.EmailVerificationRequired() {
		if err := a.CheckCode(email, "register", in.Code); err != nil {
			return nil, err
		}
	}
	var n int64
	a.DB.Model(&model.User{}).Where("email = ?", email).Count(&n)
	if n > 0 {
		return nil, Conflict("该邮箱已注册，请直接登录")
	}
	hash, err := secure.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	nick := strings.TrimSpace(in.Nickname)
	if nick == "" {
		nick = strings.Split(email, "@")[0]
	}
	agreedAt := now()
	u := &model.User{Email: &email, PasswordHash: hash, Nickname: truncateStr(nick, 32), Status: model.UserActive, EmailVerified: a.EmailVerificationRequired(), AdminRoles: model.Strings(), LegalVersion: a.LegalVersion(), LegalAgreedAt: &agreedAt}
	u.Preferences.V = model.UserPreferences{Theme: "system", Locale: "zh-CN", Identity: in.Identity, Notify: map[string]bool{}}
	u.CV.V = model.CVProfile{Tags: []string{}, Works: []string{}, DemoIDs: []string{}}
	plan := a.defaultPlan(a.DB) // resolved before the transaction (SQLite has a single connection)
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		st, err := a.createStudioTx(tx, u, nick+"的工作室", "personal", plan)
		if err != nil {
			return err
		}
		u.PersonalStudio = st.ID
		return tx.Model(u).Update("personal_studio", st.ID).Error
	})
	if err != nil {
		return nil, err
	}
	if site.SignupCredits > 0 {
		exp := now().AddDate(1, 0, 0)
		a.Grant(u.PersonalStudio, site.SignupCredits, "signup", &exp, "新用户注册赠送", "")
	}
	a.welcomeMembership(u)
	if in.Invitation != "" {
		a.AcceptInvitation(u, in.Invitation)
	}
	a.Notify(u.ID, u.PersonalStudio, "welcome", "欢迎来到"+site.Name, "我们为你准备了新手引导：从上传一本小说开始，几分钟就能听到第一章的多角色有声书。", "/")
	return u, nil
}

func (a *App) defaultPlan(tx *gorm.DB) *model.Plan {
	code := a.Billing().DefaultPlan
	var p model.Plan
	if tx.First(&p, "code = ?", code).Error == nil {
		return &p
	}
	if tx.Order("sort_order asc").First(&p).Error == nil {
		return &p
	}
	return nil
}

// createStudioTx must only use tx: any a.DB call inside a transaction deadlocks on SQLite.
func (a *App) createStudioTx(tx *gorm.DB, owner *model.User, name, typ string, p *model.Plan) (*model.Studio, error) {
	st := &model.Studio{Name: truncateStr(name, 60), Type: typ, OwnerID: owner.ID, Status: "active", Features: model.Strings()}
	st.Settings.V = model.DefaultStudioSettings()
	if p != nil {
		st.PlanID = p.ID
		renew := now().AddDate(0, 1, 0)
		st.PlanRenewAt = &renew
	}
	if err := tx.Create(st).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(&model.StudioMember{StudioID: st.ID, UserID: owner.ID, Role: model.RoleOwner, Status: "active"}).Error; err != nil {
		return nil, err
	}
	return st, nil
}

// CreateStudio creates a team studio owned by u.
func (a *App) CreateStudio(u *model.User, name, desc string) (*model.Studio, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, Bad("请填写工作室名称")
	}
	if err := a.CheckStudioLimit(u.ID); err != nil {
		return nil, err
	}
	var st *model.Studio
	plan := a.UserPlan(u.ID) // studios follow their owner's membership
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		st, err = a.createStudioTx(tx, u, name, "team", &plan)
		if err == nil && desc != "" {
			st.Description = desc
			tx.Model(st).Update("description", desc)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	// no credits here: plan credits are granted once per member (to the studio they choose), not per studio
	return st, nil
}

// welcomeMembership sets up a new account's membership, including the new-user trial if configured.
func (a *App) welcomeMembership(u *model.User) {
	a.MembershipRow(u.ID)
	b := a.Billing()
	if b.TrialPlan != "" && b.TrialDays > 0 {
		var p model.Plan
		if a.DB.First(&p, "code = ?", b.TrialPlan).Error == nil && p.ID != a.DefaultPlanFor().ID {
			if _, err := a.GrantMembership(u.ID, p.ID, b.TrialDays, model.MemberSourceTrial, "", "新用户试用", ""); err == nil {
				return
			}
		}
	}
	a.RefreshMembership(u.ID)
}

// ---------- sessions ----------

type TokenPair struct {
	AccessToken  string    `json:"accessToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
	RefreshToken string    `json:"-"`
	RefreshExp   time.Time `json:"-"`
}

func (a *App) issue(u *model.User, kind, ua, ip string) (*TokenPair, error) {
	refresh := secure.Token(32)
	exp := now().Add(30 * 24 * time.Hour)
	s := model.Session{UserID: u.ID, Kind: kind, RefreshHash: secure.SHA256(refresh), UserAgent: truncateStr(ua, 250), IP: ip, ExpiresAt: exp, LastUsedAt: now()}
	if err := a.DB.Create(&s).Error; err != nil {
		return nil, err
	}
	tok, texp, err := secure.IssueAccess(a.Cfg.JWTSecret, u.ID, s.ID, kind, 30*time.Minute)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: tok, ExpiresAt: texp, RefreshToken: refresh, RefreshExp: exp}, nil
}

func (a *App) Login(identifier, password, kind, ua, ip string) (*model.User, *TokenPair, error) {
	id := NormalizeEmail(identifier)
	if !a.Allow("login:"+ip, 20) {
		return nil, nil, TooMany("登录尝试过于频繁，请稍后再试")
	}
	var u model.User
	if err := a.DB.Where("email = ? OR phone = ?", id, id).First(&u).Error; err != nil {
		return nil, nil, Unauthorized("账号或密码不正确")
	}
	if u.LockedUntil != nil && u.LockedUntil.After(now()) {
		return nil, nil, Forbidden(fmt.Sprintf("登录失败次数过多，账号已临时锁定，请在 %s 后重试", u.LockedUntil.Local().Format("15:04")))
	}
	if !secure.CheckPassword(u.PasswordHash, password) {
		fails := u.FailedLogins + 1
		up := map[string]any{"failed_logins": fails}
		if fails >= 10 {
			lock := now().Add(15 * time.Minute)
			up["locked_until"] = &lock
			up["failed_logins"] = 0
		}
		a.DB.Model(&u).Updates(up)
		if fails >= 5 && fails < 10 {
			return nil, nil, Unauthorized(fmt.Sprintf("账号或密码不正确（再错 %d 次将锁定 15 分钟）", 10-fails))
		}
		return nil, nil, Unauthorized("账号或密码不正确")
	}
	if u.Status != model.UserActive {
		return nil, nil, Forbidden("账号已被停用，如有疑问请联系客服")
	}
	if kind == "admin" && !u.IsAdmin() {
		return nil, nil, Forbidden("该账号没有管理后台权限")
	}
	t := now()
	a.DB.Model(&u).Updates(map[string]any{"failed_logins": 0, "locked_until": nil, "last_login_at": &t, "deletion_at": nil})
	pair, err := a.issue(&u, kind, ua, ip)
	return &u, pair, err
}

func (a *App) Refresh(refresh, kind, ua, ip string) (*model.User, *TokenPair, error) {
	var s model.Session
	if refresh == "" || a.DB.First(&s, "refresh_hash = ?", secure.SHA256(refresh)).Error != nil {
		return nil, nil, Unauthorized("登录已过期，请重新登录")
	}
	if s.RevokedAt != nil || s.ExpiresAt.Before(now()) || s.Kind != kind {
		return nil, nil, Unauthorized("登录已过期，请重新登录")
	}
	var u model.User
	if a.DB.First(&u, "id = ?", s.UserID).Error != nil || u.Status != model.UserActive {
		return nil, nil, Unauthorized("账号不可用")
	}
	newRefresh := secure.Token(32)
	exp := now().Add(30 * 24 * time.Hour)
	a.DB.Model(&s).Updates(map[string]any{"refresh_hash": secure.SHA256(newRefresh), "expires_at": exp, "last_used_at": now(), "ip": ip, "user_agent": truncateStr(ua, 250)})
	tok, texp, err := secure.IssueAccess(a.Cfg.JWTSecret, u.ID, s.ID, kind, 30*time.Minute)
	if err != nil {
		return nil, nil, err
	}
	return &u, &TokenPair{AccessToken: tok, ExpiresAt: texp, RefreshToken: newRefresh, RefreshExp: exp}, nil
}

func (a *App) RevokeSession(userID, sessionID string) {
	t := now()
	a.DB.Model(&model.Session{}).Where("id = ? AND user_id = ?", sessionID, userID).Update("revoked_at", &t)
}

func (a *App) RevokeAll(userID, except string) {
	t := now()
	a.DB.Model(&model.Session{}).Where("user_id = ? AND id <> ? AND revoked_at IS NULL", userID, except).Update("revoked_at", &t)
}

func (a *App) ResetPassword(email, code, password string) error {
	email = NormalizeEmail(email)
	if err := ValidatePassword(password); err != nil {
		return err
	}
	if err := a.CheckCode(email, "reset", code); err != nil {
		return err
	}
	var u model.User
	if err := a.DB.First(&u, "email = ?", email).Error; err != nil {
		return NotFound("账号")
	}
	hash, _ := secure.HashPassword(password)
	a.DB.Model(&u).Updates(map[string]any{"password_hash": hash, "failed_logins": 0, "locked_until": nil})
	a.RevokeAll(u.ID, "")
	return nil
}

// ---------- membership ----------

func (a *App) SeatLimit(studioID string) int {
	var st model.Studio
	if a.DB.First(&st, "id = ?", studioID).Error != nil {
		return 0
	}
	if st.SeatLimit > 0 {
		return st.SeatLimit
	}
	var p model.Plan
	if st.PlanID != "" && a.DB.First(&p, "id = ?", st.PlanID).Error == nil {
		return p.Seats
	}
	return 1
}

func (a *App) SeatsUsed(studioID string) int {
	var st model.Studio
	a.DB.First(&st, "id = ?", studioID)
	q := a.DB.Model(&model.StudioMember{}).Where("studio_id = ? AND status = ?", studioID, "active")
	if !st.Settings.V.GuestCountsSeat {
		q = q.Where("role <> ?", model.RoleGuest)
	}
	var n int64
	q.Count(&n)
	return int(n)
}

func (a *App) CreateInvitation(x *Access, target, role, bookID string, positions []string, days, maxUses int) (*model.Invitation, error) {
	if bookID == "" && !x.IsManager() {
		return nil, Forbidden("只有所有者或管理员可以邀请成员")
	}
	if role == model.RoleOwner {
		return nil, Bad("不能直接邀请为所有者，请使用「转让工作室」")
	}
	if role == "" {
		role = model.RoleMember
	}
	if bookID != "" && role != model.RoleGuest && role != model.RoleMember {
		role = model.RoleGuest
	}
	if days <= 0 {
		days = 7
	}
	inv := &model.Invitation{StudioID: x.StudioID, BookID: bookID, Target: NormalizeEmail(target), Role: role, Positions: model.Strings(positions...), Token: secure.Token(24), InvitedBy: x.User.ID, ExpiresAt: now().AddDate(0, 0, days), MaxUses: maxUses}
	if err := a.DB.Create(inv).Error; err != nil {
		return nil, err
	}
	if inv.Target != "" && emailRe.MatchString(inv.Target) {
		var st model.Studio
		a.DB.First(&st, "id = ?", x.StudioID)
		link := a.Cfg.PublicURL + "/invite/" + inv.Token
		go a.SendMail(inv.Target, x.User.Nickname+" 邀请你加入「"+st.Name+"」", fmt.Sprintf(`<p>%s 邀请你加入工作室「%s」。</p><p><a href="%s">点击接受邀请</a>（%d 天内有效）</p>`, x.User.Nickname, st.Name, link, days))
		var invitee model.User
		if a.DB.First(&invitee, "email = ?", inv.Target).Error == nil {
			a.Notify(invitee.ID, x.StudioID, "invitation", "你收到一个工作室邀请", x.User.Nickname+" 邀请你加入「"+st.Name+"」", "/invite/"+inv.Token)
		}
	}
	return inv, nil
}

func (a *App) InvitationInfo(token string) (*model.Invitation, *model.Studio, *model.Book, error) {
	var inv model.Invitation
	if err := a.DB.First(&inv, "token = ?", token).Error; err != nil {
		return nil, nil, nil, NotFound("邀请")
	}
	if inv.RevokedAt != nil || inv.ExpiresAt.Before(now()) || (inv.MaxUses > 0 && inv.Uses >= inv.MaxUses) {
		return nil, nil, nil, Bad("邀请链接已失效，请联系邀请人重新发送")
	}
	var st model.Studio
	a.DB.First(&st, "id = ?", inv.StudioID)
	var b *model.Book
	if inv.BookID != "" {
		var bk model.Book
		if a.DB.First(&bk, "id = ?", inv.BookID).Error == nil {
			b = &bk
		}
	}
	return &inv, &st, b, nil
}

func (a *App) AcceptInvitation(u *model.User, token string) (*model.StudioMember, error) {
	inv, st, _, err := a.InvitationInfo(token)
	if err != nil {
		return nil, err
	}
	var m model.StudioMember
	existing := a.DB.First(&m, "studio_id = ? AND user_id = ?", st.ID, u.ID).Error == nil
	if !existing {
		countsSeat := inv.Role != model.RoleGuest || st.Settings.V.GuestCountsSeat
		if countsSeat && a.SeatsUsed(st.ID) >= a.SeatLimit(st.ID) {
			return nil, Forbidden("该工作室的成员席位已满，请联系管理员升级套餐")
		}
		status := "active"
		if st.Settings.V.JoinApproval {
			status = "pending"
		}
		m = model.StudioMember{StudioID: st.ID, UserID: u.ID, Role: inv.Role, Status: status}
		if err := a.DB.Create(&m).Error; err != nil {
			return nil, err
		}
		if status == "pending" {
			a.notifyManagers(st.ID, "join_request", u.Nickname+" 申请加入工作室", "请在成员管理中审批", "/studio/members")
		} else {
			a.notifyManagers(st.ID, "member_joined", u.Nickname+" 加入了工作室", "", "/studio/members")
		}
	}
	if inv.BookID != "" {
		var bm model.BookMember
		if a.DB.First(&bm, "book_id = ? AND user_id = ?", inv.BookID, u.ID).Error != nil {
			a.DB.Create(&model.BookMember{BookID: inv.BookID, UserID: u.ID, Positions: inv.Positions, AddedBy: inv.InvitedBy})
		} else {
			merged := bm.Positions.V
			for _, p := range inv.Positions.V {
				if !contains(merged, p) {
					merged = append(merged, p)
				}
			}
			a.DB.Model(&bm).Update("positions", model.Strings(merged...))
		}
	}
	a.DB.Model(inv).Update("uses", inv.Uses+1)
	return &m, nil
}

// NotifyManagers notifies a studio's owners and admins.
func (a *App) NotifyManagers(studioID, typ, title, body, link string) {
	a.notifyManagers(studioID, typ, title, body, link)
}

func (a *App) notifyManagers(studioID, typ, title, body, link string) {
	var ids []string
	a.DB.Model(&model.StudioMember{}).Where("studio_id = ? AND role IN ? AND status = ?", studioID, []string{model.RoleOwner, model.RoleAdmin}, "active").Pluck("user_id", &ids)
	a.NotifyMany(ids, studioID, typ, title, body, link)
}

func (a *App) TransferStudio(x *Access, toUserID string) error {
	if x.Role != model.RoleOwner {
		return Forbidden("只有所有者可以转让工作室")
	}
	var target model.StudioMember
	if err := a.DB.First(&target, "studio_id = ? AND user_id = ? AND status = ?", x.StudioID, toUserID, "active").Error; err != nil {
		return Bad("对方必须是该工作室的成员")
	}
	if err := a.CheckStudioLimit(toUserID); err != nil {
		return Bad("对方的会员最多可拥有 %d 个工作室，已达上限，需对方升级会员后再转让", a.UserPlan(toUserID).MaxStudios)
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.StudioMember{}).Where("studio_id = ? AND user_id = ?", x.StudioID, x.User.ID).Update("role", model.RoleAdmin)
		tx.Model(&target).Update("role", model.RoleOwner)
		return tx.Model(&model.Studio{}).Where("id = ?", x.StudioID).Update("owner_id", toUserID).Error
	})
	if err == nil {
		a.SyncStudioPlan(x.StudioID) // the studio now follows the new owner's membership
	}
	return err
}
