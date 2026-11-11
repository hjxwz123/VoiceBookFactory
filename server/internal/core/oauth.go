package core

// Third-party sign-in over the standard protocols:
//   - OAuth 2.0 authorization code flow (RFC 6749 §4.1) with PKCE S256 (RFC 7636);
//   - state and the PKCE verifier kept in an encrypted, HttpOnly cookie, so the callback only completes
//     in the browser that started it (login CSRF protection, RFC 9700 §2.1);
//   - OpenID Connect providers: endpoints from discovery (OIDC Discovery 1.0), and an ID token whose
//     signature is checked against the provider's JWKS, with issuer, audience, azp, expiry and nonce
//     (OIDC Core §3.1.3.7); userinfo must name the same subject (§5.3.2).
// WeChat's website login follows the same code flow with its own parameter names and is kept as a preset.

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tidwall/gjson"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

// OAuthProvider is one sign-in option configured in the admin console.
type OAuthProvider struct {
	ID          string `json:"id"`     // in URLs: /api/v1/auth/oauth/{id}/callback
	Preset      string `json:"preset"` // github | google | microsoft | gitlab | gitee | wechat | oidc | oauth2
	Name        string `json:"name"`   // button label
	Enabled     bool   `json:"enabled"`
	ClientID    string `json:"clientId"`
	SecretEnc   string `json:"secretEnc"`
	Issuer      string `json:"issuer"`  // OpenID Connect
	AuthURL     string `json:"authUrl"` // plain OAuth 2.0 (or overrides)
	TokenURL    string `json:"tokenUrl"`
	UserInfoURL string `json:"userInfoUrl"`
	Scopes      string `json:"scopes"`
	AuthMethod  string `json:"authMethod"` // client_secret_basic | client_secret_post; "" = discovery or preset
	FieldID     string `json:"fieldId"`    // userinfo JSON paths for plain OAuth 2.0; "a,b" tries a then b
	FieldEmail  string `json:"fieldEmail"`
	FieldName   string `json:"fieldName"`
	FieldAvatar string `json:"fieldAvatar"`
	AllowSignup bool   `json:"allowSignup"` // someone without an account can create one with it
}

type legacyOAuthApp struct {
	Enabled   bool   `json:"enabled"`
	ClientID  string `json:"clientId"`
	SecretEnc string `json:"secretEnc"`
}

type OAuthSettings struct {
	Providers []OAuthProvider `json:"providers"`
	// the first version had two fixed slots; they are read as providers
	Google *legacyOAuthApp `json:"google,omitempty"`
	WeChat *legacyOAuthApp `json:"wechat,omitempty"`
}

// OAuthPreset fills in what a well-known provider publishes, so the operator only enters the client.
type OAuthPreset struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"` // oidc | oauth2 | wechat
	Issuer       string `json:"issuer,omitempty"`
	AuthURL      string `json:"authUrl,omitempty"`
	TokenURL     string `json:"tokenUrl,omitempty"`
	UserInfoURL  string `json:"userInfoUrl,omitempty"`
	EmailsURL    string `json:"-"`
	TokenInQuery bool   `json:"-"` // the API wants ?access_token= instead of a bearer header
	Scopes       string `json:"scopes,omitempty"`
	AuthMethod   string `json:"authMethod,omitempty"`
	FieldID      string `json:"fieldId,omitempty"`
	FieldEmail   string `json:"fieldEmail,omitempty"`
	FieldName    string `json:"fieldName,omitempty"`
	FieldAvatar  string `json:"fieldAvatar,omitempty"`
	Console      string `json:"console"` // where the operator registers the app
	Extra        string `json:"-"`       // extra authorization parameters
}

var OAuthPresets = []OAuthPreset{
	{Key: "github", Name: "GitHub", Protocol: "oauth2", AuthURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token",
		UserInfoURL: "https://api.github.com/user", EmailsURL: "https://api.github.com/user/emails", Scopes: "read:user user:email", AuthMethod: "client_secret_post",
		FieldID: "id", FieldEmail: "email", FieldName: "name,login", FieldAvatar: "avatar_url", Console: "GitHub → Settings → Developer settings → OAuth Apps → New OAuth App"},
	{Key: "google", Name: "Google", Protocol: "oidc", Issuer: "https://accounts.google.com", Scopes: "openid email profile", Extra: "prompt=select_account",
		Console: "Google Cloud Console → API 和服务 → 凭据 → 创建 OAuth 客户端 ID（Web 应用）"},
	{Key: "microsoft", Name: "Microsoft", Protocol: "oidc", Issuer: "https://login.microsoftonline.com/common/v2.0", Scopes: "openid email profile", Extra: "prompt=select_account",
		Console: "Microsoft Entra 管理中心 → 应用注册 → 新注册（重定向 URI 选 Web）。只允许本组织登录时，把签发方里的 common 换成租户 ID"},
	{Key: "gitlab", Name: "GitLab", Protocol: "oidc", Issuer: "https://gitlab.com", Scopes: "openid email profile",
		Console: "GitLab → 偏好设置 → 应用 → 添加新应用（勾选 openid、email、profile）。自建 GitLab 请把签发方改成你的地址"},
	{Key: "gitee", Name: "Gitee", Protocol: "oauth2", AuthURL: "https://gitee.com/oauth/authorize", TokenURL: "https://gitee.com/oauth/token",
		UserInfoURL: "https://gitee.com/api/v5/user", EmailsURL: "https://gitee.com/api/v5/emails", TokenInQuery: true, Scopes: "user_info emails", AuthMethod: "client_secret_post",
		FieldID: "id", FieldEmail: "email", FieldName: "name,login", FieldAvatar: "avatar_url", Console: "Gitee → 设置 → 第三方应用 → 创建应用（勾选 user_info 和 emails）"},
	{Key: "wechat", Name: "微信", Protocol: "wechat", AuthURL: "https://open.weixin.qq.com/connect/qrconnect", TokenURL: "https://api.weixin.qq.com/sns/oauth2/access_token",
		UserInfoURL: "https://api.weixin.qq.com/sns/userinfo", Scopes: "snsapi_login", Console: "微信开放平台 → 管理中心 → 网站应用（授权回调域填写本站域名）"},
	{Key: "oidc", Name: "OpenID Connect", Protocol: "oidc", Scopes: "openid email profile",
		Console: "任何支持 OpenID Connect 的身份服务，例如 Authing、Logto、Keycloak、Casdoor、阿里云 IDaaS。填写签发方（Issuer）地址即可自动发现其余地址"},
	{Key: "oauth2", Name: "OAuth 2.0", Protocol: "oauth2", AuthMethod: "client_secret_basic", FieldID: "sub,id", FieldEmail: "email", FieldName: "name,nickname,preferred_username,login,username", FieldAvatar: "picture,avatar_url,avatar",
		Console: "只支持 OAuth 2.0（没有 OpenID Connect）的服务：填写授权地址、令牌地址、用户信息地址，以及用户信息里各字段的位置"},
}

func oauthPreset(key string) OAuthPreset {
	for _, p := range OAuthPresets {
		if p.Key == key {
			return p
		}
	}
	return OAuthPresets[len(OAuthPresets)-1]
}

var oauthIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,15}$`)

// OAuthConfig returns the configured providers (old two-slot settings read as providers).
func (a *App) OAuthConfig() []OAuthProvider {
	var s OAuthSettings
	a.Setting("oauth", &s)
	out := slices.Clone(s.Providers)
	if len(out) == 0 {
		if g := s.Google; g != nil && g.ClientID != "" {
			out = append(out, OAuthProvider{ID: "google", Preset: "google", Name: "Google", Enabled: g.Enabled, ClientID: g.ClientID, SecretEnc: g.SecretEnc})
		}
		if w := s.WeChat; w != nil && w.ClientID != "" {
			out = append(out, OAuthProvider{ID: "wechat", Preset: "wechat", Name: "微信", Enabled: w.Enabled, ClientID: w.ClientID, SecretEnc: w.SecretEnc})
		}
	}
	return out
}

func (a *App) oauthProvider(id string) (OAuthProvider, OAuthPreset, error) {
	for _, p := range a.OAuthConfig() {
		if p.ID == id {
			if !p.Enabled || p.ClientID == "" {
				return p, OAuthPreset{}, Bad("这个登录方式没有开通")
			}
			return p, p.effective(), nil
		}
	}
	return OAuthProvider{}, OAuthPreset{}, NotFound("登录方式")
}

// effective merges the provider's own values over its preset.
func (p OAuthProvider) effective() OAuthPreset {
	e := oauthPreset(p.Preset)
	pick := func(own, def string) string {
		if v := strings.TrimSpace(own); v != "" {
			return v
		}
		return def
	}
	e.Issuer = strings.TrimRight(pick(p.Issuer, e.Issuer), "/")
	e.AuthURL, e.TokenURL, e.UserInfoURL = pick(p.AuthURL, e.AuthURL), pick(p.TokenURL, e.TokenURL), pick(p.UserInfoURL, e.UserInfoURL)
	e.Scopes, e.AuthMethod = pick(p.Scopes, e.Scopes), pick(p.AuthMethod, e.AuthMethod)
	e.FieldID, e.FieldEmail, e.FieldName, e.FieldAvatar = pick(p.FieldID, e.FieldID), pick(p.FieldEmail, e.FieldEmail), pick(p.FieldName, e.FieldName), pick(p.FieldAvatar, e.FieldAvatar)
	e.Name = pick(p.Name, e.Name)
	return e
}

// OAuthProviders lists the sign-in buttons for the login page.
func (a *App) OAuthProviders() []map[string]any {
	out := []map[string]any{}
	for _, p := range a.OAuthConfig() {
		if p.Enabled && p.ClientID != "" && p.SecretEnc != "" {
			out = append(out, map[string]any{"id": p.ID, "name": p.effective().Name, "preset": p.Preset, "signup": p.AllowSignup})
		}
	}
	return out
}

func (a *App) OAuthProviderName(id string) string {
	for _, p := range a.OAuthConfig() {
		if p.ID == id {
			return p.effective().Name
		}
	}
	return id
}

// OAuthCallbackURL is the redirect URI to register with the provider.
func (a *App) OAuthCallbackURL(id string) string {
	return a.Cfg.PublicURL + "/api/v1/auth/oauth/" + id + "/callback"
}

// checkEndpoint refuses provider endpoints that are not HTTPS (plain HTTP only for this machine, for testing).
func (a *App) checkEndpoint(label, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Bad("%s不是有效的网址：%s", label, raw)
	}
	if u.Scheme == "http" {
		h := u.Hostname()
		if ip := net.ParseIP(h); h != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return Bad("%s必须使用 https：%s", label, raw)
		}
	}
	return nil
}

// ValidateOAuthProviders checks the admin form before it is saved.
func (a *App) ValidateOAuthProviders(list []OAuthProvider) error {
	seen := map[string]bool{}
	for _, p := range list {
		name := p.effective().Name
		if !oauthIDRe.MatchString(p.ID) {
			return Bad("「%s」的标识只能用小写字母、数字和短横线，2~16 个字符，以字母开头", name)
		}
		if seen[p.ID] {
			return Bad("标识 %s 重复了", p.ID)
		}
		seen[p.ID] = true
		if !p.Enabled {
			continue
		}
		if strings.TrimSpace(p.ClientID) == "" || p.SecretEnc == "" {
			return Bad("「%s」要填写 Client ID 和 Client Secret 才能启用", name)
		}
		e := p.effective()
		switch e.Protocol {
		case "oidc":
			if e.Issuer == "" {
				return Bad("「%s」要填写签发方（Issuer）地址", name)
			}
			if err := a.checkEndpoint("签发方地址", e.Issuer); err != nil {
				return err
			}
		case "oauth2":
			for _, f := range [][2]string{{"授权地址", e.AuthURL}, {"令牌地址", e.TokenURL}, {"用户信息地址", e.UserInfoURL}} {
				if f[1] == "" {
					return Bad("「%s」要填写%s", name, f[0])
				}
				if err := a.checkEndpoint(f[0], f[1]); err != nil {
					return err
				}
			}
			if e.FieldID == "" {
				return Bad("「%s」要填写用户 ID 字段", name)
			}
		}
	}
	return nil
}

// ---------- discovery and keys ----------

type oidcDiscovery struct {
	Issuer           string   `json:"issuer"`
	AuthEndpoint     string   `json:"authorization_endpoint"`
	TokenEndpoint    string   `json:"token_endpoint"`
	UserInfoEndpoint string   `json:"userinfo_endpoint"`
	JWKSURI          string   `json:"jwks_uri"`
	AuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
	PKCEMethods      []string `json:"code_challenge_methods_supported"`
	SigningAlgs      []string `json:"id_token_signing_alg_values_supported"`
}

type cached[T any] struct {
	v  T
	at time.Time
}

var (
	oidcCacheMu sync.Mutex
	oidcDocs    = map[string]cached[*oidcDiscovery]{}
	oidcKeys    = map[string]cached[[]jwk]{}
	oauthHTTP   = &http.Client{Timeout: 15 * time.Second}
)

func oauthRequest(ctx context.Context, method, u string, form url.Values, bearer string) (*http.Request, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "voicebook-oauth/1.0")
	return req, nil
}

func oauthDo(req *http.Request) ([]byte, http.Header, error) {
	resp, err := oauthHTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("连不上 %s：%v", req.URL.Host, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
			Message     string `json:"message"`
		}
		json.Unmarshal(data, &e)
		if msg := firstNonEmpty(e.Description, e.Error, e.Message); msg != "" {
			return nil, nil, fmt.Errorf("%s 返回 HTTP %d：%s", req.URL.Host, resp.StatusCode, truncateStr(msg, 200))
		}
		return nil, nil, fmt.Errorf("%s 返回 HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return data, resp.Header, nil
}

// Discover reads an OpenID Connect provider's configuration (cached for an hour).
func (a *App) Discover(ctx context.Context, issuer string) (*oidcDiscovery, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if err := a.checkEndpoint("签发方地址", issuer); err != nil {
		return nil, err
	}
	oidcCacheMu.Lock()
	if c, ok := oidcDocs[issuer]; ok && time.Since(c.at) < time.Hour {
		oidcCacheMu.Unlock()
		return c.v, nil
	}
	oidcCacheMu.Unlock()
	req, err := oauthRequest(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil, "")
	if err != nil {
		return nil, err
	}
	data, _, err := oauthDo(req)
	if err != nil {
		return nil, fmt.Errorf("读取 OpenID Connect 配置失败：%v", err)
	}
	var d oidcDiscovery
	if err := json.Unmarshal(data, &d); err != nil || d.AuthEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, errors.New("签发方地址下没有有效的 OpenID Connect 配置（/.well-known/openid-configuration）")
	}
	// OIDC Discovery §4.3: the document's issuer must be the one asked for. Microsoft's shared
	// endpoints answer with a {tenantid} template, matched per token instead.
	if strings.TrimRight(d.Issuer, "/") != issuer && !strings.Contains(d.Issuer, "{tenantid}") {
		return nil, fmt.Errorf("配置里的签发方 %s 和填写的 %s 不一致", d.Issuer, issuer)
	}
	for _, u := range []string{d.AuthEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if err := a.checkEndpoint("OpenID Connect 配置里的地址", u); err != nil {
			return nil, err
		}
	}
	oidcCacheMu.Lock()
	oidcDocs[issuer] = cached[*oidcDiscovery]{&d, time.Now()}
	oidcCacheMu.Unlock()
	return &d, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (a *App) jwks(ctx context.Context, uri string, refresh bool) ([]jwk, error) {
	oidcCacheMu.Lock()
	c, ok := oidcKeys[uri]
	oidcCacheMu.Unlock()
	// a key we do not know triggers a refresh (key rotation), at most once a minute
	if ok && time.Since(c.at) < time.Hour && (!refresh || time.Since(c.at) < time.Minute) {
		return c.v, nil
	}
	req, err := oauthRequest(ctx, http.MethodGet, uri, nil, "")
	if err != nil {
		return nil, err
	}
	data, _, err := oauthDo(req)
	if err != nil {
		return nil, fmt.Errorf("读取签名公钥失败：%v", err)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, errors.New("签名公钥格式不正确")
	}
	oidcCacheMu.Lock()
	oidcKeys[uri] = cached[[]jwk]{set.Keys, time.Now()}
	oidcCacheMu.Unlock()
	return set.Keys, nil
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func (k jwk) public() (any, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64int(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64int(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		curve := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[k.Crv]
		if curve == nil {
			return nil, fmt.Errorf("unsupported curve %s", k.Crv)
		}
		x, err := b64int(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64int(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported curve %s", k.Crv)
		}
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.X, "="))
		if err != nil {
			return nil, err
		}
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("unsupported key type %s", k.Kty)
}

func ktyFor(alg string) string {
	switch {
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		return "RSA"
	case strings.HasPrefix(alg, "ES"):
		return "EC"
	case alg == "EdDSA":
		return "OKP"
	}
	return ""
}

var idTokenAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA", "HS256", "HS384", "HS512"}

// verifyIDToken checks an ID token's signature and claims (OIDC Core §3.1.3.7).
func (a *App) verifyIDToken(ctx context.Context, raw string, d *oidcDiscovery, clientID, secret, nonce string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		alg, _ := t.Header["alg"].(string)
		if strings.HasPrefix(alg, "HS") {
			return []byte(secret), nil // OIDC Core §10.1: MAC with the client secret
		}
		kid, _ := t.Header["kid"].(string)
		for _, refresh := range []bool{false, true} {
			keys, err := a.jwks(ctx, d.JWKSURI, refresh)
			if err != nil {
				return nil, err
			}
			var match []jwk
			for _, k := range keys {
				if k.Kty == ktyFor(alg) && (k.Use == "" || k.Use == "sig") && (kid == "" || k.Kid == kid) {
					match = append(match, k)
				}
			}
			if len(match) == 1 || (len(match) > 1 && kid != "") {
				return match[0].public()
			}
		}
		return nil, fmt.Errorf("找不到签名公钥 %q", kid)
	}, jwt.WithValidMethods(idTokenAlgs), jwt.WithAudience(clientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(2*time.Minute))
	if err != nil {
		return nil, fmt.Errorf("身份令牌校验失败：%v", err)
	}
	want := strings.TrimRight(d.Issuer, "/")
	if strings.Contains(want, "{tenantid}") {
		tid, _ := claims["tid"].(string)
		if tid == "" {
			return nil, errors.New("身份令牌缺少租户信息")
		}
		want = strings.ReplaceAll(want, "{tenantid}", tid)
	}
	if iss, _ := claims["iss"].(string); strings.TrimRight(iss, "/") != want {
		return nil, fmt.Errorf("身份令牌的签发方 %q 不对", iss)
	}
	if aud, _ := claims.GetAudience(); len(aud) > 1 {
		if azp, _ := claims["azp"].(string); azp != clientID {
			return nil, errors.New("身份令牌不是签发给本站的")
		}
	}
	if n, _ := claims["nonce"].(string); subtle.ConstantTimeCompare([]byte(n), []byte(nonce)) != 1 {
		return nil, errors.New("身份令牌和这次登录对不上（nonce 不一致）")
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return nil, errors.New("身份令牌缺少用户标识")
	}
	return claims, nil
}

// ---------- the flow ----------

// oauthFlow is what the browser carries between start and callback, encrypted and authenticated.
type oauthFlow struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
	Redirect string `json:"r"`
	BindUser string `json:"b,omitempty"`
	Agree    bool   `json:"a,omitempty"`
	Exp      int64  `json:"e"`
}

const OAuthFlowTTL = 10 * time.Minute

func safeRedirect(r string) string {
	if !strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") || strings.HasPrefix(r, "/\\") {
		return "/"
	}
	return r
}

// OAuthBegin starts a sign-in (or, with bindUserID, links a provider to that account). It returns the
// provider's authorization URL and the value of the flow cookie.
func (a *App) OAuthBegin(ctx context.Context, id, redirect, bindUserID string, agree bool) (string, string, error) {
	p, e, err := a.oauthProvider(id)
	if err != nil {
		return "", "", err
	}
	f := oauthFlow{Provider: id, State: secure.Token(24), Verifier: secure.Token(48), Nonce: secure.Token(24), Redirect: safeRedirect(redirect), BindUser: bindUserID, Agree: agree, Exp: now().Add(OAuthFlowTTL).Unix()}
	q := url.Values{}
	var endpoint string
	switch e.Protocol {
	case "wechat":
		endpoint = e.AuthURL
		q.Set("appid", p.ClientID)
		q.Set("redirect_uri", a.OAuthCallbackURL(id))
		q.Set("response_type", "code")
		q.Set("scope", e.Scopes)
		q.Set("state", f.State)
	default:
		endpoint = e.AuthURL
		if e.Protocol == "oidc" {
			d, err := a.Discover(ctx, e.Issuer)
			if err != nil {
				return "", "", Bad("%v", err)
			}
			endpoint = d.AuthEndpoint
			q.Set("nonce", f.Nonce)
		}
		q.Set("response_type", "code")
		q.Set("client_id", p.ClientID)
		q.Set("redirect_uri", a.OAuthCallbackURL(id))
		if e.Scopes != "" {
			q.Set("scope", e.Scopes)
		}
		q.Set("state", f.State)
		sum := sha256.Sum256([]byte(f.Verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
		if e.Extra != "" {
			extra, _ := url.ParseQuery(e.Extra)
			for k, v := range extra {
				q[k] = v
			}
		}
	}
	b, _ := json.Marshal(f)
	cookie, err := secure.Encrypt(a.Cfg.SecretKey, string(b))
	if err != nil {
		return "", "", err
	}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	u := endpoint + sep + q.Encode()
	if e.Protocol == "wechat" {
		u += "#wechat_redirect"
	}
	return u, cookie, nil
}

type oauthProfile struct {
	Subject, Email, Name, Avatar string
	EmailVerified                bool
}

// OAuthResult is the outcome of a callback.
type OAuthResult struct {
	User     *model.User
	Pair     *TokenPair // nil when an account was linked rather than signed in
	Redirect string
	Bound    bool
	Created  bool
}

// OAuthFlowRedirect reads where a flow cookie wanted to return to, so errors land on the right page.
func (a *App) OAuthFlowRedirect(cookie string) (redirect string, bind bool) {
	plain, err := secure.Decrypt(a.Cfg.SecretKey, cookie)
	var f oauthFlow
	if err != nil || json.Unmarshal([]byte(plain), &f) != nil {
		return "/", false
	}
	return f.Redirect, f.BindUser != ""
}

// OAuthFinish completes the flow from the provider's redirect. sessionUserID is the account signed in
// in this browser (needed to finish linking).
func (a *App) OAuthFinish(ctx context.Context, id string, q url.Values, cookie, sessionUserID, ua, ip string) (*OAuthResult, error) {
	plain, err := secure.Decrypt(a.Cfg.SecretKey, cookie)
	var f oauthFlow
	if cookie == "" || err != nil || json.Unmarshal([]byte(plain), &f) != nil || f.Provider != id || now().Unix() > f.Exp {
		return nil, Bad("登录请求已过期，请回到登录页重新发起")
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		return nil, Bad("登录请求和这个浏览器对不上，请回到登录页重新发起")
	}
	p, e, err := a.oauthProvider(id)
	if err != nil {
		return nil, err
	}
	if errCode := q.Get("error"); errCode != "" {
		if errCode == "access_denied" {
			return nil, Bad("你取消了 %s 授权", e.Name)
		}
		return nil, Bad("%s 授权失败：%s", e.Name, firstNonEmpty(q.Get("error_description"), errCode))
	}
	code := q.Get("code")
	if code == "" {
		return nil, Bad("%s 没有返回授权码", e.Name)
	}
	prof, err := a.oauthProfile(ctx, p, e, code, f)
	if err != nil {
		return nil, Bad("%s 登录失败：%v", e.Name, err)
	}
	return a.oauthAccount(p, e, prof, f, sessionUserID, ua, ip)
}

func (a *App) exchangeCode(ctx context.Context, p OAuthProvider, e OAuthPreset, tokenURL, method, code string, f oauthFlow) (map[string]any, error) {
	secret := a.Decrypt(p.SecretEnc)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {a.OAuthCallbackURL(p.ID)}, "code_verifier": {f.Verifier}}
	if method != "client_secret_basic" {
		form.Set("client_id", p.ClientID)
		form.Set("client_secret", secret)
	}
	req, err := oauthRequest(ctx, http.MethodPost, tokenURL, form, "")
	if err != nil {
		return nil, err
	}
	if method == "client_secret_basic" {
		// RFC 6749 §2.3.1: both parts form-encoded before Basic encoding
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(secret))
	}
	data, hdr, err := oauthDo(req)
	if err != nil {
		return nil, err
	}
	tok := map[string]any{}
	if strings.Contains(hdr.Get("Content-Type"), "x-www-form-urlencoded") {
		v, _ := url.ParseQuery(string(data))
		for k := range v {
			tok[k] = v.Get(k)
		}
	} else if err := json.Unmarshal(data, &tok); err != nil {
		return nil, errors.New("令牌接口返回的内容无法识别")
	}
	if msg, _ := tok["error"].(string); msg != "" {
		desc, _ := tok["error_description"].(string)
		return nil, fmt.Errorf("换取令牌失败：%s", firstNonEmpty(desc, msg))
	}
	if at, _ := tok["access_token"].(string); at == "" {
		return nil, errors.New("令牌接口没有返回 access_token")
	}
	return tok, nil
}

func (a *App) apiGet(ctx context.Context, e OAuthPreset, u, token string) ([]byte, error) {
	bearer := token
	if e.TokenInQuery {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "access_token=" + url.QueryEscape(token)
		bearer = ""
	}
	req, err := oauthRequest(ctx, http.MethodGet, u, nil, bearer)
	if err != nil {
		return nil, err
	}
	data, _, err := oauthDo(req)
	return data, err
}

// field reads the first non-empty of comma-separated JSON paths.
func field(doc []byte, paths string) string {
	for _, p := range strings.Split(paths, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if v := gjson.GetBytes(doc, p); v.Exists() && v.Type != gjson.Null {
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

func claimString(c jwt.MapClaims, keys ...string) string {
	for _, k := range keys {
		if s, ok := c[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func claimTrue(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	}
	return false
}

func (a *App) oauthProfile(ctx context.Context, p OAuthProvider, e OAuthPreset, code string, f oauthFlow) (*oauthProfile, error) {
	switch e.Protocol {
	case "wechat":
		return a.wechatProfile(ctx, p, e, code)
	case "oidc":
		d, err := a.Discover(ctx, e.Issuer)
		if err != nil {
			return nil, err
		}
		method := p.AuthMethod
		if method == "" {
			method = "client_secret_basic" // the default when discovery lists nothing (OIDC Discovery §3)
			if len(d.AuthMethods) > 0 && !slices.Contains(d.AuthMethods, "client_secret_basic") && slices.Contains(d.AuthMethods, "client_secret_post") {
				method = "client_secret_post"
			}
		}
		tok, err := a.exchangeCode(ctx, p, e, d.TokenEndpoint, method, code, f)
		if err != nil {
			return nil, err
		}
		raw, _ := tok["id_token"].(string)
		if raw == "" {
			return nil, errors.New("没有返回身份令牌（id_token），请确认授权范围里有 openid")
		}
		c, err := a.verifyIDToken(ctx, raw, d, p.ClientID, a.Decrypt(p.SecretEnc), f.Nonce)
		if err != nil {
			return nil, err
		}
		prof := &oauthProfile{Subject: claimString(c, "sub"), Email: claimString(c, "email"), EmailVerified: claimTrue(c["email_verified"]),
			Name: claimString(c, "name", "nickname", "preferred_username"), Avatar: claimString(c, "picture")}
		if d.UserInfoEndpoint != "" && (prof.Email == "" || prof.Name == "") {
			at, _ := tok["access_token"].(string)
			if doc, err := a.apiGet(ctx, e, d.UserInfoEndpoint, at); err == nil && gjson.ValidBytes(doc) {
				if field(doc, "sub") == prof.Subject { // OIDC Core §5.3.2
					if prof.Email == "" {
						prof.Email, prof.EmailVerified = field(doc, "email"), gjson.GetBytes(doc, "email_verified").Bool() || gjson.GetBytes(doc, "email_verified").String() == "true"
					}
					prof.Name = firstNonEmpty(prof.Name, field(doc, "name,nickname,preferred_username"))
					prof.Avatar = firstNonEmpty(prof.Avatar, field(doc, "picture"))
				}
			}
		}
		return prof, nil
	default:
		tok, err := a.exchangeCode(ctx, p, e, e.TokenURL, e.AuthMethod, code, f)
		if err != nil {
			return nil, err
		}
		at, _ := tok["access_token"].(string)
		doc, err := a.apiGet(ctx, e, e.UserInfoURL, at)
		if err != nil {
			return nil, fmt.Errorf("读取用户信息失败：%v", err)
		}
		prof := &oauthProfile{Subject: field(doc, e.FieldID), Email: field(doc, e.FieldEmail), Name: field(doc, e.FieldName), Avatar: field(doc, e.FieldAvatar)}
		if prof.Subject == "" {
			return nil, fmt.Errorf("用户信息里找不到用户 ID（字段 %s）", e.FieldID)
		}
		if e.EmailsURL != "" {
			// the profile's public email is not necessarily verified; the email list says which are
			prof.Email = ""
			if list, err := a.apiGet(ctx, e, e.EmailsURL, at); err == nil {
				var best string
				gjson.ParseBytes(list).ForEach(func(_, v gjson.Result) bool {
					verified := v.Get("verified").Bool() || v.Get("state").String() == "confirmed"
					primary := v.Get("primary").Bool() || strings.Contains(v.Get("scope").Raw, `"primary"`) // GitHub | Gitee
					if verified && (best == "" || primary) {
						best = v.Get("email").String()
					}
					return !(verified && primary)
				})
				if best != "" {
					prof.Email, prof.EmailVerified = best, true
				}
			}
		}
		return prof, nil
	}
}

func (a *App) wechatProfile(ctx context.Context, p OAuthProvider, e OAuthPreset, code string) (*oauthProfile, error) {
	q := url.Values{"appid": {p.ClientID}, "secret": {a.Decrypt(p.SecretEnc)}, "code": {code}, "grant_type": {"authorization_code"}}
	req, err := oauthRequest(ctx, http.MethodGet, e.TokenURL+"?"+q.Encode(), nil, "")
	if err != nil {
		return nil, err
	}
	data, _, err := oauthDo(req)
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		OpenID      string `json:"openid"`
		UnionID     string `json:"unionid"`
		ErrMsg      string `json:"errmsg"`
	}
	json.Unmarshal(data, &tok)
	if tok.AccessToken == "" || tok.OpenID == "" {
		return nil, fmt.Errorf("换取令牌失败：%s", firstNonEmpty(tok.ErrMsg, "没有返回 access_token"))
	}
	info := url.Values{"access_token": {tok.AccessToken}, "openid": {tok.OpenID}}
	var u struct {
		Nickname   string `json:"nickname"`
		HeadImgURL string `json:"headimgurl"`
		UnionID    string `json:"unionid"`
	}
	if req, err := oauthRequest(ctx, http.MethodGet, e.UserInfoURL+"?"+info.Encode(), nil, ""); err == nil {
		if data, _, err := oauthDo(req); err == nil {
			json.Unmarshal(data, &u)
		}
	}
	return &oauthProfile{Subject: firstNonEmpty(tok.UnionID, u.UnionID, tok.OpenID), Name: u.Nickname, Avatar: u.HeadImgURL}, nil
}

// oauthAccount signs in, links or creates the account for a verified third-party profile.
func (a *App) oauthAccount(p OAuthProvider, e OAuthPreset, prof *oauthProfile, f oauthFlow, sessionUserID, ua, ip string) (*OAuthResult, error) {
	t := now()
	email := ""
	if prof.Email != "" && prof.EmailVerified {
		email = NormalizeEmail(prof.Email)
	}
	var ident model.OAuthIdentity
	found := a.DB.Where("provider = ? AND subject = ?", p.ID, prof.Subject).Limit(1).Find(&ident).RowsAffected > 0
	touch := func(userID string) {
		up := map[string]any{"name": truncateStr(prof.Name, 60), "avatar": truncateStr(prof.Avatar, 500), "email": truncateStr(email, 190), "last_login_at": &t}
		if found {
			a.DB.Model(&ident).Updates(up)
		} else {
			ident = model.OAuthIdentity{Provider: p.ID, Subject: prof.Subject, UserID: userID, Name: truncateStr(prof.Name, 60), Avatar: truncateStr(prof.Avatar, 500), Email: truncateStr(email, 190), LastLoginAt: &t}
			a.DB.Create(&ident)
		}
	}

	// linking from account settings: only for the account signed in in this browser
	if f.BindUser != "" {
		if sessionUserID == "" || sessionUserID != f.BindUser {
			return nil, Bad("请先登录要绑定的账号，再回到账号设置里绑定")
		}
		var u model.User
		if a.DB.First(&u, "id = ?", f.BindUser).Error != nil {
			return nil, NotFound("账号")
		}
		if found && ident.UserID != u.ID {
			return nil, Conflict(fmt.Sprintf("这个 %s 账号已经绑定了另一个用户", e.Name))
		}
		if !found {
			var n int64
			a.DB.Model(&model.OAuthIdentity{}).Where("user_id = ? AND provider = ?", u.ID, p.ID).Count(&n)
			if n > 0 {
				return nil, Conflict(fmt.Sprintf("你已经绑定过一个 %s 账号，请先解除绑定", e.Name))
			}
		}
		touch(u.ID)
		return &OAuthResult{User: &u, Redirect: f.Redirect, Bound: true}, nil
	}

	if found {
		var u model.User
		if a.DB.First(&u, "id = ?", ident.UserID).Error != nil {
			return nil, NotFound("账号")
		}
		uu, pair, err := a.finishLogin(&u, "studio", ua, ip)
		if err != nil {
			return nil, err
		}
		touch(u.ID)
		if f.Agree && uu.LegalVersion != a.LegalVersion() {
			uu.LegalVersion = a.AgreeLegal(uu.ID)
		}
		return &OAuthResult{User: uu, Pair: pair, Redirect: f.Redirect}, nil
	}

	// a new identity never takes over an existing account by email: the owner links it after signing in
	if prof.Email != "" {
		var n int64
		a.DB.Model(&model.User{}).Where("email = ?", NormalizeEmail(prof.Email)).Count(&n)
		if n > 0 {
			return nil, Conflict(fmt.Sprintf("邮箱 %s 已经注册过。请先用邮箱登录，再到「账号设置」绑定 %s，以后就能直接用 %s 登录了", prof.Email, e.Name, e.Name))
		}
	}
	site := a.Site()
	switch {
	case !p.AllowSignup:
		return nil, Forbidden(fmt.Sprintf("这个 %s 账号还没有绑定本站账号。请先注册或登录，再到「账号设置」绑定 %s", e.Name, e.Name))
	case !site.RegistrationOpen:
		return nil, Forbidden("平台暂未开放注册")
	case site.InviteOnly:
		return nil, Forbidden(fmt.Sprintf("当前为邀请制注册：请先用邀请码注册，再到「账号设置」绑定 %s", e.Name))
	case !f.Agree:
		return nil, Bad("注册前请先阅读并同意《用户服务协议》《隐私政策》和《版权与素材使用声明》")
	}
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	nick := firstNonEmpty(prof.Name, strings.Split(email, "@")[0], e.Name+"用户")
	u, err := a.createUser(emailPtr, nil, "", nick, prof.Avatar)
	if err != nil {
		return nil, err
	}
	u.LegalVersion, u.LegalAgreedAt = a.AgreeLegal(u.ID), &t
	touch(u.ID)
	uu, pair, err := a.finishLogin(u, "studio", ua, ip)
	if err != nil {
		return nil, err
	}
	return &OAuthResult{User: uu, Pair: pair, Redirect: "/?welcome=1", Created: true}, nil
}
