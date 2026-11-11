package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOSS answers the object calls the SDK makes in path style (http://127.0.0.1/<bucket>/<key>).
type fakeOSS struct {
	mu      sync.Mutex
	objects map[string][]byte
	deny    bool
}

func (f *fakeOSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	signed := strings.HasPrefix(r.Header.Get("Authorization"), "OSS4-HMAC-SHA256 ") || r.URL.Query().Get("x-oss-signature-version") == "OSS4-HMAC-SHA256"
	if !signed || f.deny {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InvalidAccessKeyId</Code><Message>The OSS Access Key Id you provided does not exist in our records.</Message><RequestId>1</RequestId></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objects[key] = b
		w.Header().Set("ETag", `"x"`)
	case http.MethodGet, http.MethodHead:
		b, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>no</Message></Error>`)
			return
		}
		if cd := r.URL.Query().Get("response-content-disposition"); cd != "" {
			w.Header().Set("Content-Disposition", cd)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

type memIndex struct {
	mu sync.Mutex
	m  map[string]int64
}

func (x *memIndex) Lookup(k string) (int64, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	n, ok := x.m[k]
	return n, ok
}
func (x *memIndex) Record(k, _, _ string, n int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.m[k] = n
	return nil
}
func (x *memIndex) Forget(k string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.m, k)
	return nil
}

func TestStoreMirrorsToOSS(t *testing.T) {
	fake := &fakeOSS{objects: map[string][]byte{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	local, err := NewLocal(t.TempDir(), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(local)
	idx := &memIndex{m: map[string]int64{}}
	s.UseIndex(idx)
	remote, err := NewOSS(OSSConfig{Region: "cn-hangzhou", Endpoint: srv.URL, Bucket: "vb-test", AccessKeyID: "ak", AccessKeySecret: "sk", Prefix: "/books/"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetMirror(&Mirror{Remote: remote, Upload: true, Redirect: true})

	key := "studios/s1/voice/202611/a.wav"
	if _, err := s.PutBytes(key, []byte("RIFF-audio")); err != nil {
		t.Fatal(err)
	}
	if got := fake.objects["vb-test/books/"+key]; string(got) != "RIFF-audio" {
		t.Fatalf("object not uploaded under the prefix: %q (have %v)", got, keys(fake.objects))
	}
	if n, ok := idx.Lookup(key); !ok || n != 10 {
		t.Fatalf("upload not recorded: %d %v", n, ok)
	}
	// scratch files stay on this server
	s.PutBytes("tmp/imports/x.json", []byte("{}"))
	if _, ok := idx.Lookup("tmp/imports/x.json"); ok || len(fake.objects) != 1 {
		t.Fatal("tmp/ files must not be uploaded")
	}

	// downloads go to OSS through a signed URL, with the download name
	u, ok := s.RemoteURL(key, time.Hour, "第1章.wav")
	if !ok || !strings.HasPrefix(u, srv.URL+"/vb-test/books/"+key) || !strings.Contains(u, "x-oss-signature=") || !strings.Contains(u, "response-content-disposition=") {
		t.Fatalf("signed url %q", u)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "RIFF-audio" || !strings.Contains(resp.Header.Get("Content-Disposition"), "UTF-8''") {
		t.Fatalf("signed url served %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Disposition"))
	}

	// an old local copy whose OSS copy is confirmed is evicted, then downloaded again on first use
	old := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(local.Path(key), old, old)
	if n, freed := s.Evict(context.Background(), 7*24*time.Hour); n != 1 || freed != 10 {
		t.Fatalf("evicted %d files, %d bytes", n, freed)
	}
	if local.Exists(key) {
		t.Fatal("local copy should be gone")
	}
	if !s.Exists(key) || s.Size(key) != 10 {
		t.Fatal("a file kept in OSS still exists")
	}
	b, err := s.ReadAll(key)
	if err != nil || !bytes.Equal(b, []byte("RIFF-audio")) {
		t.Fatalf("restore: %q %v", b, err)
	}
	// a recently used file is kept
	if n, _ := s.Evict(context.Background(), 7*24*time.Hour); n != 0 {
		t.Fatal("a freshly downloaded file must not be evicted")
	}

	// files stored before OSS was turned on are uploaded by UploadExisting
	local.PutBytes("studios/s1/cover/202611/old.png", []byte("png"))
	files, _, unsynced, _ := s.LocalUsage()
	if files != 2 || unsynced != 1 {
		t.Fatalf("usage: %d files, %d unsynced", files, unsynced)
	}
	var last SyncProgress
	if err := s.UploadExisting(context.Background(), func(p SyncProgress) { last = p }); err != nil || last.Done != 1 || last.Failed != 0 {
		t.Fatalf("sync: %+v %v", last, err)
	}
	if _, ok := fake.objects["vb-test/books/studios/s1/cover/202611/old.png"]; !ok {
		t.Fatal("existing file not uploaded")
	}

	// deleting removes both copies
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.objects["vb-test/books/"+key]; ok || s.Exists(key) {
		t.Fatal("delete must remove the OSS copy and the record")
	}

	// proxy mode: no redirect, the server streams its (restored) local copy
	s.SetMirror(&Mirror{Remote: remote, Upload: true, Redirect: false})
	if _, ok := s.RemoteURL("studios/s1/cover/202611/old.png", time.Hour, ""); ok {
		t.Fatal("proxy mode must not hand out OSS URLs")
	}
}

func TestStoreKeepsWorkingWhenOSSFails(t *testing.T) {
	fake := &fakeOSS{objects: map[string][]byte{}, deny: true}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	local, _ := NewLocal(t.TempDir(), []byte("secret"))
	s := NewStore(local)
	idx := &memIndex{m: map[string]int64{}}
	s.UseIndex(idx)
	remote, _ := NewOSS(OSSConfig{Region: "cn-hangzhou", Endpoint: srv.URL, Bucket: "vb-test", AccessKeyID: "bad", AccessKeySecret: "sk"})
	s.SetMirror(&Mirror{Remote: remote, Upload: true, Redirect: true})
	key := "studios/s1/voice/202611/b.wav"
	if _, err := s.PutBytes(key, []byte("abc")); err != nil {
		t.Fatalf("a failed upload must not fail the write: %v", err)
	}
	if b, _ := s.ReadAll(key); string(b) != "abc" {
		t.Fatal("the local copy must stay usable")
	}
	if _, ok := idx.Lookup(key); ok {
		t.Fatal("a failed upload must not be recorded")
	}
	if _, ok := s.RemoteURL(key, time.Hour, ""); ok {
		t.Fatal("files not in OSS are served locally")
	}
	err := remote.Upload(context.Background(), "x", local.Path(key), "audio/wav")
	if msg := remote.Explain(err); !strings.Contains(msg, "AccessKey ID") {
		t.Fatalf("explain: %q", msg)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOSSURLs(t *testing.T) {
	for _, c := range []struct {
		cfg  OSSConfig
		want string
	}{
		{OSSConfig{Region: "cn-hangzhou", Bucket: "my-books"}, "https://my-books.oss-cn-hangzhou.aliyuncs.com/a/b.mp3?"},
		// uploads over the internal network, browsers still get the public host
		{OSSConfig{Region: "cn-shanghai", Bucket: "my-books", Internal: true}, "https://my-books.oss-cn-shanghai.aliyuncs.com/a/b.mp3?"},
		{OSSConfig{Region: "cn-hangzhou", Bucket: "my-books", CustomDomain: "https://media.example.com/"}, "https://media.example.com/a/b.mp3?"},
	} {
		c.cfg.AccessKeyID, c.cfg.AccessKeySecret = "ak", "sk"
		o, err := NewOSS(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		u, err := o.SignGet(context.Background(), "a/b.mp3", time.Hour, "")
		if err != nil || !strings.HasPrefix(u, c.want) || !strings.Contains(u, "x-oss-expires=3600") {
			t.Errorf("%+v: %q %v", c.cfg, u, err)
		}
	}
	if _, err := NewOSS(OSSConfig{Region: "cn-hangzhou", Bucket: "My_Books", AccessKeyID: "ak", AccessKeySecret: "sk"}); err == nil {
		t.Error("invalid bucket names are refused")
	}
}
