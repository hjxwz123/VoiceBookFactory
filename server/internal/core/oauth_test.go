package core

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"voicebook/internal/model"
)

// fakeIdP is an OpenID Connect provider (and a GitHub-style OAuth 2.0 API) on httptest.
type fakeIdP struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	issuer   string // what discovery and tokens claim (may hold {tenantid})
	mu       sync.Mutex
	codes    map[string]grant
	sub      string
	email    string
	verified bool
	// knobs to break the ID token
	nonce, aud, iss string
	exp             time.Duration
	signWith        *rsa.PrivateKey
	hs256           string
	tid             string
}

type grant struct{ challenge, nonce, redirect string }

func newFakeIdP(t *testing.T) *fakeIdP {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, key: k, codes: map[string]grant{}, sub: "u-1001", email: "lin@example.com", verified: true, exp: time.Hour}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	f.issuer = f.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": f.issuer, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"userinfo_endpoint": f.srv.URL + "/userinfo", "jwks_uri": f.srv.URL + "/jwks", "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		e := big.NewInt(int64(k.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id, secret, basic := r.BasicAuth()
		if !basic {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		f.mu.Lock()
		g, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case id != "client-1" || secret != "s3cret":
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		case !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge || r.PostForm.Get("redirect_uri") != g.redirect:
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "bad code or verifier"})
			return
		}
		out := map[string]any{"access_token": "at-" + f.sub, "token_type": "Bearer"}
		if r.URL.Query().Get("plain") == "" {
			out["id_token"] = f.idToken(g.nonce)
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-"+f.sub {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sub": f.sub, "name": "林晚", "picture": "https://img.example.com/a.png", "email": f.email, "email_verified": f.verified})
	})
	// GitHub-style API: numeric id, public email that is not the verified one, and an email list
	mux.HandleFunc("/api/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-"+f.sub {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"id": 583231, "login": "linwan", "name": null, "email": "public@example.com", "avatar_url": "https://img.example.com/gh.png"}`))
	})
	mux.HandleFunc("/api/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"email":"old@example.com","primary":false,"verified":true},{"email":"lw@example.com","primary":true,"verified":true},{"email":"x@example.com","primary":false,"verified":false}]`))
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) idToken(nonce string) string {
	iss := strings.ReplaceAll(f.issuer, "{tenantid}", f.tid)
	claims := jwt.MapClaims{"iss": firstNonEmpty(f.iss, iss), "sub": f.sub, "aud": firstNonEmpty(f.aud, "client-1"), "exp": time.Now().Add(f.exp).Unix(), "iat": time.Now().Unix(),
		"nonce": firstNonEmpty(f.nonce, nonce), "email": f.email, "email_verified": f.verified, "name": "林晚"}
	if f.tid != "" {
		claims["tid"] = f.tid
	}
	if f.hs256 != "" {
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(f.hs256))
		return s
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	key := f.key
	if f.signWith != nil {
		key = f.signWith
	}
	s, err := tok.SignedString(key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// authorize plays the provider's consent page: it checks the request and issues a code.
func (f *fakeIdP) authorize(t *testing.T, authURL string) (state, code string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != "client-1" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") == "" {
		t.Fatalf("authorization request is not a PKCE code request: %s", authURL)
	}
	code = "code-" + q.Get("state")[:8]
	f.mu.Lock()
	f.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri")}
	f.mu.Unlock()
	return q.Get("state"), code
}

func oauthTestApp(t *testing.T) (*App, *fakeIdP) {
	a, _ := testApp(t)
	a.Cfg.SecretKey = make([]byte, 32)
	a.Cfg.PublicURL = "https://books.example.com"
	a.seedLegal()
	idp := newFakeIdP(t)
	oidcCacheMu.Lock()
	oidcDocs, oidcKeys = map[string]cached[*oidcDiscovery]{}, map[string]cached[[]jwk]{}
	oidcCacheMu.Unlock()
	return a, idp
}

func setProviders(t *testing.T, a *App, list ...OAuthProvider) {
	t.Helper()
	for i := range list {
		list[i].SecretEnc = a.Encrypt("s3cret")
	}
	if err := a.ValidateOAuthProviders(list); err != nil {
		t.Fatal(err)
	}
	a.SetSetting("oauth", OAuthSettings{Providers: list})
}

func login(t *testing.T, a *App, idp *fakeIdP, id string, agree bool, bindUser, session string) (*OAuthResult, error) {
	t.Helper()
	authURL, cookie, err := a.OAuthBegin(context.Background(), id, "/books", bindUser, agree)
	if err != nil {
		t.Fatal(err)
	}
	state, code := idp.authorize(t, authURL)
	return a.OAuthFinish(context.Background(), id, url.Values{"state": {state}, "code": {code}}, cookie, session, "test", "127.0.0.1")
}

func TestOIDCSignInSignUpAndLink(t *testing.T) {
	a, idp := oauthTestApp(t)
	setProviders(t, a, OAuthProvider{ID: "corp", Preset: "oidc", Name: "企业账号", Enabled: true, ClientID: "client-1", Issuer: idp.srv.URL, AllowSignup: true})

	authURL, _, err := a.OAuthBegin(context.Background(), "corp", "/books", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authURL, idp.srv.URL+"/authorize?") || !strings.Contains(authURL, "nonce=") || !strings.Contains(authURL, url.QueryEscape("https://books.example.com/api/v1/auth/oauth/corp/callback")) {
		t.Fatalf("auth url %s", authURL)
	}

	// sign-up needs the agreements
	if _, err := login(t, a, idp, "corp", false, "", ""); err == nil || !strings.Contains(err.Error(), "同意") {
		t.Fatalf("sign-up without consent: %v", err)
	}
	res, err := login(t, a, idp, "corp", true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Pair == nil || res.User.Email == nil || *res.User.Email != "lin@example.com" || !res.User.EmailVerified || res.User.Nickname != "林晚" {
		t.Fatalf("new account: %+v", res.User)
	}
	if res.User.LegalVersion == "" || res.User.LegalAgreedAt == nil {
		t.Fatal("consent must be recorded for accounts created by OAuth")
	}
	// the next sign-in finds the same account
	res2, err := login(t, a, idp, "corp", false, "", "")
	if err != nil || res2.Created || res2.User.ID != res.User.ID || res2.Redirect != "/books" {
		t.Fatalf("second sign-in: %+v %v", res2, err)
	}

	// a different provider account whose email belongs to an existing user does not take it over
	idp.sub, idp.email = "u-2002", "lin@example.com"
	if _, err := login(t, a, idp, "corp", true, "", ""); err == nil || !strings.Contains(err.Error(), "已经注册过") {
		t.Fatalf("email collision: %v", err)
	}
	var n int64
	a.DB.Model(&model.OAuthIdentity{}).Where("subject = ?", "u-2002").Count(&n)
	if n != 0 {
		t.Fatal("the identity must not be linked by email")
	}

	// linking from account settings needs the same signed-in account in this browser
	var other model.User
	a.DB.Where("id <> ?", res.User.ID).First(&other)
	if _, err := login(t, a, idp, "corp", false, other.ID, ""); err == nil {
		t.Fatal("linking without a session must fail")
	}
	bound, err := login(t, a, idp, "corp", false, other.ID, other.ID)
	if err != nil || !bound.Bound || bound.Pair != nil {
		t.Fatalf("link: %+v %v", bound, err)
	}
	if _, err := login(t, a, idp, "corp", false, other.ID, other.ID); err != nil {
		t.Fatalf("linking the same account again is harmless: %v", err)
	}
	idp.sub = "u-1001" // already linked to the first account
	if _, err := login(t, a, idp, "corp", false, other.ID, other.ID); err == nil || !strings.Contains(err.Error(), "另一个用户") {
		t.Fatalf("stealing a linked identity: %v", err)
	}

	// providers that do not allow sign-up only sign in linked accounts
	setProviders(t, a, OAuthProvider{ID: "corp", Preset: "oidc", Enabled: true, ClientID: "client-1", Issuer: idp.srv.URL})
	idp.sub, idp.email = "u-3003", "new@example.com"
	if _, err := login(t, a, idp, "corp", true, "", ""); err == nil || !strings.Contains(err.Error(), "还没有绑定") {
		t.Fatalf("sign-up disabled: %v", err)
	}
}

func TestOAuthFlowIsBoundToTheBrowser(t *testing.T) {
	a, idp := oauthTestApp(t)
	setProviders(t, a, OAuthProvider{ID: "corp", Preset: "oidc", Enabled: true, ClientID: "client-1", Issuer: idp.srv.URL, AllowSignup: true})
	authURL, cookie, _ := a.OAuthBegin(context.Background(), "corp", "/", "", true)
	state, code := idp.authorize(t, authURL)
	_, otherCookie, _ := a.OAuthBegin(context.Background(), "corp", "/", "", true)
	for name, c := range map[string]struct{ cookie, state string }{
		"no cookie":           {"", state},
		"another flow":        {otherCookie, state},
		"tampered cookie":     {cookie[:len(cookie)-4] + "AAAA", state},
		"forged state":        {cookie, "x" + state},
		"other provider path": {cookie, state},
	} {
		id := "corp"
		if name == "other provider path" {
			id = "gh"
		}
		if _, err := a.OAuthFinish(context.Background(), id, url.Values{"state": {c.state}, "code": {code}}, c.cookie, "", "", ""); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
	// the provider reporting a refusal
	if _, err := a.OAuthFinish(context.Background(), "corp", url.Values{"state": {state}, "error": {"access_denied"}}, cookie, "", "", ""); err == nil || !strings.Contains(err.Error(), "取消") {
		t.Errorf("access_denied: %v", err)
	}
	// an expired flow
	at(t, time.Now().Add(OAuthFlowTTL+time.Minute))
	if _, err := a.OAuthFinish(context.Background(), "corp", url.Values{"state": {state}, "code": {code}}, cookie, "", "", ""); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Errorf("expired: %v", err)
	}
}

func TestIDTokenChecks(t *testing.T) {
	a, idp := oauthTestApp(t)
	setProviders(t, a, OAuthProvider{ID: "corp", Preset: "oidc", Enabled: true, ClientID: "client-1", Issuer: idp.srv.URL, AllowSignup: true})
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, breakIt := range map[string]func(){
		"nonce":     func() { idp.nonce = "replayed" },
		"audience":  func() { idp.aud = "someone-else" },
		"issuer":    func() { idp.iss = "https://evil.example.com" },
		"expired":   func() { idp.exp = -10 * time.Minute },
		"signature": func() { idp.signWith = other },
	} {
		idp.nonce, idp.aud, idp.iss, idp.exp, idp.signWith = "", "", "", time.Hour, nil
		breakIt()
		if _, err := login(t, a, idp, "corp", true, "", ""); err == nil || !strings.Contains(err.Error(), "令牌") {
			t.Errorf("%s: want an ID token error, got %v", name, err)
		}
	}
	idp.nonce, idp.aud, idp.iss, idp.exp, idp.signWith = "", "", "", time.Hour, nil

	// HS256 ID tokens are MACed with the client secret (OIDC Core §10.1)
	idp.hs256 = "s3cret"
	if _, err := login(t, a, idp, "corp", true, "", ""); err != nil {
		t.Errorf("HS256 with the client secret: %v", err)
	}
	idp.hs256, idp.sub = "wrong-secret", "u-9"
	if _, err := login(t, a, idp, "corp", true, "", ""); err == nil {
		t.Error("HS256 with another secret must fail")
	}
	idp.hs256 = ""

	// unverified emails are not attached to the new account
	idp.sub, idp.email, idp.verified = "u-4004", "maybe@example.com", false
	res, err := login(t, a, idp, "corp", true, "", "")
	if err != nil || res.User.Email != nil {
		t.Fatalf("unverified email: %+v %v", res, err)
	}
}

func TestMicrosoftTenantIssuer(t *testing.T) {
	a, idp := oauthTestApp(t)
	idp.issuer = idp.srv.URL + "/{tenantid}/v2.0"
	idp.tid = "9188040d-6c67-4c5b-b112-36a304b66dad"
	setProviders(t, a, OAuthProvider{ID: "ms", Preset: "microsoft", Enabled: true, ClientID: "client-1", Issuer: idp.srv.URL, AllowSignup: true})
	if _, err := login(t, a, idp, "ms", true, "", ""); err != nil {
		t.Fatalf("tenant issuer: %v", err)
	}
	idp.tid, idp.sub = "", "u-5"
	if _, err := login(t, a, idp, "ms", true, "", ""); err == nil {
		t.Fatal("a token without its tenant cannot match the template")
	}
}

func TestPlainOAuth2Provider(t *testing.T) {
	a, idp := oauthTestApp(t)
	setProviders(t, a, OAuthProvider{ID: "gh", Preset: "github", Enabled: true, ClientID: "client-1", AuthURL: idp.srv.URL + "/authorize",
		TokenURL: idp.srv.URL + "/token?plain=1", UserInfoURL: idp.srv.URL + "/api/user", AllowSignup: true})
	// the preset's emails endpoint is GitHub's; point it at the fake for the test
	for i := range OAuthPresets {
		if OAuthPresets[i].Key == "github" {
			old := OAuthPresets[i].EmailsURL
			OAuthPresets[i].EmailsURL = idp.srv.URL + "/api/user/emails"
			t.Cleanup(func() { OAuthPresets[i].EmailsURL = old })
		}
	}
	authURL, _, _ := a.OAuthBegin(context.Background(), "gh", "/", "", true)
	if !strings.Contains(authURL, "scope=read%3Auser+user%3Aemail") || strings.Contains(authURL, "nonce=") {
		t.Fatalf("auth url %s", authURL)
	}
	res, err := login(t, a, idp, "gh", true, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// the verified primary email, not the public profile email; the login when there is no name
	if res.User.Email == nil || *res.User.Email != "lw@example.com" || res.User.Nickname != "linwan" {
		t.Fatalf("profile: email=%v nick=%q", res.User.Email, res.User.Nickname)
	}
	var ident model.OAuthIdentity
	a.DB.First(&ident, "provider = ?", "gh")
	if ident.Subject != "583231" || ident.Avatar != "https://img.example.com/gh.png" {
		t.Fatalf("identity %+v", ident)
	}
}

func TestOAuthSettingsValidation(t *testing.T) {
	a, _ := oauthTestApp(t)
	sec := a.Encrypt("x")
	for name, p := range map[string]OAuthProvider{
		"bad id":         {ID: "Google!", Preset: "google", Enabled: true, ClientID: "c", SecretEnc: sec},
		"no secret":      {ID: "google", Preset: "google", Enabled: true, ClientID: "c"},
		"no issuer":      {ID: "corp", Preset: "oidc", Enabled: true, ClientID: "c", SecretEnc: sec},
		"http issuer":    {ID: "corp", Preset: "oidc", Enabled: true, ClientID: "c", SecretEnc: sec, Issuer: "http://idp.example.com"},
		"oauth2 no urls": {ID: "x", Preset: "oauth2", Enabled: true, ClientID: "c", SecretEnc: sec},
	} {
		if err := a.ValidateOAuthProviders([]OAuthProvider{p}); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	if err := a.ValidateOAuthProviders([]OAuthProvider{{ID: "google", Preset: "google", Enabled: true, ClientID: "c", SecretEnc: sec}, {ID: "github", Preset: "github"}}); err != nil {
		t.Errorf("presets need only the client: %v", err)
	}
	// the first version's two slots are read as providers
	a.SetSetting("oauth", map[string]any{"google": map[string]any{"enabled": true, "clientId": "g-1", "secretEnc": sec}, "wechat": map[string]any{"enabled": false, "clientId": ""}})
	if list := a.OAuthConfig(); len(list) != 1 || list[0].ID != "google" || list[0].ClientID != "g-1" {
		t.Fatalf("legacy settings: %+v", list)
	}
}
