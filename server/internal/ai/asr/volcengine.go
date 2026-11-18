package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"voicebook/internal/ai/volc"
)

// Volcengine recording-file recognition (豆包录音文件识别, standard edition), official API:
//
//	POST {base}/api/v3/auc/bigmodel/submit   X-Api-Key, X-Api-Resource-Id: volc.seedasr.auc, X-Api-Request-Id: <task id>, X-Api-Sequence: -1
//	     {"audio": {"url": "...", "format": "mp3"}, "request": {"model_name": "bigmodel", "show_utterances": true, ...}}
//	POST {base}/api/v3/auc/bigmodel/query    same headers, X-Api-Request-Id: <task id>, body {}
//
// The state is in the X-Api-Status-Code response header: 20000000 done, 20000001 processing, 20000002 queued,
// 20000003 silent audio. The service downloads the audio itself, so the file is published on this site through a
// short-lived signed link (Account.Publish) for the duration of the task.

const (
	VolcASRDefaultResource = "volc.seedasr.auc" // 豆包录音文件识别模型 2.0 (volc.bigasr.auc is 1.0)
	volcASRDone            = "20000000"
	volcASRProcessing      = "20000001"
	volcASRQueued          = "20000002"
	volcASRSilent          = "20000003"
)

var VolcASRResources = []string{"volc.seedasr.auc", "volc.bigasr.auc"}

type volcASR struct {
	base, key, appID, resource string
	cfg                        map[string]any
	publish                    func(ctx context.Context, path string) (string, func(), error)
	http                       *http.Client
	poll                       time.Duration
	maxWait                    time.Duration
}

func newVolc(a Account) *volcASR {
	res := strings.TrimSpace(a.Model)
	if !strings.HasPrefix(res, "volc.") {
		res = VolcASRDefaultResource
	}
	v := &volcASR{base: volc.Base(a.BaseURL), key: strings.TrimSpace(a.APIKey), appID: volc.AppID(a.Config), resource: res, cfg: a.Config, publish: a.Publish,
		http: &http.Client{Timeout: 60 * time.Second}, poll: 2 * time.Second, maxWait: 30 * time.Minute}
	if n, ok := a.Config["maxWaitMinutes"].(float64); ok && n >= 1 {
		v.maxWait = time.Duration(n * float64(time.Minute))
	}
	return v
}

func (v *volcASR) Name() string    { return "volcengine" }
func (v *volcASR) MaxBytes() int64 { return 512 << 20 }

// volcLanguages are the codes the service accepts in audio.language. Chinese is left unset on purpose: the
// model then also handles English words and dialects (上海话、四川话、粤语…) in a Chinese recording.
var volcLanguages = map[string]string{
	"en": "en-US", "ja": "ja-JP", "id": "id-ID", "es": "es-MX", "pt": "pt-BR", "de": "de-DE", "fr": "fr-FR", "ko": "ko-KR",
	"fil": "fil-PH", "ms": "ms-MY", "th": "th-TH", "ar": "ar-SA", "it": "it-IT", "bn": "bn-BD", "el": "el-GR", "nl": "nl-NL",
	"ru": "ru-RU", "tr": "tr-TR", "vi": "vi-VN", "pl": "pl-PL", "ro": "ro-RO", "ne": "ne-NP", "uk": "uk-UA",
}

func volcLanguage(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "zh-hk" || strings.HasPrefix(l, "yue") {
		return "yue-CN"
	}
	return volcLanguages[strings.SplitN(l, "-", 2)[0]]
}

var volcFormats = map[string]string{"wav": "wav", "mp3": "mp3", "ogg": "ogg", "opus": "ogg", "spx": "spx", "amr": "amr", "aac": "aac", "m4a": "m4a"}

func (v *volcASR) call(ctx context.Context, path, requestID string, sequence bool, body any) (string, string, string, []byte, int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.base+path, bytes.NewReader(b))
	if err != nil {
		return "", "", "", nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	volc.Auth(req.Header, v.key, v.appID)
	req.Header.Set("X-Api-Resource-Id", v.resource)
	req.Header.Set("X-Api-Request-Id", requestID)
	if sequence {
		req.Header.Set("X-Api-Sequence", "-1")
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return "", "", "", nil, 0, fmt.Errorf("连接火山引擎录音文件识别失败：%w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.Header.Get("X-Api-Status-Code"), resp.Header.Get("X-Api-Message"), resp.Header.Get("X-Tt-Logid"), data, resp.StatusCode, nil
}

func (v *volcASR) failure(httpStatus int, code, msg, logID string, data []byte) *volc.Error {
	if code == "" || msg == "" {
		c, m := volc.Parse(data)
		code, msg = firstNonEmptyStr(code, c), firstNonEmptyStr(msg, m)
	}
	if httpStatus == http.StatusOK && code != "" {
		httpStatus = 400
		if strings.HasPrefix(code, "55") {
			httpStatus = 500
		}
	}
	return &volc.Error{Status: httpStatus, Code: code, Msg: msg, Plain: volc.Plain("录音文件识别", httpStatus, code, msg), LogID: logID}
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// checkReachable refuses links the service could never download: this site's own address must be public
// unless the recognition service itself runs on a private network.
func (v *volcASR) checkReachable(link string) error {
	if volc.Local(v.base) {
		return nil
	}
	u, err := url.Parse(link)
	if err != nil || volc.PrivateHost(u.Hostname()) {
		host := link
		if u != nil {
			host = u.Scheme + "://" + u.Host
		}
		return fmt.Errorf("火山引擎录音文件识别需要从公网下载音频，但本站地址 %s 只能在内网访问。请把环境变量 VB_PUBLIC_URL 设置成公网可以访问的域名后重试", host)
	}
	return nil
}

type volcASRText struct {
	Text       string `json:"text"`
	Utterances []struct {
		Text      string `json:"text"`
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
		Words     []struct {
			Text      string `json:"text"`
			StartTime int64  `json:"start_time"`
			EndTime   int64  `json:"end_time"`
		} `json:"words"`
	} `json:"utterances"`
}

type volcASRResult struct {
	AudioInfo struct {
		Duration int64 `json:"duration"`
	} `json:"audio_info"`
	Result volcASRText `json:"-"`
}

// UnmarshalJSON reads `result` as an object (the documented example) or a list of them (the field table says
// list); a list is joined in order.
func (o *volcASRResult) UnmarshalJSON(b []byte) error {
	var raw struct {
		AudioInfo json.RawMessage `json:"audio_info"`
		Result    json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw.AudioInfo) > 0 {
		_ = json.Unmarshal(raw.AudioInfo, &o.AudioInfo)
	}
	r := bytes.TrimSpace(raw.Result)
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	if r[0] != '[' {
		return json.Unmarshal(r, &o.Result)
	}
	var list []volcASRText
	if err := json.Unmarshal(r, &list); err != nil {
		return err
	}
	var text strings.Builder
	for _, x := range list {
		text.WriteString(x.Text)
		o.Result.Utterances = append(o.Result.Utterances, x.Utterances...)
	}
	o.Result.Text = text.String()
	return nil
}

func (v *volcASR) Transcribe(ctx context.Context, path, language string) (*Result, error) {
	if v.key == "" {
		return nil, errors.New("火山引擎语音识别账号缺少 API Key")
	}
	format := volcFormats[audioFormat(path)]
	if format == "" {
		return nil, fmt.Errorf("火山引擎录音文件识别不支持 %s 格式，请使用 wav、mp3、ogg、m4a 或 aac", audioFormat(path))
	}
	if v.publish == nil {
		return nil, errors.New("火山引擎录音文件识别需要通过链接下载音频，但当前环境没有提供文件链接")
	}
	link, cleanup, err := v.publish(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("准备音频链接失败：%w", err)
	}
	defer cleanup()
	if err := v.checkReachable(link); err != nil {
		return nil, err
	}
	audio := map[string]any{"url": link, "format": format}
	if l := volcLanguage(language); l != "" {
		audio["language"] = l
	}
	request := map[string]any{"model_name": "bigmodel", "enable_itn": true, "enable_punc": true, "show_utterances": true}
	if b, ok := v.cfg["enableDDC"].(bool); ok && b {
		request["enable_ddc"] = true
	}
	taskID := volc.RequestID()
	var code, msg, logID string
	var data []byte
	var status int
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * v.poll):
			}
		}
		code, msg, logID, data, status, err = v.call(ctx, "/api/v3/auc/bigmodel/submit", taskID, true, map[string]any{"user": map[string]any{"uid": "voicebook"}, "audio": audio, "request": request})
		if err != nil {
			continue
		}
		if status == http.StatusOK && (code == volcASRDone || code == "") {
			var reply struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(data, &reply) == nil && strings.TrimSpace(reply.TaskID) != "" {
				taskID = strings.TrimSpace(reply.TaskID) // the documented reply names the task; it normally equals our request id
			}
			break
		}
		if e := v.failure(status, code, msg, logID, data); !e.Retryable() || attempt == 2 {
			return nil, e
		} else {
			err = e
		}
	}
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(v.maxWait)
	wait := v.poll
	transient := 0 // consecutive "try the query again" answers
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < 5*v.poll {
			wait += v.poll / 2
		}
		code, msg, logID, data, status, err = v.call(ctx, "/api/v3/auc/bigmodel/query", taskID, false, map[string]any{})
		if err != nil {
			if time.Now().After(deadline) {
				return nil, err
			}
			continue // a dropped connection while polling is not the task failing
		}
		if code == volcASRProcessing || code == volcASRQueued || code == volcASRDone {
			transient = 0
		}
		switch code {
		case volcASRDone:
			var out volcASRResult
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, fmt.Errorf("无法解析火山引擎识别结果：%v", err)
			}
			return out.result(), nil
		case volcASRSilent, "45000002": // silence, or audio with nothing to recognize
			return &Result{}, nil
		case volcASRProcessing, volcASRQueued:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("火山引擎识别了 %d 分钟还没有完成，请稍后重试（任务 %s）", int(v.maxWait.Minutes()), taskID)
			}
			continue
		}
		e := v.failure(status, code, msg, logID, data)
		// busy, a passing server error or "reading the result failed": the task is still there, so query again
		// (the documentation says not to resubmit). 55001010/55001011 mean the task itself failed.
		if (e.Code == "55000031" || e.Code == "55000000" || e.Code == "55000001" || e.Code == "" && status >= 500) && transient < 8 && time.Now().Before(deadline) {
			transient++
			continue
		}
		return nil, e
	}
}

func (o *volcASRResult) result() *Result {
	r := &Result{Text: strings.TrimSpace(o.Result.Text)}
	for _, u := range o.Result.Utterances {
		r.Segments = append(r.Segments, Segment{Text: u.Text, StartMs: u.StartTime, EndMs: u.EndTime})
		for _, w := range u.Words {
			r.Words = append(r.Words, Word{Text: w.Text, StartMs: w.StartTime, EndMs: w.EndTime})
		}
	}
	if len(r.Words) == 0 {
		r.Words = wordsFromSegments(r.Segments)
	}
	return r
}

// Check asks for the result of a task that does not exist: the service authenticates the key first, so an
// unknown-task answer proves the key works without recognising (or paying for) anything.
func (v *volcASR) Check(ctx context.Context) (string, error) {
	if v.key == "" {
		return "", errors.New("火山引擎语音识别账号缺少 API Key")
	}
	code, msg, logID, data, status, err := v.call(ctx, "/api/v3/auc/bigmodel/query", volc.RequestID(), false, map[string]any{})
	if err != nil {
		return "", err
	}
	e := v.failure(status, code, msg, logID, data)
	if status == 401 || status == 403 || e.Code == "45000010" || strings.Contains(e.Plain, "密钥") || strings.Contains(e.Plain, "开通") {
		return "", e
	}
	note := ""
	if !volc.Local(v.base) {
		note = "；识别时火山引擎会从本站下载音频，请确认 VB_PUBLIC_URL 是公网地址"
	}
	return "火山引擎录音文件识别密钥有效，模型 " + v.resource + "（检测时没有提交音频，不产生费用）" + note, nil
}
