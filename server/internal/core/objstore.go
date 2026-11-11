package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/model"
	"voicebook/internal/secure"
	"voicebook/internal/storage"
)

// StorageSettings chooses where uploaded and generated files live. Files are always written to this
// server first; with Aliyun OSS on they are also uploaded there, and OSS becomes the copy that counts.
type StorageSettings struct {
	Driver        string      `json:"driver"`        // local | oss
	OSS           OSSSettings `json:"oss"`           // kept when switching back to local, so files already in OSS stay readable
	ServeMode     string      `json:"serveMode"`     // redirect: browsers download from OSS; proxy: through this server
	KeepLocalDays int         `json:"keepLocalDays"` // local copies of files in OSS untouched this long are removed (0 = keep)
}

type OSSSettings struct {
	Region       string `json:"region"`
	Endpoint     string `json:"endpoint"`
	Internal     bool   `json:"internal"`
	Bucket       string `json:"bucket"`
	AccessKeyID  string `json:"accessKeyId"`
	SecretEnc    string `json:"secretEnc"`
	Prefix       string `json:"prefix"`
	CustomDomain string `json:"customDomain"`
}

func init() {
	settingDefaults["storage"] = StorageSettings{Driver: "local", ServeMode: "redirect", KeepLocalDays: 7}
}

func (a *App) StorageSettings() StorageSettings {
	var s StorageSettings
	a.Setting("storage", &s)
	return s
}

func (s OSSSettings) config(secret string) storage.OSSConfig {
	return storage.OSSConfig{Region: s.Region, Endpoint: s.Endpoint, Internal: s.Internal, Bucket: s.Bucket, AccessKeyID: strings.TrimSpace(s.AccessKeyID), AccessKeySecret: strings.TrimSpace(secret), Prefix: s.Prefix, CustomDomain: s.CustomDomain}
}

// ApplyStorage puts the saved storage settings into effect.
func (a *App) ApplyStorage() {
	if a.Store == nil {
		return
	}
	s := a.StorageSettings()
	if s.OSS.Bucket == "" || s.OSS.SecretEnc == "" {
		a.Store.SetMirror(nil)
		return
	}
	remote, err := storage.NewOSS(s.OSS.config(a.Decrypt(s.OSS.SecretEnc)))
	if err != nil {
		slog.Warn("object storage settings are incomplete", "err", err)
		a.Store.SetMirror(nil)
		return
	}
	a.Store.SetMirror(&storage.Mirror{Remote: remote, Upload: s.Driver == "oss", Redirect: s.ServeMode != "proxy"})
}

// objectIndex keeps the record of remote copies in the stored_objects table.
type objectIndex struct{ db *gorm.DB }

func (x objectIndex) Lookup(key string) (int64, bool) {
	var rows []model.StoredObject
	x.db.Select("size").Where("object_key = ?", key).Limit(1).Find(&rows)
	if len(rows) == 0 {
		return 0, false
	}
	return rows[0].Size, true
}

func (x objectIndex) Record(key, backend, bucket string, size int64) error {
	return x.db.Save(&model.StoredObject{Key: key, Backend: backend, Bucket: bucket, Size: size, CreatedAt: time.Now().UTC()}).Error
}

func (x objectIndex) Forget(key string) error {
	return x.db.Where("object_key = ?", key).Delete(&model.StoredObject{}).Error
}

// StorageCheckStep is one step of a connection test.
type StorageCheckStep struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// CheckStorage tests OSS settings end to end: upload, read back, a signed browser URL, delete.
func (a *App) CheckStorage(ctx context.Context, s StorageSettings, secret string) ([]StorageCheckStep, bool) {
	var steps []StorageCheckStep
	fail := func(name, msg string) ([]StorageCheckStep, bool) {
		return append(steps, StorageCheckStep{Name: name, Message: msg}), false
	}
	o, err := storage.NewOSS(s.OSS.config(secret))
	if err != nil {
		return fail("检查填写", err.Error())
	}
	dir, err := a.Store.TempDir("osscheck")
	if err != nil {
		return fail("准备测试文件", err.Error())
	}
	defer os.RemoveAll(dir)
	body := []byte("voicebook storage check " + secure.Token(8))
	src := filepath.Join(dir, "probe.txt")
	os.WriteFile(src, body, 0o644)
	key := "voicebook-check/" + secure.Token(6) + ".txt"
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := o.Upload(c, key, src, "text/plain"); err != nil {
		return fail("上传测试文件", o.Explain(err))
	}
	steps = append(steps, StorageCheckStep{Name: "上传测试文件", OK: true})
	defer o.Delete(context.Background(), key)

	back := filepath.Join(dir, "back.txt")
	if err := o.Download(c, key, back); err != nil {
		return fail("读取测试文件", o.Explain(err))
	}
	if got, _ := os.ReadFile(back); !bytes.Equal(got, body) {
		return fail("读取测试文件", "读回的内容和上传的不一致")
	}
	steps = append(steps, StorageCheckStep{Name: "读取测试文件", OK: true})

	if s.ServeMode != "proxy" {
		u, err := o.SignGet(c, key, 5*time.Minute, "")
		if err != nil {
			return fail("浏览器直连 OSS", o.Explain(err))
		}
		req, _ := http.NewRequestWithContext(c, http.MethodGet, u, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			msg := "打不开 OSS 的临时链接：" + err.Error()
			if s.OSS.CustomDomain != "" {
				msg += "。请确认自定义域名已解析并绑定到这个存储桶"
			}
			return fail("浏览器直连 OSS", msg)
		}
		got, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
			msg := fmt.Sprintf("临时链接返回 HTTP %d", resp.StatusCode)
			if s.OSS.CustomDomain != "" {
				msg += "。自定义域名或 CDN 需要能把带签名的请求转给私有存储桶（CDN 请开启「阿里云 OSS 私有 Bucket 回源」），也可以把访问方式改为「经服务器转发」"
			}
			return fail("浏览器直连 OSS", msg)
		}
		steps = append(steps, StorageCheckStep{Name: "浏览器直连 OSS", OK: true, Message: "访问链接由服务器签名，1 小时内有效"})
	}
	if err := o.Delete(c, key); err != nil {
		return fail("删除测试文件", o.Explain(err))
	}
	steps = append(steps, StorageCheckStep{Name: "删除测试文件", OK: true})
	return steps, true
}

// storageSync tracks the background upload of files that were stored before OSS was turned on.
type storageSync struct {
	mu       sync.Mutex
	running  bool
	progress storage.SyncProgress
	started  time.Time
	finished *time.Time
}

var objSync storageSync

// StartStorageSync uploads every local file that has no OSS copy yet, in the background.
func (a *App) StartStorageSync() error {
	m := a.Store.Mirror()
	if m == nil || !m.Upload {
		return Bad("请先开启阿里云 OSS 并保存设置")
	}
	objSync.mu.Lock()
	if objSync.running {
		objSync.mu.Unlock()
		return Conflict("正在上传，请等这一轮结束")
	}
	objSync.running, objSync.started, objSync.finished, objSync.progress = true, now(), nil, storage.SyncProgress{}
	objSync.mu.Unlock()
	go func() {
		err := a.Store.UploadExisting(context.Background(), func(p storage.SyncProgress) {
			objSync.mu.Lock()
			objSync.progress = p
			objSync.mu.Unlock()
		})
		objSync.mu.Lock()
		t := now()
		objSync.running, objSync.finished = false, &t
		if err != nil {
			objSync.progress.LastError = err.Error()
		}
		objSync.mu.Unlock()
		slog.Info("uploaded existing files to object storage", "done", objSync.progress.Done, "failed", objSync.progress.Failed, "err", err)
	}()
	return nil
}

// StorageStatus describes where files are now, for the admin console.
func (a *App) StorageStatus() map[string]any {
	files, bytes, unsynced, unsyncedBytes := a.Store.LocalUsage()
	var remote struct {
		N     int64
		Bytes int64
	}
	a.DB.Model(&model.StoredObject{}).Select("count(*) as n, coalesce(sum(size), 0) as bytes").Scan(&remote)
	objSync.mu.Lock()
	sync := map[string]any{"running": objSync.running, "total": objSync.progress.Total, "done": objSync.progress.Done, "failed": objSync.progress.Failed, "bytes": objSync.progress.Bytes, "lastError": objSync.progress.LastError, "finishedAt": objSync.finished}
	objSync.mu.Unlock()
	m := a.Store.Mirror()
	return map[string]any{
		"localFiles": files, "localBytes": bytes, "unsyncedFiles": unsynced, "unsyncedBytes": unsyncedBytes,
		"remoteFiles": remote.N, "remoteBytes": remote.Bytes, "sync": sync,
		"active": m != nil && m.Upload, "readable": m != nil,
	}
}

// evictLocalCopies frees disk space: local copies of files that are safely in OSS and have not been
// used for the configured number of days.
func (a *App) evictLocalCopies() {
	s := a.StorageSettings()
	if s.Driver != "oss" || s.KeepLocalDays <= 0 {
		return
	}
	n, freed := a.Store.Evict(context.Background(), time.Duration(s.KeepLocalDays)*24*time.Hour)
	if n > 0 {
		slog.Info("removed local copies of files kept in object storage", "files", n, "bytes", freed)
	}
}
