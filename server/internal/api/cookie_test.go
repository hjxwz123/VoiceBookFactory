package api

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"voicebook/internal/config"
	"voicebook/internal/core"
)

// Opening the site by plain http (e.g. http://1.2.3.4:8080) must not get Secure cookies, or the browser
// drops the refresh cookie and every reload asks to sign in again, even when VB_PUBLIC_URL is https.
func TestSecureCookieFollowsTheRequest(t *testing.T) {
	s := &Server{app: &core.App{Cfg: &config.Config{PublicURL: "https://voicebook.example.com"}}}
	cases := []struct {
		name   string
		host   string
		header map[string]string
		tls    bool
		want   bool
	}{
		{"plain http by IP", "1.2.3.4:8080", nil, false, false},
		{"plain http by IP with port 80", "1.2.3.4", nil, false, false},
		{"TLS straight to the app", "1.2.3.4:8443", nil, true, true},
		{"proxy says https", "127.0.0.1:8080", map[string]string{"X-Forwarded-Proto": "https"}, false, true},
		{"proxy says http", "voicebook.example.com", map[string]string{"X-Forwarded-Proto": "http"}, false, false},
		{"proxy chain, first hop https", "x", map[string]string{"X-Forwarded-Proto": "https, http"}, false, true},
		{"RFC 7239 Forwarded", "x", map[string]string{"Forwarded": `for=1.2.3.4;proto=https;host=voicebook.example.com`}, false, true},
		{"proxy without headers, public https host", "voicebook.example.com", nil, false, true},
		{"proxy without headers, public host with :443", "voicebook.example.com:443", nil, false, true},
		{"same name on another port", "voicebook.example.com:8080", nil, false, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
		r.Host = c.host
		for k, v := range c.header {
			r.Header.Set(k, v)
		}
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := s.secureCookie(r); got != c.want {
			t.Errorf("%s: secure = %v, want %v", c.name, got, c.want)
		}
	}
	// an http public URL never asks for Secure cookies
	s.app.Cfg.PublicURL = "http://1.2.3.4:8080"
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.Host = "1.2.3.4:8080"
	if s.secureCookie(r) {
		t.Error("http public URL must not mark cookies Secure")
	}
}
