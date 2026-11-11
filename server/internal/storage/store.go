package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Remote is a durable object store (Aliyun OSS) that keeps a copy of the files on the local disk.
type Remote interface {
	Name() string   // "oss"
	Bucket() string // where the objects go, for the record
	Upload(ctx context.Context, key, path, contentType string) error
	Download(ctx context.Context, key, path string) error
	Stat(ctx context.Context, key string) (int64, error)
	Delete(ctx context.Context, key string) error
	// SignGet returns a time-limited URL a browser can fetch the object from; filename forces a download name.
	SignGet(ctx context.Context, key string, ttl time.Duration, filename string) (string, error)
}

// Index records which keys have a copy in the remote store.
type Index interface {
	Lookup(key string) (size int64, ok bool)
	Record(key, backend, bucket string, size int64) error
	Forget(key string) error
}

// Mirror is the remote configuration in effect.
type Mirror struct {
	Remote   Remote
	Upload   bool // new files are uploaded to the remote store
	Redirect bool // downloads go straight to the remote store through a signed URL instead of through this server
}

// Store is the storage the app works with. Every file is written to the local disk, which stays the
// working copy that ffmpeg reads. When a remote store is configured the file is also uploaded there as
// the durable copy, local copies can be evicted, and a file that only the remote store has is
// downloaded back the first time something needs it.
type Store struct {
	*Local
	index  Index
	mirror atomic.Pointer[Mirror]
	fetch  singleflight.Group
}

func NewStore(l *Local) *Store { return &Store{Local: l} }

// UseIndex sets where remote copies are recorded (a database table).
func (s *Store) UseIndex(x Index) { s.index = x }

// SetMirror switches the remote configuration; nil means local disk only.
func (s *Store) SetMirror(m *Mirror) { s.mirror.Store(m) }

func (s *Store) Mirror() *Mirror { return s.mirror.Load() }

// Mirrored reports whether a key belongs in the remote store (scratch files under tmp/ never do).
func Mirrored(key string) bool {
	k, err := clean(key)
	return err == nil && !strings.HasPrefix(k, "tmp/")
}

func (s *Store) lookup(key string) (int64, bool) {
	if s.index == nil || !Mirrored(key) {
		return 0, false
	}
	return s.index.Lookup(key)
}

// Put writes r to key on the local disk and, when uploads are on, to the remote store. A failed upload
// is logged and the file stays usable locally; the next "upload existing files" run picks it up.
func (s *Store) Put(key string, r io.Reader) (int64, string, error) {
	n, sum, err := s.Local.Put(key, r)
	if err != nil {
		return n, sum, err
	}
	if m := s.mirror.Load(); m != nil && m.Upload && m.Remote != nil && s.index != nil && Mirrored(key) {
		if err := s.upload(m.Remote, key, n); err != nil {
			slog.Warn("upload to object storage failed; the file stays on this server", "key", key, "err", err)
		}
	}
	return n, sum, nil
}

func transferTimeout(size int64) time.Duration {
	return 30*time.Second + time.Duration(size/(256<<10))*time.Second // at least 256 KB/s
}

func (s *Store) upload(r Remote, key string, size int64) error {
	k, _ := clean(key)
	ctx, cancel := context.WithTimeout(context.Background(), transferTimeout(size))
	defer cancel()
	if err := r.Upload(ctx, k, s.Local.Path(k), ContentType(k)); err != nil {
		return err
	}
	return s.index.Record(k, r.Name(), r.Bucket(), size)
}

func (s *Store) PutBytes(key string, b []byte) (string, error) {
	_, sum, err := s.Put(key, bytes.NewReader(b))
	return sum, err
}

func (s *Store) PutFile(key, src string) (int64, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	return s.Put(key, f)
}

// Path returns the local path of key, first downloading the file when only the remote store has it.
func (s *Store) Path(key string) string {
	p := s.Local.Path(key)
	st, err := os.Stat(p)
	switch {
	case err != nil && Mirrored(key):
		if err := s.restore(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("download from object storage failed", "key", key, "err", err)
		}
	case err == nil && time.Since(st.ModTime()) > 24*time.Hour:
		// the modification time doubles as "last used", which Evict goes by
		if m := s.mirror.Load(); m != nil && m.Upload {
			t := time.Now()
			os.Chtimes(p, t, t)
		}
	}
	return p
}

func (s *Store) restore(key string) error {
	k, err := clean(key)
	if err != nil {
		return err
	}
	_, err, _ = s.fetch.Do(k, func() (any, error) {
		p := s.Local.Path(k)
		if _, err := os.Stat(p); err == nil {
			return nil, nil
		}
		size, ok := s.lookup(k)
		if !ok {
			return nil, fs.ErrNotExist
		}
		m := s.mirror.Load()
		if m == nil || m.Remote == nil {
			return nil, errors.New("the file is in object storage, which is no longer configured")
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		tmp := p + ".fetch"
		ctx, cancel := context.WithTimeout(context.Background(), transferTimeout(size))
		defer cancel()
		if err := m.Remote.Download(ctx, k, tmp); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		return nil, os.Rename(tmp, p)
	})
	return err
}

func (s *Store) Open(key string) (*os.File, error) {
	return os.Open(s.Path(key))
}

func (s *Store) ReadAll(key string) ([]byte, error) {
	return os.ReadFile(s.Path(key))
}

func (s *Store) Exists(key string) bool {
	if s.Local.Exists(key) {
		return true
	}
	_, ok := s.lookup(key)
	return ok
}

func (s *Store) Size(key string) int64 {
	if n := s.Local.Size(key); n > 0 {
		return n
	}
	n, _ := s.lookup(key)
	return n
}

// Delete removes the local file and the remote copy.
func (s *Store) Delete(key string) error {
	err := s.Local.Delete(key)
	if _, ok := s.lookup(key); ok {
		k, _ := clean(key)
		if m := s.mirror.Load(); m != nil && m.Remote != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if rerr := m.Remote.Delete(ctx, k); rerr != nil {
				slog.Warn("delete from object storage failed", "key", k, "err", rerr)
			}
			cancel()
		}
		s.index.Forget(k)
	}
	return err
}

// RemoteURL returns a signed URL on the remote store when downloads are served from there and the
// remote store has the file.
func (s *Store) RemoteURL(key string, ttl time.Duration, filename string) (string, bool) {
	m := s.mirror.Load()
	if m == nil || !m.Redirect || m.Remote == nil {
		return "", false
	}
	if _, ok := s.lookup(key); !ok {
		return "", false
	}
	k, _ := clean(key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := m.Remote.SignGet(ctx, k, ttl, filename)
	if err != nil {
		slog.Warn("signing an object storage URL failed", "key", k, "err", err)
		return "", false
	}
	return u, true
}

// walk visits the stored files on the local disk (scratch space and partial writes excluded).
func (s *Store) walk(fn func(key, path string, info fs.FileInfo)) {
	filepath.Walk(s.Root, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(s.Root, p)
		if rerr != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if info.IsDir() {
			if key == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(key, ".part") || strings.HasSuffix(key, ".fetch") || !info.Mode().IsRegular() || !Mirrored(key) {
			return nil
		}
		fn(key, p, info)
		return nil
	})
}

// LocalUsage counts the stored files on the local disk and how many of them have no remote copy yet.
func (s *Store) LocalUsage() (files int, bytes int64, unsynced int, unsyncedBytes int64) {
	s.walk(func(key, _ string, info fs.FileInfo) {
		files++
		bytes += info.Size()
		if _, ok := s.lookup(key); !ok {
			unsynced++
			unsyncedBytes += info.Size()
		}
	})
	return
}

// SyncProgress reports an UploadExisting run.
type SyncProgress struct {
	Total, Done, Failed int
	Bytes               int64
	LastError           string
}

// UploadExisting uploads the local files that have no remote copy yet.
func (s *Store) UploadExisting(ctx context.Context, report func(SyncProgress)) error {
	m := s.mirror.Load()
	if m == nil || m.Remote == nil || s.index == nil {
		return errors.New("object storage is not configured")
	}
	type item struct {
		key  string
		size int64
	}
	var todo []item
	s.walk(func(key, _ string, info fs.FileInfo) {
		if _, ok := s.lookup(key); !ok {
			todo = append(todo, item{key, info.Size()})
		}
	})
	p := SyncProgress{Total: len(todo)}
	report(p)
	for _, it := range todo {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.upload(m.Remote, it.key, it.size); err != nil {
			p.Failed++
			p.LastError = err.Error()
		} else {
			p.Bytes += it.size
		}
		p.Done++
		report(p)
	}
	return nil
}

// Evict removes local copies untouched for longer than age whose remote copy is confirmed (same size
// in the remote store). They are downloaded again when needed.
func (s *Store) Evict(ctx context.Context, age time.Duration) (files int, freed int64) {
	m := s.mirror.Load()
	if m == nil || m.Remote == nil || s.index == nil || age <= 0 {
		return
	}
	cutoff := time.Now().Add(-age)
	s.walk(func(key, path string, info fs.FileInfo) {
		if ctx.Err() != nil || info.ModTime().After(cutoff) {
			return
		}
		size, ok := s.lookup(key)
		if !ok || size != info.Size() {
			return
		}
		c, cancel := context.WithTimeout(ctx, 15*time.Second)
		remote, err := m.Remote.Stat(c, key)
		cancel()
		if err != nil || remote != info.Size() {
			return
		}
		if os.Remove(path) == nil {
			files++
			freed += info.Size()
		}
	})
	return
}

// ContentType guesses a key's media type from its extension.
func ContentType(key string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(key))); t != "" {
		return t
	}
	return "application/octet-stream"
}
