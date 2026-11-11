package core

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

// ======================= SMS (reserved adapters) =======================

type SMSSettings struct {
	Provider     string `json:"provider"` // "" (disabled) | aliyun | tencent
	AccessKey    string `json:"accessKey"`
	SecretEnc    string `json:"secretEnc"`
	SignName     string `json:"signName"`
	TemplateCode string `json:"templateCode"`
}

var ErrSMSNotImplemented = errors.New("短信平台适配器为预留接口，尚未实现")

// SMSSender sends verification codes; real platform adapters are reserved.
type SMSSender interface {
	Send(ctx context.Context, phone, code string) error
}

type smsStub string

func (s smsStub) Send(context.Context, string, string) error { return ErrSMSNotImplemented }

func (a *App) smsSender() (SMSSender, bool) {
	var s SMSSettings
	a.Setting("sms", &s)
	if s.Provider == "" {
		return nil, false
	}
	return smsStub(s.Provider), true
}

var phoneRe = regexp.MustCompile(`^(\+\d{8,15}|1[3-9]\d{9})$`)

func NormalizePhone(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), "-", "")
}

// PhoneLoginAvailable reports whether phone codes can be delivered (SMS configured or dev mode).
func (a *App) PhoneLoginAvailable() bool {
	_, ok := a.smsSender()
	return ok || a.Cfg.DevMode
}

// SendPhoneCode issues a code to a phone number (purpose: login | bind).
func (a *App) SendPhoneCode(ctx context.Context, phone, purpose string) (string, error) {
	phone = NormalizePhone(phone)
	if !phoneRe.MatchString(phone) {
		return "", Bad("请输入有效的手机号")
	}
	if !a.Allow("code:"+phone, 3) {
		return "", TooMany("验证码发送过于频繁，请稍后再试")
	}
	code := secure.Digits(6)
	a.DB.Create(&model.VerificationCode{Target: phone, Purpose: purpose, CodeHash: secure.SHA256(code), Expires: now().Add(10 * time.Minute)})
	if sender, ok := a.smsSender(); ok {
		if err := sender.Send(ctx, phone, code); err != nil {
			slog.Warn("sms send failed", "err", err)
			if !a.Cfg.DevMode {
				return "", Bad("短信发送失败：%v", err)
			}
		} else if !a.Cfg.DevMode {
			return "", nil
		}
	} else if !a.Cfg.DevMode {
		return "", Bad("平台未开通手机验证码登录")
	}
	slog.Info("verification code", "target", phone, "purpose", purpose, "code", code)
	return code, nil
}

// LoginWithCode signs in with an email or phone verification code; phone users are created on first login.
func (a *App) LoginWithCode(target, code, kind, ua, ip string) (*model.User, *TokenPair, error) {
	target = strings.TrimSpace(target)
	isPhone := phoneRe.MatchString(NormalizePhone(target))
	if isPhone {
		target = NormalizePhone(target)
	} else {
		target = NormalizeEmail(target)
	}
	if err := a.CheckCode(target, "login", code); err != nil {
		return nil, nil, err
	}
	var u model.User
	err := a.DB.Where("email = ? OR phone = ?", target, target).First(&u).Error
	if err != nil {
		if !isPhone || kind == "admin" {
			return nil, nil, Unauthorized("账号不存在，请先注册")
		}
		nu, err := a.createUser(nil, &target, "", "用户"+target[len(target)-4:], "")
		if err != nil {
			return nil, nil, err
		}
		u = *nu
	}
	return a.finishLogin(&u, kind, ua, ip)
}

func (a *App) finishLogin(u *model.User, kind, ua, ip string) (*model.User, *TokenPair, error) {
	if u.Status != model.UserActive {
		return nil, nil, Forbidden("账号已被停用，如有疑问请联系客服")
	}
	if kind == "admin" && !u.IsAdmin() {
		return nil, nil, Forbidden("该账号没有管理后台权限")
	}
	t := now()
	a.DB.Model(u).Updates(map[string]any{"failed_logins": 0, "locked_until": nil, "last_login_at": &t, "deletion_at": nil})
	pair, err := a.issue(u, kind, ua, ip)
	return u, pair, err
}

// createUser creates an account with a personal studio and signup credits.
func (a *App) createUser(email, phone *string, password, nickname, avatar string) (*model.User, error) {
	site := a.Site()
	if !site.RegistrationOpen {
		return nil, Forbidden("平台暂未开放注册")
	}
	u := &model.User{Email: email, Phone: phone, Nickname: truncateStr(nickname, 32), AvatarURL: avatar, Status: model.UserActive, AdminRoles: model.Strings(), EmailVerified: email != nil}
	if password != "" {
		h, err := secure.HashPassword(password)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = h
	}
	u.Preferences.V = model.UserPreferences{Theme: "system", Locale: "zh-CN", Notify: map[string]bool{}}
	u.CV.V = model.CVProfile{Tags: []string{}, Works: []string{}, DemoIDs: []string{}}
	plan := a.defaultPlan(a.DB)
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		st, err := a.createStudioTx(tx, u, u.Nickname+"的工作室", "personal", plan)
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
	return u, nil
}

// BindPhone attaches a verified phone number to an account.
func (a *App) BindPhone(u *model.User, phone, code string) error {
	phone = NormalizePhone(phone)
	if err := a.CheckCode(phone, "bind", code); err != nil {
		return err
	}
	var n int64
	a.DB.Model(&model.User{}).Where("phone = ? AND id <> ?", phone, u.ID).Count(&n)
	if n > 0 {
		return Conflict("该手机号已绑定其他账号")
	}
	return a.DB.Model(u).Update("phone", phone).Error
}
