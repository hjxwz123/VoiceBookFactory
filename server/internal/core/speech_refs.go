package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"voicebook/internal/secure"
	"voicebook/internal/storage"
)

// shareTTL is how long a speech service may download a recording published for it.
const shareTTL = 3 * time.Hour

// publishTemp makes a local recording downloadable from the internet for shareTTL, for speech services that
// only accept an audio URL (Volcengine recording-file recognition). With object storage configured the file goes
// there (any instance of a cluster can serve it); otherwise this server serves it under a signed link. cleanup
// removes it again once the service has the result.
func (a *App) publishTemp(ctx context.Context, path string) (string, func(), error) {
	ext := strings.ToLower(filepath.Ext(path))
	name := secure.Token(16) + ext
	if m := a.Store.Mirror(); m != nil && m.Remote != nil {
		key := "tmp-share/" + name
		uctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := m.Remote.Upload(uctx, key, path, storage.ContentType(key))
		cancel()
		if err == nil {
			if u, err := m.Remote.SignGet(ctx, key, shareTTL, ""); err == nil {
				return u, func() {
					dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := m.Remote.Delete(dctx, key); err != nil {
						slog.Warn("remove shared recording from object storage", "key", key, "err", err)
					}
				}, nil
			}
			_ = m.Remote.Delete(context.Background(), key)
		}
		slog.Warn("sharing a recording through object storage failed; serving it from this server", "err", err)
	}
	key := "tmp/share/" + name // scratch space: never mirrored, swept by cleanupTemp
	if _, _, err := a.Store.Local.PutFile(key, path); err != nil {
		return "", nil, err
	}
	return a.Cfg.PublicURL + a.Store.SignedPath(key, shareTTL, ""), func() { _ = a.Store.Local.Delete(key) }, nil
}

// speechRefs keeps reference recordings for voices that a platform imitates from a sample sent with every
// request (Volcengine audio generation): cloned and designed voices. The files are ordinary stored files under
// voice-refs/, mirrored to object storage like any other asset.
type speechRefs struct{ a *App }

func (a *App) refStore() speechRefs { return speechRefs{a} }

func refKey(id string) (string, error) {
	if !strings.HasPrefix(id, "voice-refs/") || strings.Contains(id, "..") {
		return "", errors.New("无效的参考录音")
	}
	return id, nil
}

func (r speechRefs) PutRef(_ context.Context, name string, data []byte) (string, error) {
	clean := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			return c
		}
		return '-'
	}, name)
	key := fmt.Sprintf("voice-refs/%s-%s.wav", clean, secure.Token(6))
	if _, err := r.a.Store.PutBytes(key, data); err != nil {
		return "", err
	}
	return key, nil
}

func (r speechRefs) GetRef(_ context.Context, id string) ([]byte, error) {
	key, err := refKey(id)
	if err != nil {
		return nil, err
	}
	if !r.a.Store.Exists(key) {
		return nil, errors.New("参考录音已被删除，请重新克隆或设计这个音色")
	}
	return r.a.Store.ReadAll(key)
}

func (r speechRefs) DeleteRef(_ context.Context, id string) error {
	key, err := refKey(id)
	if err != nil {
		return err
	}
	return r.a.Store.Delete(key)
}
