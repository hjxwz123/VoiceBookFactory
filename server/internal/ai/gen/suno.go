package gen

// Suno music generation.
//
// Suno has no public, documented API: its own developer platform (platform.suno.com) is in a
// partner phase with no published reference. In practice Suno is reached through relay services
// that speak the Suno-API task protocol, the one New API / One API expose for their "Suno" channel:
//
//	POST {base}/suno/submit/music   → {"code":"success","data":"<task id>"}
//	GET  {base}/suno/fetch/{task}   → {"code":"success","data":{"status":"SUCCESS","data":[clip, clip]}}
//
// One submission makes two songs (clips). Each clip carries an mp3 audio_url that is only final
// once the clip status is "complete" (before that it is a live stream). This adapter submits,
// polls until the task finishes and downloads the finished files.
//
// Docs of the protocol: https://doc.newapi.pro/en/api/suno-music

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// MusicSetProvider is implemented by providers that return several candidate tracks per request
// (Suno makes two songs per submission). Callers prefer it so no candidate is thrown away.
type MusicSetProvider interface {
	GenerateMusicSet(ctx context.Context, r MusicRequest) ([]File, error)
}

// MusicFeatures says what a music provider can take beyond a description, for the studio's form.
type MusicFeatures struct {
	Vocals     bool `json:"vocals"`     // songs with singing, not only instrumentals
	Lyrics     bool `json:"lyrics"`     // custom lyrics
	Style      bool `json:"style"`      // style tags
	Duration   bool `json:"duration"`   // honours a requested length
	PerRequest int  `json:"perRequest"` // tracks returned by one request
}

// Features of a music adapter; unknown and placeholder ones only take a description and a length.
func MusicFeaturesOf(provider string) MusicFeatures {
	if provider == "suno" {
		return MusicFeatures{Vocals: true, Lyrics: true, Style: true, PerRequest: 2}
	}
	return MusicFeatures{Duration: true, PerRequest: 1}
}

// SunoError is a failed Suno call, with a message an operator or user can act on.
type SunoError struct {
	Status  int
	Message string // friendly Chinese explanation
	Raw     string // what the service said, kept for the operator
}

func (e *SunoError) Error() string {
	s := "Suno 生成失败：" + e.Message
	if e.Raw != "" && !strings.Contains(e.Message, e.Raw) {
		s += "（" + clip(e.Raw, 200) + "）"
	}
	return s
}

type sunoMusic struct {
	base    string
	key     string
	model   string
	hc      *http.Client
	poll    time.Duration
	maxWait time.Duration
}

// sunoPollUnit scales the polling interval; tests shrink it.
var sunoPollUnit = time.Second

func newSuno(a Account) *sunoMusic {
	base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	// the Suno routes live at the root of a relay, not under the OpenAI-style /v1 people often paste
	base = strings.TrimSuffix(base, "/v1")
	return &sunoMusic{
		base:    base,
		key:     strings.TrimSpace(a.APIKey),
		model:   strings.TrimSpace(a.Model),
		hc:      &http.Client{Timeout: time.Duration(cfgInt(a.Config, "timeoutSec", 60)) * time.Second},
		poll:    time.Duration(cfgInt(a.Config, "pollSec", 5)) * sunoPollUnit,
		maxWait: time.Duration(cfgInt(a.Config, "maxWaitSec", 600)) * sunoPollUnit,
	}
}

func (s *sunoMusic) Name() string { return "suno" }

func (s *sunoMusic) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.key, "Accept": "application/json"}
}

// envelope is the relay's wrapper. code is "success" on New API; some relays send 200 or "200".
type sunoEnvelope struct {
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (e sunoEnvelope) ok() bool {
	switch c := e.Code.(type) {
	case string:
		return c == "success" || c == "200" || c == "0"
	case float64:
		return c == 200 || c == 0
	case nil:
		return e.Error == nil
	}
	return false
}

func (e sunoEnvelope) text() string {
	switch {
	case e.Error != nil && e.Error.Message != "":
		return e.Error.Message
	case e.Message != "":
		return e.Message
	case e.Msg != "":
		return e.Msg
	}
	if c, ok := e.Code.(string); ok {
		return c
	}
	return ""
}

type sunoClip struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	AudioURL string `json:"audio_url"`
	ImageURL string `json:"image_url"`
	Model    string `json:"model_name"`
	Metadata struct {
		Tags         string   `json:"tags"`
		Prompt       string   `json:"prompt"`
		Duration     *float64 `json:"duration"`
		ErrorType    *string  `json:"error_type"`
		ErrorMessage *string  `json:"error_message"`
	} `json:"metadata"`
}

type sunoTask struct {
	TaskID     string     `json:"task_id"`
	Status     string     `json:"status"`
	FailReason string     `json:"fail_reason"`
	Progress   string     `json:"progress"`
	Data       []sunoClip `json:"data"`
}

// explain turns an HTTP failure into something an operator can act on.
func (s *sunoMusic) explain(status int, body []byte, during string) *SunoError {
	var env sunoEnvelope
	_ = json.Unmarshal(body, &env)
	raw := env.text()
	if raw == "" && len(body) > 0 && body[0] != '<' {
		raw = string(body)
	}
	e := &SunoError{Status: status, Raw: strings.TrimSpace(raw)}
	switch {
	case status == 401 || status == 403:
		e.Message = "密钥无效，或这个账号没有开通 Suno 渠道"
	case status == 402:
		e.Message = "服务方余额或额度不足"
	case status == 429:
		e.Message = "请求太频繁或额度已用完，请稍后再试"
	case status == 404 && during == "submit" && (len(body) == 0 || body[0] == '<' || env.Code == nil):
		e.Message = "接口地址不对：这个地址没有 /suno/submit/music。请填写支持 Suno 的 New API / One API 中转服务的根地址"
	case status >= 500:
		e.Message = "服务方暂时出错，请稍后再试"
	case status == 0:
		e.Message = "连接不上服务地址，请检查接口地址和服务器网络"
	default:
		e.Message = fmt.Sprintf("请求被拒绝（HTTP %d）", status)
	}
	if low := strings.ToLower(raw); e.Status < 500 && (strings.Contains(low, "quota") || strings.Contains(raw, "余额") || strings.Contains(raw, "额度")) {
		e.Message = "服务方余额或额度不足"
	}
	return e
}

func (s *sunoMusic) check() error {
	if s.base == "" {
		return &SunoError{Message: "还没有填写接口地址（支持 Suno 的中转服务根地址）"}
	}
	if s.key == "" {
		return &SunoError{Message: "还没有填写 API Key"}
	}
	return nil
}

// submit starts one task and returns its id. It is never retried after the service accepted the
// request, so a slow answer can never be billed twice.
func (s *sunoMusic) submit(ctx context.Context, r MusicRequest) (string, error) {
	body := map[string]any{"make_instrumental": r.Instrumental}
	if s.model != "" {
		body["mv"] = s.model
	}
	lyrics := strings.TrimSpace(r.Lyrics)
	if r.Instrumental {
		lyrics = "" // nothing is sung
	}
	style := strings.TrimSpace(r.Style)
	if lyrics != "" || style != "" || strings.TrimSpace(r.Title) != "" {
		// custom mode: our lyrics (none for an instrumental), style tags and a title
		if !r.Instrumental {
			body["prompt"] = clipRunes(lyrics, 3000)
		} else {
			body["prompt"] = ""
		}
		if style == "" {
			style = r.Prompt
		}
		body["tags"] = clipRunes(style, 200)
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.Prompt
		}
		body["title"] = clipRunes(title, 80)
	} else {
		// description mode: Suno writes the lyrics (if any) and picks the style
		body["gpt_description_prompt"] = clipRunes(r.Prompt, 400)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt*3) * sunoPollUnit):
			}
		}
		status, data, err := doHTTP(ctx, s.hc, http.MethodPost, s.base+"/suno/submit/music", s.headers(), body)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// the request may have reached the service; do not risk a second song bill
			return "", &SunoError{Message: "连接服务时出错，任务可能没有提交成功", Raw: err.Error()}
		}
		if status == 429 || status == 502 || status == 503 { // refused before any work started
			lastErr = s.explain(status, data, "submit")
			continue
		}
		if status != 200 {
			return "", s.explain(status, data, "submit")
		}
		var env sunoEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			return "", &SunoError{Status: status, Message: "服务返回的不是 Suno 任务格式，请确认接口地址", Raw: clip(string(data), 160)}
		}
		if !env.ok() {
			return "", &SunoError{Status: status, Message: "服务拒绝了这次生成", Raw: env.text()}
		}
		var id string
		if json.Unmarshal(env.Data, &id) != nil || id == "" {
			var obj struct {
				TaskID string `json:"task_id"`
				ID     string `json:"id"`
			}
			_ = json.Unmarshal(env.Data, &obj)
			id = firstNonEmpty(obj.TaskID, obj.ID)
		}
		if id == "" {
			return "", &SunoError{Status: status, Message: "服务没有返回任务编号", Raw: clip(string(data), 160)}
		}
		return id, nil
	}
	return "", lastErr
}

// fetch reads a task once. Network trouble and 5xx are reported as retryable.
func (s *sunoMusic) fetch(ctx context.Context, id string) (*sunoTask, bool, error) {
	status, data, err := doHTTP(ctx, s.hc, http.MethodGet, s.base+"/suno/fetch/"+url.PathEscape(id), s.headers(), nil)
	if err != nil {
		return nil, true, &SunoError{Message: "查询生成进度时连接失败", Raw: err.Error()}
	}
	if status != 200 {
		return nil, status >= 500 || status == 429, s.explain(status, data, "fetch")
	}
	var env sunoEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, true, &SunoError{Status: status, Message: "进度查询返回了无法识别的内容", Raw: clip(string(data), 160)}
	}
	if !env.ok() {
		return nil, false, &SunoError{Status: status, Message: "查询生成进度失败", Raw: env.text()}
	}
	var t sunoTask
	if err := json.Unmarshal(env.Data, &t); err != nil {
		var list []sunoTask // a relay that always answers with the batch shape
		if json.Unmarshal(env.Data, &list) != nil || len(list) == 0 {
			return nil, true, &SunoError{Status: status, Message: "进度查询返回了无法识别的内容", Raw: clip(string(env.Data), 160)}
		}
		t = list[0]
	}
	return &t, false, nil
}

// wait polls until every clip is complete or the task fails.
func (s *sunoMusic) wait(ctx context.Context, id string) ([]sunoClip, error) {
	deadline := time.Now().Add(s.maxWait)
	misses := 0
	for {
		t, retry, err := s.fetch(ctx, id)
		switch {
		case err != nil && (!retry || misses >= 5):
			return nil, err
		case err != nil:
			misses++
		default:
			misses = 0
			st := strings.ToUpper(t.Status)
			if st == "FAILURE" || st == "FAIL" || st == "FAILED" || st == "ERROR" {
				return nil, &SunoError{Message: "Suno 没能完成这首曲子", Raw: firstNonEmpty(t.FailReason, clipError(t.Data))}
			}
			var done []sunoClip
			for _, c := range t.Data {
				if strings.EqualFold(c.Status, "error") {
					continue
				}
				if strings.EqualFold(c.Status, "complete") && c.AudioURL != "" {
					done = append(done, c)
				}
			}
			finished := st == "SUCCESS" || st == "COMPLETE" || st == "COMPLETED"
			if finished || (len(t.Data) > 0 && len(done)+countErrors(t.Data) == len(t.Data)) {
				if len(done) == 0 {
					return nil, &SunoError{Message: "任务结束了，但没有可用的音频", Raw: firstNonEmpty(t.FailReason, clipError(t.Data))}
				}
				return done, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, &SunoError{Message: fmt.Sprintf("等了 %d 分钟仍未完成，已停止等待（任务编号 %s，可在服务方后台查看）", int(s.maxWait/sunoPollUnit/60), id)}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.poll):
		}
	}
}

func countErrors(cs []sunoClip) int {
	n := 0
	for _, c := range cs {
		if strings.EqualFold(c.Status, "error") {
			n++
		}
	}
	return n
}

func clipError(cs []sunoClip) string {
	for _, c := range cs {
		if c.Metadata.ErrorMessage != nil && *c.Metadata.ErrorMessage != "" {
			return *c.Metadata.ErrorMessage
		}
	}
	return ""
}

// download fetches a finished clip. Only http(s) URLs are followed and the size is capped.
func (s *sunoMusic) download(ctx context.Context, c sunoClip) (File, error) {
	u, err := url.Parse(c.AudioURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return File{}, &SunoError{Message: "音频地址无效", Raw: clip(c.AudioURL, 120)}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return File{}, ctx.Err()
			case <-time.After(time.Duration(attempt*2) * sunoPollUnit):
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.AudioURL, nil)
		dl := &http.Client{Timeout: 3 * time.Minute}
		resp, err := dl.Do(req)
		if err != nil {
			lastErr = &SunoError{Message: "下载生成的音频失败", Raw: err.Error()}
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20+1))
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			lastErr = &SunoError{Status: resp.StatusCode, Message: "下载生成的音频失败"}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return File{}, lastErr
			}
			continue
		}
		if len(data) > 64<<20 {
			return File{}, &SunoError{Message: "音频文件超过 64 MB，已放弃"}
		}
		mime, ext := audioType(data, resp.Header.Get("Content-Type"), u.Path)
		if mime == "" {
			return File{}, &SunoError{Message: "下载到的不是音频文件", Raw: clip(http.DetectContentType(data), 60)}
		}
		meta := map[string]any{"provider": "suno", "clipId": c.ID, "title": c.Title, "tags": c.Metadata.Tags, "model": c.Model}
		if c.Metadata.Duration != nil {
			meta["durationSec"] = *c.Metadata.Duration
		}
		if c.Metadata.Prompt != "" {
			meta["lyrics"] = c.Metadata.Prompt
		}
		if c.ImageURL != "" {
			meta["imageUrl"] = c.ImageURL
		}
		return File{Data: data, Mime: mime, Ext: ext, Meta: meta}, nil
	}
	return File{}, lastErr
}

// audioType recognises mp3 / wav / m4a / ogg / flac from the bytes, falling back to the header and URL.
func audioType(b []byte, header, urlPath string) (string, string) {
	switch {
	case len(b) >= 3 && string(b[:3]) == "ID3", len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0:
		return "audio/mpeg", "mp3"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE":
		return "audio/wav", "wav"
	case len(b) >= 4 && string(b[:4]) == "OggS":
		return "audio/ogg", "ogg"
	case len(b) >= 4 && string(b[:4]) == "fLaC":
		return "audio/flac", "flac"
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		return "audio/mp4", "m4a"
	}
	if sniff := http.DetectContentType(b); strings.HasPrefix(sniff, "text/") || strings.HasPrefix(sniff, "application/json") {
		return "", "" // an error page, whatever the header claims
	}
	if strings.HasPrefix(strings.ToLower(header), "audio/") {
		ext := strings.TrimPrefix(path.Ext(urlPath), ".")
		if ext == "" {
			ext = "mp3"
		}
		return strings.ToLower(strings.Split(header, ";")[0]), ext
	}
	return "", ""
}

// GenerateMusicSet submits once and returns every finished song (usually two).
func (s *sunoMusic) GenerateMusicSet(ctx context.Context, r MusicRequest) ([]File, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" && strings.TrimSpace(r.Lyrics) == "" && strings.TrimSpace(r.Style) == "" {
		return nil, &SunoError{Message: "请描述想要的音乐"}
	}
	id, err := s.submit(ctx, r)
	if err != nil {
		return nil, err
	}
	if r.OnTask != nil {
		r.OnTask(id)
	}
	clips, err := s.wait(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []File
	var lastErr error
	for _, c := range clips {
		f, err := s.download(ctx, c)
		if err != nil {
			lastErr = err
			continue
		}
		f.Meta["taskId"] = id
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, lastErr
	}
	return out, nil
}

func (s *sunoMusic) GenerateMusic(ctx context.Context, r MusicRequest) (*File, error) {
	files, err := s.GenerateMusicSet(ctx, r)
	if err != nil {
		return nil, err
	}
	return &files[0], nil
}

// Check verifies the address and key without making (and paying for) a song: it asks for a task
// that cannot exist. A relay that knows the route answers in JSON, whatever it thinks of the id.
func (s *sunoMusic) Check(ctx context.Context) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	probe := "vb-check-" + hex.EncodeToString(buf)
	status, data, err := doHTTP(ctx, s.hc, http.MethodGet, s.base+"/suno/fetch/"+probe, s.headers(), nil)
	if err != nil {
		return "", &SunoError{Message: "连接不上服务地址，请检查接口地址和服务器网络", Raw: err.Error()}
	}
	if status == 401 || status == 403 {
		return "", s.explain(status, data, "check")
	}
	var env sunoEnvelope
	if json.Unmarshal(data, &env) != nil {
		return "", &SunoError{Status: status, Message: "这个地址不像支持 Suno 的中转服务（/suno/fetch 没有返回 JSON）。请填写服务的根地址", Raw: clip(string(data), 120)}
	}
	if status >= 500 {
		return "", s.explain(status, data, "check")
	}
	m := s.model
	if m == "" {
		m = "服务默认"
	}
	return fmt.Sprintf("连接正常：接口地址和密钥可用，模型 %s。检查没有生成歌曲，不产生费用", m), nil
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}
