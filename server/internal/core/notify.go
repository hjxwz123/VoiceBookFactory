package core

import (
	"crypto/tls"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
	"unicode"

	"voicebook/internal/events"
	"voicebook/internal/model"
)

// Notify creates an in-app notification, pushes it live, and emails it when the user opted in.
func (a *App) Notify(userID, studioID, typ, title, body, link string) {
	a.notify(userID, studioID, typ, title, body, link, true)
}

func (a *App) notify(userID, studioID, typ, title, body, link string, mailIfOptedIn bool) {
	if userID == "" {
		return
	}
	n := model.Notification{UserID: userID, StudioID: studioID, Type: typ, Title: title, Body: truncateStr(body, 900), Link: link, Data: model.EmptyMap()}
	if err := a.DB.Create(&n).Error; err != nil {
		return
	}
	a.emitUser(userID, events.Event{Type: "notification", Data: n})
	if !mailIfOptedIn {
		return
	}
	var u model.User
	if a.DB.First(&u, "id = ?", userID).Error == nil && u.Email != nil && u.Preferences.V.Notify[typ] {
		go a.SendMail(*u.Email, title, fmt.Sprintf("<p>%s</p><p>%s</p><p><a href=\"%s%s\">在%s中查看</a></p>", title, body, a.Cfg.PublicURL, link, a.Site().Name))
	}
}

func (a *App) NotifyMany(userIDs []string, studioID, typ, title, body, link string) {
	seen := map[string]bool{}
	for _, id := range userIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			a.Notify(id, studioID, typ, title, body, link)
		}
	}
}

// Recipient is a user found for an administrator's message.
type Recipient struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

// ResolveRecipients finds users from what an administrator typed: email addresses, phone numbers or
// user IDs, one per line or separated by commas. It returns the users found and the entries that
// matched nobody.
func (a *App) ResolveRecipients(entries []string) ([]Recipient, []string) {
	var tokens []string
	for _, e := range entries {
		tokens = append(tokens, strings.FieldsFunc(e, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(",，;；、", r)
		})...)
	}
	var found []Recipient
	var unknown []string
	seen := map[string]bool{}
	for _, t := range tokens {
		var u model.User
		q := a.DB.Model(&model.User{})
		switch {
		case strings.Contains(t, "@"):
			q = q.Where("LOWER(email) = ?", strings.ToLower(t))
		case isPhone(t):
			p := strings.TrimPrefix(strings.TrimPrefix(t, "+86"), "86")
			q = q.Where("phone = ? OR phone = ?", p, "+86"+p)
		default:
			q = q.Where("id = ?", t)
		}
		if q.Limit(1).Find(&u).Error != nil || u.ID == "" {
			unknown = append(unknown, t)
			continue
		}
		if seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		r := Recipient{ID: u.ID, Nickname: u.Nickname}
		if u.Email != nil {
			r.Email = *u.Email
		}
		if u.Phone != nil {
			r.Phone = *u.Phone
		}
		found = append(found, r)
	}
	return found, unknown
}

func isPhone(s string) bool {
	s = strings.TrimPrefix(s, "+")
	if len(s) < 7 || len(s) > 15 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SendMessage delivers an administrator's message as an in-app notification to each user and, when
// asked (and email is set up), as an email too. It returns how many emails went out.
func (a *App) SendMessage(userIDs []string, title, body, link string, email bool) int {
	email = email && a.SMTPConfigured()
	seen := map[string]bool{}
	mailed := 0
	for _, id := range userIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		a.notify(id, "", "broadcast", title, body, link, !email) // with email on, it is sent once below
		if !email {
			continue
		}
		var u model.User
		if a.DB.Select("id", "email").First(&u, "id = ?", id).Error != nil || u.Email == nil || *u.Email == "" {
			continue
		}
		page := ""
		if link != "" {
			page = fmt.Sprintf(`<p><a href="%s%s">在%s中查看</a></p>`, strings.TrimRight(a.Cfg.PublicURL, "/"), html.EscapeString(link), html.EscapeString(a.Site().Name))
		}
		if a.SendMail(*u.Email, title, fmt.Sprintf("<p><strong>%s</strong></p><p>%s</p>%s", html.EscapeString(title), strings.ReplaceAll(html.EscapeString(body), "\n", "<br>"), page)) == nil {
			mailed++
		}
		time.Sleep(50 * time.Millisecond) // go easy on the mail server
	}
	return mailed
}

func (a *App) SMTPConfigured() bool {
	var s SMTPSettings
	a.Setting("smtp", &s)
	return s.Host != "" && s.From != ""
}

// SendMail sends an HTML email using the SMTP settings (implicit TLS on 465, STARTTLS otherwise).
func (a *App) SendMail(to, subject, html string) error {
	var s SMTPSettings
	a.Setting("smtp", &s)
	if s.Host == "" || s.From == "" {
		return fmt.Errorf("未配置邮件服务")
	}
	from := s.From
	if s.FromName != "" {
		from = fmt.Sprintf("%s <%s>", s.FromName, s.From)
	}
	msg := strings.Join([]string{
		"From: " + from, "To: " + to, "Subject: =?UTF-8?B?" + b64(subject) + "?=", "MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8", "Date: " + time.Now().Format(time.RFC1123Z), "", html,
	}, "\r\n")
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	pass := a.Decrypt(s.PassEnc)
	var c *smtp.Client
	var err error
	if s.Port == 465 || s.TLS && s.Port != 587 {
		conn, e := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: s.Host})
		if e != nil {
			return e
		}
		c, err = smtp.NewClient(conn, s.Host)
	} else {
		c, err = smtp.Dial(addr)
		if err == nil {
			if ok, _ := c.Extension("STARTTLS"); ok {
				err = c.StartTLS(&tls.Config{ServerName: s.Host})
			}
		}
	}
	if err != nil {
		slog.Warn("smtp connect failed", "err", err)
		return err
	}
	defer c.Close()
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, pass, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
