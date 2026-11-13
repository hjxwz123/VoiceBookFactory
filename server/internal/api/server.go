// Package api exposes the HTTP API (studio app, admin console, open platform) and serves the web apps.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gorm.io/gorm"

	"voicebook/internal/core"
	"voicebook/internal/model"
	"voicebook/internal/secure"
	"voicebook/internal/version"
)

type Server struct {
	app *core.App
}

func New(app *core.App) *Server {
	errorLog = func(r *http.Request, status int, public, detail string) string {
		uid := ""
		if u, ok := r.Context().Value(keyUser).(*model.User); ok && u != nil {
			uid = u.ID
		}
		return app.LogError(uid, r.Method, r.URL.Path, status, public, detail)
	}
	return &Server{app: app}
}

type ctxKey int

const (
	keyUser ctxKey = iota
	keySession
	keyAPIKey
	keyAPIAccess
)

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, recoverer, middleware.Compress(5, "application/json", "text/html", "text/css", "application/javascript", "text/xml"))
	r.Use(securityHeaders)

	// health for load balancers and `docker compose`: 200 when the database (and Redis, if used) answer
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		out := map[string]any{"ok": true, "time": time.Now(), "version": version.Version, "db": s.app.Cfg.DBDriver, "bus": s.app.Bus.Kind(), "seeding": s.app.Seeding()}
		if c := version.Commit; c != "" {
			out["commit"] = c
		}
		status := 200
		if sqlDB, err := s.app.DB.DB(); err != nil || sqlDB.PingContext(ctx) != nil {
			out["ok"], out["dbError"] = false, "数据库无法连接"
			status = 503
		}
		if err := s.app.Bus.Ping(ctx); err != nil {
			out["ok"], out["busError"] = false, "Redis 无法连接"
			status = 503
		}
		writeJSON(w, status, out)
	})
	r.Get("/files/*", s.serveFile)
	r.Get("/feeds/{token}/{book}", s.feed)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(jsonOnlyErrors)
		s.publicRoutes(r)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate("studio"))
			s.meRoutes(r)
			s.membershipRoutes(r)
			s.studioRoutes(r)
			s.bookRoutes(r)
			s.productionRoutes(r)
			s.voiceRoutes(r)
			s.assetRoutes(r)
			s.miscRoutes(r)
		})
	})
	r.Route("/api/admin/v1", func(r chi.Router) {
		r.Use(jsonOnlyErrors)
		s.adminAuthRoutes(r)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate("admin"))
			s.adminRoutes(r)
			s.adminMembershipRoutes(r)
		})
	})
	r.Route("/openapi/v1", func(r chi.Router) {
		// the description and the guide are public; everything else needs a key
		r.Get("/openapi.yaml", s.openAPISpec)
		r.Get("/docs", s.openAPIDocs)
		r.Group(func(r chi.Router) {
			r.Use(s.apiKeyAuth)
			s.openRoutes(r)
		})
	})
	s.mountWeb(r)
	return r
}

// ---------- middleware ----------

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "path", r.URL.Path, "err", rec)
				writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": "服务器开小差了，请稍后重试"}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

func jsonOnlyErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t := r.URL.Query().Get("access_token"); t != "" && strings.HasSuffix(r.URL.Path, "/events") {
		return t
	}
	return ""
}

func (s *Server) authenticate(kind string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := secure.ParseAccess(s.app.Cfg.JWTSecret, bearer(r))
			if err != nil || claims.Kind != kind {
				fail(w, core.Unauthorized("请先登录"))
				return
			}
			var sess model.Session
			if s.app.DB.Select("id", "revoked_at").First(&sess, "id = ?", claims.SessionID).Error != nil || sess.RevokedAt != nil {
				fail(w, core.Unauthorized("登录已失效，请重新登录"))
				return
			}
			var u model.User
			if s.app.DB.First(&u, "id = ?", claims.Subject).Error != nil || u.Status != model.UserActive {
				fail(w, core.Unauthorized("账号不可用"))
				return
			}
			if kind == "admin" && !u.IsAdmin() {
				fail(w, core.Forbidden("没有管理后台权限"))
				return
			}
			ctx := context.WithValue(r.Context(), keyUser, &u)
			ctx = context.WithValue(ctx, keySession, claims.SessionID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func userOf(r *http.Request) *model.User {
	u, _ := r.Context().Value(keyUser).(*model.User)
	return u
}

func sessionOf(r *http.Request) string {
	s, _ := r.Context().Value(keySession).(string)
	return s
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- JSON helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func ok(w http.ResponseWriter, v any) {
	if v == nil {
		v = map[string]any{"ok": true}
	}
	writeJSON(w, 200, v)
}

func fail(w http.ResponseWriter, err error) {
	var ce *core.Error
	if errors.As(err, &ce) {
		body := map[string]any{"code": ce.Code, "message": ce.Msg}
		if ce.Data != nil {
			body["data"] = ce.Data
		}
		writeJSON(w, ce.Status, map[string]any{"error": body})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "内容不存在或已被删除"}})
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "UNIQUE constraint") || strings.Contains(msg, "duplicate key") {
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "conflict", "message": "已存在相同的记录，请修改后重试"}})
		return
	}
	slog.Error("request failed", "err", err)
	if len(msg) > 300 {
		msg = msg[:300]
	}
	writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": msg}})
}

func bind(r *http.Request, v any) error {
	if r.Body == nil {
		return core.Bad("请求内容为空")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<20))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		return core.Bad("请求格式错误：%v", err)
	}
	return nil
}

func param(r *http.Request, name string) string { return chi.URLParam(r, name) }

func qint(r *http.Request, name string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return v
	}
	return def
}

func qtime(r *http.Request, name string, def time.Time) time.Time {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	return def
}

type page struct {
	Items    any   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// paginate applies page/pageSize to q and returns totals.
func paginate[T any](r *http.Request, q *gorm.DB, order string) (page, error) {
	p := qint(r, "page", 1)
	size := qint(r, "pageSize", 20)
	if size > 200 {
		size = 200
	}
	if p < 1 {
		p = 1
	}
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return page{}, err
	}
	var items []T
	if order == "" {
		order = "created_at desc"
	}
	if err := q.Order(order).Offset((p - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return page{}, err
	}
	if items == nil {
		items = []T{}
	}
	return page{Items: items, Total: total, Page: p, PageSize: size}, nil
}

// h adapts handlers that return (value, error).
func h(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			if public(r) {
				failPublic(w, r, err)
				return
			}
			fail(w, err)
			return
		}
		if public(r) {
			okPublic(w, v)
			return
		}
		ok(w, v)
	}
}

// public reports whether a request comes from users (the studio app, the open API) rather than
// administrators: users never see technical error detail.
func public(r *http.Request) bool { return !strings.HasPrefix(r.URL.Path, "/api/admin/") }

// errorLog keeps the full error behind a plain message shown to a user; set by NewServer.
var errorLog func(r *http.Request, status int, public, detail string) string

// failPublic answers a user's request that failed. Messages written for users go out as they are;
// anything technical is replaced by a plain explanation and a reference, and the full error goes to
// the error log for administrators.
func failPublic(w http.ResponseWriter, r *http.Request, err error) {
	var ce *core.Error
	if errors.As(err, &ce) {
		msg := core.PublicText(ce.Msg)
		if msg != ce.Msg && errorLog != nil {
			msg += "（编号 " + errorLog(r, ce.Status, msg, ce.Msg) + "）"
		}
		body := map[string]any{"code": ce.Code, "message": msg}
		if ce.Data != nil {
			body["data"] = ce.Data
		}
		writeJSON(w, ce.Status, map[string]any{"error": body})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "duplicate key") {
		fail(w, err)
		return
	}
	slog.Error("request failed", "path", r.URL.Path, "err", err)
	msg := core.PublicText(err.Error())
	if msg == err.Error() { // not recognisably technical, but not written for users either
		msg = core.GenericFailure
	}
	if errorLog != nil {
		msg += "（编号 " + errorLog(r, 500, msg, err.Error()) + "）"
	}
	writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": msg}})
}

// errorFields matches the fields that carry stored error messages in responses and events.
var errorFields = regexp.MustCompile(`"(error|lastError|statusNote)":"[^"]`)

// okPublic answers a user's request, with stored error messages in plain words (see core.PublicText).
func okPublic(w http.ResponseWriter, v any) {
	if v == nil {
		v = map[string]any{"ok": true}
	}
	b, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, 200, v)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	w.Write(publicJSON(b))
	w.Write([]byte("\n"))
}

// publicJSON rewrites error fields of an encoded value for users. Values without any are returned
// untouched (the common case costs one scan).
func publicJSON(b []byte) []byte {
	if !errorFields.Match(b) {
		return b
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return b
	}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, val := range t {
				if s, ok := val.(string); ok && (k == "error" || k == "lastError" || k == "statusNote") {
					t[k] = core.PublicText(s)
					continue
				}
				walk(val)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return b
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}

// ---------- access loaders ----------

func (s *Server) studio(r *http.Request) (*core.Access, error) {
	return s.app.StudioAccess(userOf(r), param(r, "sid"))
}

func (s *Server) book(r *http.Request) (*core.Access, error) {
	return s.app.BookAccess(userOf(r), param(r, "bid"))
}

func (s *Server) chapter(r *http.Request) (*core.Access, *model.Chapter, error) {
	return s.app.ChapterAccess(userOf(r), param(r, "cid"))
}

func (s *Server) segment(r *http.Request) (*core.Access, *model.Segment, error) {
	return s.app.SegmentAccess(userOf(r), param(r, "segid"))
}

// ---------- uploads ----------

// saveUpload stores a multipart file as an asset.
func (s *Server) saveUpload(r *http.Request, field string, in core.AssetInput) (*model.Asset, error) {
	mb := s.uploadLimitMB(in.StudioID)
	limit := int64(mb) << 20
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, core.Bad("上传失败：%v", err)
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return nil, core.Bad("请选择要上传的文件")
	}
	defer f.Close()
	if hdr.Size > limit {
		return nil, &core.Error{Status: 413, Code: "plan_limit", Msg: fmt.Sprintf("文件过大：当前会员单个文件最大 %d MB", mb), Data: map[string]any{"kind": "upload", "limit": mb}}
	}
	if in.Name == "" {
		in.Name = strings.TrimSuffix(hdr.Filename, filepath.Ext(hdr.Filename))
	}
	if in.Ext == "" {
		in.Ext = filepath.Ext(hdr.Filename)
	}
	return s.app.SaveAsset(r.Context(), in, io.LimitReader(f, limit))
}

// uploadLimitMB is the single-file limit: the studio plan's value, else the site setting.
func (s *Server) uploadLimitMB(studioID string) int {
	if studioID != "" {
		if mb := s.app.StudioPlan(studioID).MaxUploadMB; mb > 0 {
			return mb
		}
	}
	if mb := s.app.Site().MaxUploadMB; mb > 0 {
		return mb
	}
	return 200
}

// tempUpload writes a multipart file to a temp path (for files that are processed then discarded).
func (s *Server) tempUpload(r *http.Request, field string) (string, string, func(), error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return "", "", nil, core.Bad("上传失败：%v", err)
	}
	f, hdr, err := r.FormFile(field)
	if err != nil {
		return "", "", nil, core.Bad("请选择要上传的文件")
	}
	defer f.Close()
	if mb := s.uploadLimitMB(chi.URLParam(r, "sid")); hdr.Size > int64(mb)<<20 {
		return "", "", nil, &core.Error{Status: 413, Code: "plan_limit", Msg: fmt.Sprintf("文件过大：当前会员单个文件最大 %d MB", mb), Data: map[string]any{"kind": "upload", "limit": mb}}
	}
	dir, err := s.app.Store.TempDir("upload")
	if err != nil {
		return "", "", nil, err
	}
	p := filepath.Join(dir, "upload"+strings.ToLower(filepath.Ext(hdr.Filename)))
	out, err := os.Create(p)
	if err != nil {
		return "", "", nil, err
	}
	io.Copy(out, io.LimitReader(f, 500<<20))
	out.Close()
	return p, hdr.Filename, func() { os.RemoveAll(dir) }, nil
}

// ---------- files & web ----------

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	q := r.URL.Query()
	if !s.app.Store.Verify(key, q.Get("e"), q.Get("dl"), q.Get("s")) {
		http.Error(w, "链接已过期", http.StatusForbidden)
		return
	}
	// files kept in Aliyun OSS: the browser downloads them from OSS with a short-lived signed URL
	if u, ok := s.app.Store.RemoteURL(key, time.Hour, q.Get("dl")); ok {
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.Redirect(w, r, u, http.StatusFound)
		return
	}
	f, err := s.app.Store.Open(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	if dl := q.Get("dl"); dl != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlEscape(dl)))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, filepath.Base(key), st.ModTime(), f)
}

func urlEscape(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || strings.IndexByte("-_.~", b) >= 0 {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	book := strings.TrimSuffix(param(r, "book"), ".xml")
	out, err := s.app.RSSFeed(param(r, "token"), book)
	if err != nil {
		http.Error(w, "订阅源不存在", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write(out)
}

// mountWeb serves the built SPA: /admin/* -> admin.html, everything else -> index.html.
func (s *Server) mountWeb(r chi.Router) {
	dir := s.app.Cfg.WebDir
	fs := http.FileServer(http.Dir(dir))
	serveIndex := func(w http.ResponseWriter, req *http.Request, name string) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(200)
			fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>声书工坊</title><body style="font-family:system-ui;padding:48px"><h2>前端尚未构建</h2><p>请在 web 目录运行 <code>npm run build</code>，或使用 <code>npm run dev</code> 启动开发服务器。</p></body>`)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, req, p)
	}
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/openapi/") {
			http.NotFound(w, req)
			return
		}
		clean := filepath.Join(dir, filepath.FromSlash(filepath.Clean("/"+path)))
		if st, err := os.Stat(clean); err == nil && !st.IsDir() {
			if strings.HasPrefix(path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fs.ServeHTTP(w, req)
			return
		}
		if path == "/admin" || strings.HasPrefix(path, "/admin/") {
			serveIndex(w, req, "admin.html")
			return
		}
		serveIndex(w, req, "index.html")
	})
}

// ---------- SSE ----------

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	flusher, okf := w.(http.Flusher)
	if !okf {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	var studios []string
	s.app.DB.Model(&model.StudioMember{}).Where("user_id = ? AND status = ?", u.ID, "active").Pluck("studio_id", &studios)
	sub, cancel := s.app.Hub.Subscribe(u.ID, studios)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprintf(w, "retry: 3000\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	refresh := time.NewTicker(2 * time.Minute)
	defer refresh.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-sub.C:
			b, _ := json.Marshal(ev)
			if public(r) {
				b = publicJSON(b)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-refresh.C:
			var ids []string
			s.app.DB.Model(&model.StudioMember{}).Where("user_id = ? AND status = ?", u.ID, "active").Pluck("studio_id", &ids)
			sub.SetStudios(ids)
		}
	}
}
