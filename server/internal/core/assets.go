package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/audio"
	"voicebook/internal/model"
)

type AssetInput struct {
	Scope       string
	StudioID    string
	OwnerUserID string
	Kind        string
	Name        string
	Ext         string
	Mime        string
	Origin      string
	License     string
	Tags        []string
	Listed      bool
	CreatedBy   string
	Description string
	Meta        map[string]any
	NoProbe     bool
	NoPeaks     bool
}

var audioExt = map[string]bool{"wav": true, "mp3": true, "m4a": true, "aac": true, "flac": true, "ogg": true, "opus": true, "webm": true, "wma": true, "amr": true, "m4b": true}

func IsAudioExt(ext string) bool { return audioExt[strings.ToLower(strings.TrimPrefix(ext, "."))] }

func (a *App) storageKey(in AssetInput, id string) string {
	owner := in.StudioID
	if owner == "" {
		owner = "platform"
	}
	return fmt.Sprintf("studios/%s/%s/%s/%s.%s", owner, in.Kind, now().Format("200601"), id, strings.TrimPrefix(strings.ToLower(in.Ext), "."))
}

// SaveAsset streams content into storage and creates the asset row (probing audio metadata and peaks).
func (a *App) SaveAsset(ctx context.Context, in AssetInput, r io.Reader) (*model.Asset, error) {
	if in.Scope == "" {
		in.Scope = model.ScopeStudio
	}
	if in.Origin == "" {
		in.Origin = "upload"
	}
	ext := strings.TrimPrefix(strings.ToLower(in.Ext), ".")
	if ext == "" {
		ext = "bin"
	}
	in.Ext = ext
	if in.Mime == "" {
		in.Mime = mime.TypeByExtension("." + ext)
		if in.Mime == "" {
			in.Mime = "application/octet-stream"
		}
	}
	as := &model.Asset{
		Scope: in.Scope, StudioID: in.StudioID, OwnerUserID: in.OwnerUserID, Kind: in.Kind, Name: in.Name, Mime: in.Mime,
		Tags: model.Strings(in.Tags...), License: in.License, Origin: in.Origin, Listed: in.Listed, CreatedBy: in.CreatedBy,
		Description: in.Description, Meta: model.Map{V: in.Meta},
	}
	if as.Meta.V == nil {
		as.Meta.V = map[string]any{}
	}
	as.ID = model.NewID()
	as.StorageKey = a.storageKey(in, as.ID)
	size, sum, err := a.Store.Put(as.StorageKey, r)
	if err != nil {
		return nil, err
	}
	as.Size, as.Checksum = size, sum
	if in.StudioID != "" {
		if err := a.checkQuota(in.StudioID, size); err != nil {
			a.Store.Delete(as.StorageKey)
			return nil, err
		}
	}
	if IsAudioExt(ext) && !in.NoProbe {
		info, err := a.FF.Probe(ctx, a.Store.Path(as.StorageKey))
		if err != nil {
			a.Store.Delete(as.StorageKey)
			return nil, Bad("无法识别音频文件：%v", err)
		}
		as.DurationMs, as.SampleRate, as.Channels = info.DurationMs, info.SampleRate, info.Channels
		if !in.NoPeaks {
			a.computePeaks(ctx, as)
		}
	}
	if err := a.DB.Create(as).Error; err != nil {
		a.Store.Delete(as.StorageKey)
		return nil, err
	}
	if in.StudioID != "" {
		a.DB.Model(&model.Studio{}).Where("id = ?", in.StudioID).Update("storage_used", gorm.Expr("storage_used + ?", size))
	}
	return as, nil
}

func (a *App) SaveAssetBytes(ctx context.Context, in AssetInput, data []byte) (*model.Asset, error) {
	return a.SaveAsset(ctx, in, bytes.NewReader(data))
}

func (a *App) SaveAssetFile(ctx context.Context, in AssetInput, path string) (*model.Asset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if in.Ext == "" {
		in.Ext = filepath.Ext(path)
	}
	return a.SaveAsset(ctx, in, f)
}

func (a *App) computePeaks(ctx context.Context, as *model.Asset) {
	pcm, err := a.FF.DecodePCM(ctx, a.Store.Path(as.StorageKey), 8000, 1, "")
	if err != nil {
		return
	}
	peaks := audio.Peaks(pcm, 50)
	b, _ := json.Marshal(peaks)
	key := "peaks/" + as.ID + ".json"
	if _, err := a.Store.PutBytes(key, b); err == nil {
		as.PeaksKey = key
	}
}

// AssetPeaks returns waveform peaks (computing them lazily).
func (a *App) AssetPeaks(ctx context.Context, as *model.Asset) []float32 {
	if as.PeaksKey == "" || !a.Store.Exists(as.PeaksKey) {
		a.computePeaks(ctx, as)
		if as.PeaksKey != "" {
			a.DB.Model(as).Update("peaks_key", as.PeaksKey)
		}
	}
	var p []float32
	if b, err := a.Store.ReadAll(as.PeaksKey); err == nil {
		json.Unmarshal(b, &p)
	}
	return p
}

// AssetURL returns a signed download/stream path.
func (a *App) AssetURL(as *model.Asset) string {
	if as == nil || as.StorageKey == "" {
		return ""
	}
	return a.Store.SignedPath(as.StorageKey, 12*time.Hour, "")
}

func (a *App) AssetDownloadURL(as *model.Asset, filename string) string {
	return a.Store.SignedPath(as.StorageKey, 12*time.Hour, filename)
}

func (a *App) AssetURLByID(id string) string {
	if id == "" {
		return ""
	}
	var as model.Asset
	if a.DB.Select("id", "storage_key").First(&as, "id = ?", id).Error != nil {
		return ""
	}
	return a.AssetURL(&as)
}

func (a *App) AssetPath(id string) (string, *model.Asset, error) {
	var as model.Asset
	if err := a.DB.First(&as, "id = ?", id).Error; err != nil {
		return "", nil, NotFound("素材")
	}
	return a.Store.Path(as.StorageKey), &as, nil
}

func (a *App) DeleteAssetFile(as *model.Asset) {
	a.Store.Delete(as.StorageKey)
	if as.PeaksKey != "" {
		a.Store.Delete(as.PeaksKey)
	}
	if as.StudioID != "" {
		a.DB.Model(&model.Studio{}).Where("id = ?", as.StudioID).Update("storage_used", gorm.Expr("CASE WHEN storage_used > ? THEN storage_used - ? ELSE 0 END", as.Size, as.Size))
	}
}

func (a *App) StorageLimit(studioID string) int64 {
	var st model.Studio
	if a.DB.First(&st, "id = ?", studioID).Error != nil {
		return 0
	}
	if st.StorageLimit > 0 {
		return st.StorageLimit
	}
	var p model.Plan
	if st.PlanID != "" && a.DB.First(&p, "id = ?", st.PlanID).Error == nil && p.StorageGB > 0 {
		return int64(p.StorageGB) << 30
	}
	return 5 << 30
}

func (a *App) checkQuota(studioID string, add int64) error {
	var st model.Studio
	if a.DB.Select("storage_used").First(&st, "id = ?", studioID).Error != nil {
		return nil
	}
	limit := a.StorageLimit(studioID)
	if limit > 0 && st.StorageUsed+add > limit {
		return &Error{Status: 413, Code: "storage_full", Msg: fmt.Sprintf("存储空间不足（已用 %.1f GB / %.1f GB），请清理素材或升级套餐", float64(st.StorageUsed)/(1<<30), float64(limit)/(1<<30))}
	}
	return nil
}

// AssetView is the API representation of an asset.
func (a *App) AssetView(as *model.Asset) map[string]any {
	return map[string]any{
		"id": as.ID, "scope": as.Scope, "studioId": as.StudioID, "ownerUserId": as.OwnerUserID, "kind": as.Kind, "name": as.Name,
		"mime": as.Mime, "size": as.Size, "durationMs": as.DurationMs, "sampleRate": as.SampleRate, "channels": as.Channels,
		"tags": as.Tags.V, "license": as.License, "origin": as.Origin, "listed": as.Listed, "description": as.Description,
		"meta": as.Meta.V, "createdAt": as.CreatedAt, "createdBy": as.CreatedBy, "url": a.AssetURL(as),
	}
}
