package api

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"voicebook/internal/core"
	"voicebook/internal/model"
	"voicebook/internal/secure"
	"voicebook/internal/version"
)

const (
	cookieStudio    = "vb_rt"
	cookieAdmin     = "vb_art"
	cookieOAuth     = "vb_oauth" // the encrypted state of a third-party sign-in in progress
	oauthCookiePath = "/api/v1/auth/oauth"
)

// sessionUserID is the studio account signed in in this browser, from the refresh cookie (top-level
// navigations such as the OAuth start and callback carry no access token).
func (s *Server) sessionUserID(r *http.Request) string {
	c, _ := r.Cookie(cookieStudio)
	if c == nil || c.Value == "" {
		return ""
	}
	var sess model.Session
	if s.app.DB.First(&sess, "refresh_hash = ? AND revoked_at IS NULL AND kind = ?", secure.SHA256(c.Value), "studio").Error != nil || sess.ExpiresAt.Before(time.Now()) {
		return ""
	}
	return sess.UserID
}

// oauthFailed sends the browser back with the reason: to the login page, or to the page that started linking.
func (s *Server) oauthFailed(w http.ResponseWriter, r *http.Request, redirect string, bind bool, err error) {
	if bind {
		if !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
			redirect = "/settings/account"
		}
		http.Redirect(w, r, withQuery(redirect, "oauth_error", err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login?error="+urlEscape(err.Error()), http.StatusFound)
}

func withQuery(path, k, v string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + k + "=" + urlEscape(v)
}

// secureCookie reports whether cookies for this request may be marked Secure: only when the browser
// reached us over HTTPS. A Secure cookie set over plain http (opening the site as http://IP:8080) is
// dropped by the browser, so the session would be lost on the next page load.
//
// HTTPS is known from the connection itself, from a reverse proxy's X-Forwarded-Proto / Forwarded
// header, or, for proxies that send neither, from the request being for the host of an https
// VB_PUBLIC_URL (people reach that address through the proxy, never by plain http).
func (s *Server) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		return strings.EqualFold(strings.TrimSpace(strings.Split(p, ",")[0]), "https")
	}
	if f := r.Header.Get("Forwarded"); f != "" {
		for _, part := range strings.Split(strings.Split(f, ",")[0], ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.EqualFold(k, "proto") {
				return strings.EqualFold(strings.Trim(v, `"`), "https")
			}
		}
	}
	pub, err := url.Parse(s.app.Cfg.PublicURL)
	return err == nil && pub.Scheme == "https" && strings.EqualFold(strings.TrimSuffix(r.Host, ":443"), pub.Host)
}

func refreshCookie(kind string) (name, path string) {
	if kind == "admin" {
		return cookieAdmin, "/api/admin/v1/auth"
	}
	return cookieStudio, "/api/v1/auth"
}

func (s *Server) setRefresh(w http.ResponseWriter, r *http.Request, kind string, pair *core.TokenPair) {
	name, path := refreshCookie(kind)
	http.SetCookie(w, &http.Cookie{Name: name, Value: pair.RefreshToken, Path: path, Expires: pair.RefreshExp, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r)})
}

func (s *Server) clearRefresh(w http.ResponseWriter, r *http.Request, kind string) {
	name, path := refreshCookie(kind)
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r)})
}

func userView(u *model.User) map[string]any {
	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	return map[string]any{
		"id": u.ID, "email": email, "nickname": u.Nickname, "avatarUrl": u.AvatarURL, "bio": u.Bio, "adminRoles": u.AdminRoles.V,
		"preferences": u.Preferences.V, "cv": u.CV.V, "realNameStatus": u.RealNameStatus, "realNameNote": u.RealNameNote, "personalStudioId": u.PersonalStudio,
		"emailVerified": u.EmailVerified, "phone": phoneOf(u), "hasPassword": u.PasswordHash != "", "createdAt": u.CreatedAt, "lastLoginAt": u.LastLoginAt, "deletionAt": u.DeletionAt, "status": u.Status,
		"legalVersion": u.LegalVersion, "legalAgreedAt": u.LegalAgreedAt,
	}
}

func (s *Server) authResponse(w http.ResponseWriter, r *http.Request, kind string, u *model.User, pair *core.TokenPair) {
	s.setRefresh(w, r, kind, pair)
	ok(w, map[string]any{"accessToken": pair.AccessToken, "expiresAt": pair.ExpiresAt, "user": userView(u)})
}

func (s *Server) publicRoutes(r chi.Router) {
	r.Get("/credits/guide", h(func(r *http.Request) (any, error) {
		return s.app.CreditGuide(), nil
	}))
	r.Get("/site", h(func(r *http.Request) (any, error) {
		site := s.app.Site()
		return map[string]any{
			"name": site.Name, "slogan": site.Slogan, "logo": site.Logo, "registrationOpen": site.RegistrationOpen, "inviteOnly": site.InviteOnly,
			"emailVerification": s.app.EmailVerificationRequired(), "devMode": s.app.Cfg.DevMode, "maxUploadMb": site.MaxUploadMB,
			"supportEmail": site.SupportEmail, "icp": site.ICP, "speechRate": site.SpeechRate,
			"phoneLogin": s.app.PhoneLoginAvailable(), "oauthProviders": s.app.OAuthProviders(),
			"legalVersion": s.app.LegalVersion(), "operator": site.Operator, "version": version.Version,
		}, nil
	}))
	// agreements and policies are public: they must be readable before signing up
	r.Get("/legal", h(func(r *http.Request) (any, error) {
		out := []map[string]any{}
		for _, d := range s.app.LegalDocs() {
			out = append(out, map[string]any{"slug": d.Slug, "title": d.Title, "summary": d.Summary, "version": d.Version, "updatedAt": d.UpdatedAt, "required": slices.Contains(core.RequiredLegal, d.Slug)})
		}
		return out, nil
	}))
	r.Get("/legal/{slug}", h(func(r *http.Request) (any, error) {
		var d model.Article
		if err := s.app.DB.First(&d, "kind = ? AND slug = ? AND published = ?", "legal", param(r, "slug"), true).Error; err != nil {
			return nil, core.NotFound("文档")
		}
		return map[string]any{"slug": d.Slug, "title": d.Title, "summary": d.Summary, "version": d.Version, "updatedAt": d.UpdatedAt, "body": s.app.RenderLegal(d.Body)}, nil
	}))
	r.Post("/auth/code", h(func(r *http.Request) (any, error) {
		var in struct{ Email, Purpose string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if in.Purpose != "reset" && in.Purpose != "login" {
			in.Purpose = "register"
		}
		code, err := s.app.SendCode(in.Email, in.Purpose)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"sent": true}
		if code != "" {
			out["devCode"] = code
		}
		return out, nil
	}))
	r.Post("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var in core.RegisterInput
		if err := bind(r, &in); err != nil {
			fail(w, err)
			return
		}
		u, err := s.app.Register(in)
		if err != nil {
			fail(w, err)
			return
		}
		_, pair, err := s.app.Login(*u.Email, in.Password, "studio", r.UserAgent(), clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		s.authResponse(w, r, "studio", u, pair)
	})
	r.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Email, Password string
			Agree           bool
		}
		if err := bind(r, &in); err != nil {
			fail(w, err)
			return
		}
		u, pair, err := s.app.Login(in.Email, in.Password, "studio", r.UserAgent(), clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		if in.Agree {
			u.LegalVersion = s.app.AgreeLegal(u.ID)
		}
		s.authResponse(w, r, "studio", u, pair)
	})
	r.Post("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(cookieStudio)
		val := ""
		if c != nil {
			val = c.Value
		}
		u, pair, err := s.app.Refresh(val, "studio", r.UserAgent(), clientIP(r))
		if err != nil {
			s.clearRefresh(w, r, "studio")
			fail(w, err)
			return
		}
		s.authResponse(w, r, "studio", u, pair)
	})
	r.Post("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie(cookieStudio); c != nil {
			var sess model.Session
			if s.app.DB.First(&sess, "refresh_hash = ?", secure.SHA256(c.Value)).Error == nil {
				s.app.RevokeSession(sess.UserID, sess.ID)
			}
		}
		s.clearRefresh(w, r, "studio")
		ok(w, nil)
	})
	r.Post("/auth/phone/code", h(func(r *http.Request) (any, error) {
		var in struct{ Phone, Purpose string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if in.Purpose != "bind" {
			in.Purpose = "login"
		}
		code, err := s.app.SendPhoneCode(r.Context(), in.Phone, in.Purpose)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"sent": true}
		if code != "" {
			out["devCode"] = code
		}
		return out, nil
	}))
	r.Post("/auth/login/code", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Target, Code string
			Agree        bool
		}
		if err := bind(r, &in); err != nil {
			fail(w, err)
			return
		}
		u, pair, err := s.app.LoginWithCode(in.Target, in.Code, "studio", r.UserAgent(), clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		if in.Agree {
			u.LegalVersion = s.app.AgreeLegal(u.ID)
		}
		s.authResponse(w, r, "studio", u, pair)
	})
	r.Get("/auth/oauth/{provider}/start", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect, bindUser := q.Get("redirect"), ""
		if q.Get("mode") == "bind" {
			if bindUser = s.sessionUserID(r); bindUser == "" {
				http.Redirect(w, r, "/login?error="+urlEscape("请先登录，再到账号设置里绑定"), http.StatusFound)
				return
			}
			redirect = firstNonEmptyStr(redirect, "/settings/account")
		}
		u, cookie, err := s.app.OAuthBegin(r.Context(), param(r, "provider"), redirect, bindUser, q.Get("agree") == "1")
		if err != nil {
			s.oauthFailed(w, r, redirect, bindUser != "", err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieOAuth, Value: cookie, Path: oauthCookiePath, MaxAge: int(core.OAuthFlowTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r)})
		http.Redirect(w, r, u, http.StatusFound)
	})
	r.Get("/auth/oauth/{provider}/callback", func(w http.ResponseWriter, r *http.Request) {
		flow := ""
		if c, _ := r.Cookie(cookieOAuth); c != nil {
			flow = c.Value
		}
		http.SetCookie(w, &http.Cookie{Name: cookieOAuth, Value: "", Path: oauthCookiePath, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secureCookie(r)})
		res, err := s.app.OAuthFinish(r.Context(), param(r, "provider"), r.URL.Query(), flow, s.sessionUserID(r), r.UserAgent(), clientIP(r))
		if err != nil {
			redirect, bind := s.app.OAuthFlowRedirect(flow)
			s.oauthFailed(w, r, redirect, bind, err)
			return
		}
		if res.Bound {
			http.Redirect(w, r, withQuery(res.Redirect, "oauth", "bound"), http.StatusFound)
			return
		}
		s.setRefresh(w, r, "studio", res.Pair)
		http.Redirect(w, r, "/oauth/done?next="+urlEscape(res.Redirect), http.StatusFound)
	})
	r.Post("/auth/reset", h(func(r *http.Request) (any, error) {
		var in struct{ Email, Code, Password string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return nil, s.app.ResetPassword(in.Email, in.Code, in.Password)
	}))
	r.Get("/invitations/{token}", h(func(r *http.Request) (any, error) {
		inv, st, b, err := s.app.InvitationInfo(param(r, "token"))
		if err != nil {
			return nil, err
		}
		var inviter model.User
		s.app.DB.Select("nickname", "avatar_url").First(&inviter, "id = ?", inv.InvitedBy)
		out := map[string]any{"studio": map[string]any{"id": st.ID, "name": st.Name, "logo": st.Logo, "description": st.Description}, "role": inv.Role, "positions": inv.Positions.V, "inviter": inviter.Nickname, "expiresAt": inv.ExpiresAt, "target": inv.Target}
		if b != nil {
			out["book"] = map[string]any{"id": b.ID, "title": b.Title}
		}
		return out, nil
	}))
	r.Get("/share/{token}", h(func(r *http.Request) (any, error) {
		l, err := s.app.ShareLinkByToken(param(r, "token"))
		if err != nil {
			return nil, err
		}
		return s.app.ShareView(l, r.URL.Query().Get("password"))
	}))
	r.Post("/share/{token}/comments", h(func(r *http.Request) (any, error) {
		l, err := s.app.ShareLinkByToken(param(r, "token"))
		if err != nil {
			return nil, err
		}
		var in struct {
			Password, ChapterID, SegmentID, Author, Body string
			AtMs                                         *int64
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return s.app.ShareComment(l, in.Password, in.ChapterID, in.SegmentID, in.AtMs, in.Author, in.Body)
	}))
	r.Get("/plans", h(func(r *http.Request) (any, error) {
		var plans []model.Plan
		s.app.DB.Where("listed = ?", true).Order("sort_order asc, level asc").Find(&plans)
		return plans, nil
	}))
	r.Get("/articles", h(func(r *http.Request) (any, error) {
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			kind = "help"
		}
		var list []model.Article
		q := s.app.DB.Where("published = ? AND kind = ?", true, kind)
		if c := r.URL.Query().Get("category"); c != "" {
			q = q.Where("category = ?", c)
		}
		q.Order("sort_order asc, created_at desc").Find(&list)
		return list, nil
	}))
	r.Get("/articles/{id}", h(func(r *http.Request) (any, error) {
		var a model.Article
		if err := s.app.DB.First(&a, "id = ? AND published = ?", param(r, "id"), true).Error; err != nil {
			return nil, core.NotFound("文章")
		}
		return a, nil
	}))
}

func (s *Server) meRoutes(r chi.Router) {
	r.Get("/events", s.events)
	r.Post("/me/legal/agree", h(func(r *http.Request) (any, error) {
		return map[string]any{"legalVersion": s.app.AgreeLegal(userOf(r).ID)}, nil
	}))
	r.Get("/me", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		type row struct {
			model.Studio
			Role       string
			MemberStat string
		}
		var members []model.StudioMember
		s.app.DB.Where("user_id = ?", u.ID).Find(&members)
		var studios []map[string]any
		for _, m := range members {
			var st model.Studio
			if s.app.DB.First(&st, "id = ? AND deleted_at IS NULL", m.StudioID).Error != nil {
				continue
			}
			var p model.Plan
			s.app.DB.Select("name", "code").First(&p, "id = ?", st.PlanID)
			studios = append(studios, map[string]any{"id": st.ID, "name": st.Name, "type": st.Type, "logo": st.Logo, "role": m.Role, "status": m.Status, "plan": p.Name, "planCode": p.Code})
		}
		var unread int64
		s.app.DB.Model(&model.Notification{}).Where("user_id = ? AND read_at IS NULL", u.ID).Count(&unread)
		m := s.app.MembershipRow(u.ID)
		mp := s.app.UserPlan(u.ID)
		member := map[string]any{"planName": mp.Name, "planCode": mp.Code, "level": mp.Level, "expiresAt": m.ExpiresAt, "isDefault": mp.ID == s.app.DefaultPlanFor().ID}
		return map[string]any{"user": userView(u), "studios": studios, "unread": unread, "membership": member}, nil
	}))
	r.Patch("/me", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var in struct {
			Nickname    *string                `json:"nickname"`
			AvatarURL   *string                `json:"avatarUrl"`
			Bio         *string                `json:"bio"`
			Preferences *model.UserPreferences `json:"preferences"`
			CV          *model.CVProfile       `json:"cv"`
		}
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		up := map[string]any{}
		if in.Nickname != nil {
			n := strings.TrimSpace(*in.Nickname)
			if n == "" {
				return nil, core.Bad("昵称不能为空")
			}
			up["nickname"] = n
		}
		if in.AvatarURL != nil {
			up["avatar_url"] = *in.AvatarURL
		}
		if in.Bio != nil {
			up["bio"] = *in.Bio
		}
		if in.Preferences != nil {
			if in.Preferences.Notify == nil {
				in.Preferences.Notify = map[string]bool{}
			}
			up["preferences"] = model.JSON[model.UserPreferences]{V: *in.Preferences}
		}
		if in.CV != nil {
			up["cv"] = model.JSON[model.CVProfile]{V: *in.CV}
		}
		if err := s.app.DB.Model(u).Updates(up).Error; err != nil {
			return nil, err
		}
		s.app.DB.First(u, "id = ?", u.ID)
		return userView(u), nil
	}))
	r.Post("/me/avatar", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		as, err := s.saveUpload(r, "file", core.AssetInput{Scope: model.ScopeUser, OwnerUserID: u.ID, Kind: model.AssetImage, Origin: "upload", CreatedBy: u.ID})
		if err != nil {
			return nil, err
		}
		url := s.app.Store.SignedPath(as.StorageKey, 10*365*24*time.Hour, "")
		s.app.DB.Model(u).Update("avatar_url", url)
		return map[string]any{"url": url}, nil
	}))
	r.Post("/me/password", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var in struct{ Old, New string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if !secure.CheckPassword(u.PasswordHash, in.Old) {
			return nil, core.Bad("当前密码不正确")
		}
		if err := core.ValidatePassword(in.New); err != nil {
			return nil, err
		}
		hash, _ := secure.HashPassword(in.New)
		s.app.DB.Model(u).Update("password_hash", hash)
		s.app.RevokeAll(u.ID, sessionOf(r))
		return nil, nil
	}))
	r.Get("/me/sessions", h(func(r *http.Request) (any, error) {
		var list []model.Session
		s.app.DB.Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userOf(r).ID, time.Now()).Order("last_used_at desc").Find(&list)
		cur := sessionOf(r)
		var out []map[string]any
		for _, x := range list {
			out = append(out, map[string]any{"id": x.ID, "kind": x.Kind, "userAgent": x.UserAgent, "ip": x.IP, "lastUsedAt": x.LastUsedAt, "createdAt": x.CreatedAt, "current": x.ID == cur})
		}
		return out, nil
	}))
	r.Delete("/me/sessions/{id}", h(func(r *http.Request) (any, error) {
		s.app.RevokeSession(userOf(r).ID, param(r, "id"))
		return nil, nil
	}))
	r.Post("/me/delete", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		var in struct{ Password string }
		bind(r, &in)
		if !secure.CheckPassword(u.PasswordHash, in.Password) {
			return nil, core.Bad("密码不正确")
		}
		var owned int64
		s.app.DB.Model(&model.Studio{}).Where("owner_id = ? AND type = ? AND deleted_at IS NULL", u.ID, "team").Count(&owned)
		if owned > 0 {
			return nil, core.Bad("请先转让或解散你创建的团队工作室")
		}
		t := time.Now().Add(7 * 24 * time.Hour)
		s.app.DB.Model(u).Update("deletion_at", &t)
		return map[string]any{"deletionAt": t}, nil
	}))
	r.Post("/me/delete/cancel", h(func(r *http.Request) (any, error) {
		return nil, s.app.DB.Model(userOf(r)).Update("deletion_at", nil).Error
	}))
	r.Post("/me/phone", h(func(r *http.Request) (any, error) {
		var in struct{ Phone, Code string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		return nil, s.app.BindPhone(userOf(r), in.Phone, in.Code)
	}))
	r.Get("/me/identities", h(func(r *http.Request) (any, error) {
		var list []model.OAuthIdentity
		s.app.DB.Where("user_id = ?", userOf(r).ID).Order("created_at").Find(&list)
		out := []map[string]any{}
		for _, i := range list {
			out = append(out, map[string]any{"id": i.ID, "provider": i.Provider, "providerName": s.app.OAuthProviderName(i.Provider), "name": i.Name, "email": i.Email, "avatar": i.Avatar, "createdAt": i.CreatedAt, "lastLoginAt": i.LastLoginAt})
		}
		return out, nil
	}))
	r.Delete("/me/identities/{id}", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		if u.PasswordHash == "" && u.Phone == nil {
			var n int64
			s.app.DB.Model(&model.OAuthIdentity{}).Where("user_id = ?", u.ID).Count(&n)
			if n <= 1 {
				return nil, core.Bad("这是你唯一的登录方式，请先设置密码或绑定手机")
			}
		}
		return nil, s.app.DB.Where("id = ? AND user_id = ?", param(r, "id"), u.ID).Delete(&model.OAuthIdentity{}).Error
	}))
	r.Post("/me/password/set", h(func(r *http.Request) (any, error) {
		u := userOf(r)
		if u.PasswordHash != "" {
			return nil, core.Bad("已设置过密码，请使用修改密码")
		}
		var in struct{ New string }
		bind(r, &in)
		if err := core.ValidatePassword(in.New); err != nil {
			return nil, err
		}
		hash, _ := secure.HashPassword(in.New)
		return nil, s.app.DB.Model(u).Update("password_hash", hash).Error
	}))
	r.Post("/me/realname", h(func(r *http.Request) (any, error) {
		var in struct{ Name, IDNumber string }
		if err := bind(r, &in); err != nil {
			return nil, err
		}
		if len([]rune(in.Name)) < 2 || len(in.IDNumber) < 15 {
			return nil, core.Bad("请填写真实姓名与有效证件号")
		}
		masked := in.IDNumber[:3] + strings.Repeat("*", len(in.IDNumber)-7) + in.IDNumber[len(in.IDNumber)-4:]
		return nil, s.app.DB.Model(userOf(r)).Updates(map[string]any{"real_name": in.Name, "id_number_masked": masked, "real_name_status": "pending"}).Error
	}))
	r.Post("/invitations/{token}/accept", h(func(r *http.Request) (any, error) {
		m, err := s.app.AcceptInvitation(userOf(r), param(r, "token"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"studioId": m.StudioID, "status": m.Status}, nil
	}))
	r.Get("/notifications", h(func(r *http.Request) (any, error) {
		q := s.app.DB.Model(&model.Notification{}).Where("user_id = ?", userOf(r).ID)
		if r.URL.Query().Get("unread") == "1" {
			q = q.Where("read_at IS NULL")
		}
		return paginate[model.Notification](r, q, "created_at desc")
	}))
	r.Post("/notifications/read", h(func(r *http.Request) (any, error) {
		var in struct {
			IDs []string `json:"ids"`
			All bool     `json:"all"`
		}
		bind(r, &in)
		t := time.Now()
		q := s.app.DB.Model(&model.Notification{}).Where("user_id = ? AND read_at IS NULL", userOf(r).ID)
		if !in.All {
			q = q.Where("id IN ?", in.IDs)
		}
		return nil, q.Update("read_at", &t).Error
	}))
}

func (s *Server) adminAuthRoutes(r chi.Router) {
	r.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Email, Password string }
		if err := bind(r, &in); err != nil {
			fail(w, err)
			return
		}
		u, pair, err := s.app.Login(in.Email, in.Password, "admin", r.UserAgent(), clientIP(r))
		if err != nil {
			fail(w, err)
			return
		}
		s.app.Audit(u.ID, "admin.login", "user", u.ID, "", clientIP(r), nil)
		s.authResponse(w, r, "admin", u, pair)
	})
	r.Post("/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(cookieAdmin)
		val := ""
		if c != nil {
			val = c.Value
		}
		u, pair, err := s.app.Refresh(val, "admin", r.UserAgent(), clientIP(r))
		if err != nil || !u.IsAdmin() {
			s.clearRefresh(w, r, "admin")
			if err == nil {
				err = core.Forbidden("没有管理后台权限")
			}
			fail(w, err)
			return
		}
		s.authResponse(w, r, "admin", u, pair)
	})
	r.Post("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie(cookieAdmin); c != nil {
			var sess model.Session
			if s.app.DB.First(&sess, "refresh_hash = ?", secure.SHA256(c.Value)).Error == nil {
				s.app.RevokeSession(sess.UserID, sess.ID)
			}
		}
		s.clearRefresh(w, r, "admin")
		ok(w, nil)
	})
}

func phoneOf(u *model.User) string {
	if u.Phone == nil {
		return ""
	}
	p := *u.Phone
	if len(p) >= 7 {
		return p[:3] + "****" + p[len(p)-4:]
	}
	return p
}
