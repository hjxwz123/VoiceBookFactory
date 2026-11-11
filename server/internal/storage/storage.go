// Package storage stores binary objects. Local writes to disk and serves files through HMAC-signed
// URLs; Store (store.go) adds an optional remote copy in Aliyun OSS on top of it.
package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Local struct {
	Root   string
	secret []byte
}

func NewLocal(root string, secret []byte) (*Local, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Local{Root: root, secret: secret}, nil
}

func clean(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+key)), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", errors.New("invalid storage key")
	}
	return key, nil
}

// Path returns the absolute local path for a key (used by ffmpeg).
func (s *Local) Path(key string) string {
	k, err := clean(key)
	if err != nil {
		return filepath.Join(s.Root, "_invalid")
	}
	return filepath.Join(s.Root, filepath.FromSlash(k))
}

// Put streams r into key and returns size and sha256.
func (s *Local) Put(key string, r io.Reader) (int64, string, error) {
	k, err := clean(key)
	if err != nil {
		return 0, "", err
	}
	p := filepath.Join(s.Root, filepath.FromSlash(k))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, "", err
	}
	tmp := p + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return 0, "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Local) PutBytes(key string, b []byte) (string, error) {
	_, sum, err := s.Put(key, strings.NewReader(string(b)))
	return sum, err
}

// PutFile moves (or copies) a local file into storage.
func (s *Local) PutFile(key, src string) (int64, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	return s.Put(key, f)
}

func (s *Local) Open(key string) (*os.File, error) { return os.Open(s.Path(key)) }

func (s *Local) Exists(key string) bool {
	if key == "" {
		return false
	}
	_, err := os.Stat(s.Path(key))
	return err == nil
}

func (s *Local) Size(key string) int64 {
	st, err := os.Stat(s.Path(key))
	if err != nil {
		return 0
	}
	return st.Size()
}

func (s *Local) Delete(key string) error {
	if key == "" {
		return nil
	}
	err := os.Remove(s.Path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Local) ReadAll(key string) ([]byte, error) { return os.ReadFile(s.Path(key)) }

func (s *Local) sign(key string, exp int64, dl string) string {
	m := hmac.New(sha256.New, s.secret)
	fmt.Fprintf(m, "%s|%d|%s", key, exp, dl)
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// SignedPath returns a relative URL (/files/...) valid for ttl. filename forces a download name.
func (s *Local) SignedPath(key string, ttl time.Duration, filename string) string {
	if key == "" {
		return ""
	}
	exp := time.Now().Add(ttl).Unix()
	q := url.Values{}
	q.Set("e", strconv.FormatInt(exp, 10))
	if filename != "" {
		q.Set("dl", filename)
	}
	q.Set("s", s.sign(key, exp, filename))
	return "/files/" + key + "?" + q.Encode()
}

func (s *Local) Verify(key, exp, dl, sig string) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > e {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(key, e, dl)))
}

// TempDir creates a scratch directory under the storage root.
func (s *Local) TempDir(prefix string) (string, error) {
	base := filepath.Join(s.Root, "tmp")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, prefix)
}

// DirSize returns total bytes under a key prefix.
func (s *Local) DirSize(prefix string) int64 {
	var total int64
	filepath.Walk(s.Path(prefix), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}
