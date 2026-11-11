package core

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"

	"voicebook/internal/events"
	"voicebook/internal/model"
)

// Notify creates an in-app notification, pushes it live, and emails it when the user opted in.
func (a *App) Notify(userID, studioID, typ, title, body, link string) {
	if userID == "" {
		return
	}
	n := model.Notification{UserID: userID, StudioID: studioID, Type: typ, Title: title, Body: truncateStr(body, 900), Link: link, Data: model.EmptyMap()}
	if err := a.DB.Create(&n).Error; err != nil {
		return
	}
	a.emitUser(userID, events.Event{Type: "notification", Data: n})
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
