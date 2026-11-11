package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSuno speaks the New API Suno task protocol.
type fakeSuno struct {
	mu        sync.Mutex
	submits   int
	fetches   int
	lastBody  map[string]any
	auth      string
	submitSeq []int // HTTP statuses to answer submits with, in order; 200 when exhausted
	pending   int   // fetches answered IN_PROGRESS before SUCCESS
	final     string
	failMsg   string
	clips     []map[string]any
	audio     []byte
	srv       *httptest.Server
}

func newFakeSuno(t *testing.T) *fakeSuno {
	f := &fakeSuno{final: "SUCCESS", pending: 2, audio: append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 2048)...)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.clips = []map[string]any{f.clip("c1", "雾港晨曦", "complete"), f.clip("c2", "雾港晨曦", "complete")}
	return f
}

func (f *fakeSuno) clip(id, title, status string) map[string]any {
	return map[string]any{"id": id, "title": title, "status": status, "audio_url": f.srv.URL + "/audio/" + id + ".mp3", "image_url": f.srv.URL + "/img/" + id + ".png",
		"model_name": "chirp-v4", "metadata": map[string]any{"tags": "piano, calm", "prompt": "[Instrumental]", "duration": 132.5, "error_message": nil}}
}

func (f *fakeSuno) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/audio/") {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(f.audio)
		return
	}
	if r.Header.Get("Authorization") != "Bearer sk-test" {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"无效的令牌","type":"new_api_error"}}`)
		return
	}
	f.auth = r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/suno/submit/music":
		f.submits++
		b, _ := io.ReadAll(r.Body)
		f.lastBody = map[string]any{}
		json.Unmarshal(b, &f.lastBody)
		if len(f.submitSeq) > 0 {
			st := f.submitSeq[0]
			f.submitSeq = f.submitSeq[1:]
			if st != 200 {
				w.WriteHeader(st)
				fmt.Fprint(w, `{"code":"error","message":"upstream busy"}`)
				return
			}
		}
		fmt.Fprint(w, `{"code":"success","message":"","data":"task-123"}`)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/suno/fetch/"):
		f.fetches++
		id := strings.TrimPrefix(r.URL.Path, "/suno/fetch/")
		if id != "task-123" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"code":"task_not_exist","message":"task_not_exist","data":null}`)
			return
		}
		status, progress := "IN_PROGRESS", "40%"
		clips := []map[string]any{}
		if f.fetches > f.pending {
			status, progress, clips = f.final, "100%", f.clips
		}
		json.NewEncoder(w).Encode(map[string]any{"code": "success", "data": map[string]any{"task_id": id, "action": "MUSIC", "status": status, "fail_reason": f.failMsg, "progress": progress, "data": clips}})
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, "404 page not found")
	}
}

func fastSuno(t *testing.T) {
	old := sunoPollUnit
	sunoPollUnit = time.Millisecond
	t.Cleanup(func() { sunoPollUnit = old })
}

func TestSunoDescriptionModeMakesTwoSongs(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	p, err := NewMusic(Account{Provider: "suno", BaseURL: f.srv.URL + "/v1/", APIKey: "sk-test", Model: "chirp-v4"})
	if err != nil {
		t.Fatal(err)
	}
	var task string
	files, err := p.(MusicSetProvider).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "舒缓的古风钢琴，适合回忆场景", Instrumental: true, OnTask: func(id string) { task = id }})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Ext != "mp3" || files[0].Mime != "audio/mpeg" {
		t.Fatalf("files %d %s %s", len(files), files[0].Ext, files[0].Mime)
	}
	if files[0].Meta["title"] != "雾港晨曦" || files[0].Meta["taskId"] != "task-123" || files[0].Meta["durationSec"] != 132.5 {
		t.Fatalf("meta %v", files[0].Meta)
	}
	if task != "task-123" {
		t.Fatalf("OnTask got %q", task)
	}
	b := f.lastBody
	if b["gpt_description_prompt"] != "舒缓的古风钢琴，适合回忆场景" || b["make_instrumental"] != true || b["mv"] != "chirp-v4" || b["tags"] != nil {
		t.Fatalf("submit body %v", b)
	}
	if f.submits != 1 || f.fetches != 3 {
		t.Fatalf("submits %d fetches %d", f.submits, f.fetches)
	}
}

func TestSunoCustomModeSendsLyricsStyleAndTitle(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	p := newSuno(Account{Provider: "suno", BaseURL: f.srv.URL, APIKey: "sk-test"})
	if _, err := p.GenerateMusicSet(context.Background(), MusicRequest{Prompt: "片头曲", Lyrics: "[Verse]\n雾港的清晨", Style: "古风, 女声, 钢琴", Title: "雾港来信"}); err != nil {
		t.Fatal(err)
	}
	b := f.lastBody
	if b["prompt"] != "[Verse]\n雾港的清晨" || b["tags"] != "古风, 女声, 钢琴" || b["title"] != "雾港来信" || b["make_instrumental"] != false || b["gpt_description_prompt"] != nil {
		t.Fatalf("submit body %v", b)
	}
	if _, ok := b["mv"]; ok {
		t.Fatal("mv sent although no model was set")
	}
	// an instrumental in custom mode sends no lyrics
	if _, err := p.GenerateMusicSet(context.Background(), MusicRequest{Prompt: "史诗战斗", Style: "epic orchestral", Lyrics: "不应发送", Instrumental: true}); err != nil {
		t.Fatal(err)
	}
	if f.lastBody["prompt"] != "" || f.lastBody["title"] != "史诗战斗" {
		t.Fatalf("instrumental custom body %v", f.lastBody)
	}
	// lyrics alone do not switch an instrumental out of description mode
	if _, err := p.GenerateMusicSet(context.Background(), MusicRequest{Prompt: "雨夜", Lyrics: "不应发送", Instrumental: true}); err != nil {
		t.Fatal(err)
	}
	if f.lastBody["gpt_description_prompt"] != "雨夜" || f.lastBody["tags"] != nil {
		t.Fatalf("instrumental description body %v", f.lastBody)
	}
}

func TestSunoFailedTaskExplainsWhy(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	f.final, f.failMsg = "FAILURE", "歌词包含敏感内容"
	_, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "歌词包含敏感内容") {
		t.Fatalf("err %v", err)
	}
}

func TestSunoKeepsTheCompleteClipWhenOneFails(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	f.final = "IN_PROGRESS" // the relay has not flipped the task yet, but every clip has settled
	bad := f.clip("c2", "x", "error")
	bad["metadata"].(map[string]any)["error_message"] = "content flagged"
	f.clips = []map[string]any{f.clip("c1", "雾港晨曦", "complete"), bad}
	files, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"})
	if err != nil || len(files) != 1 || files[0].Meta["clipId"] != "c1" {
		t.Fatalf("files %d err %v", len(files), err)
	}
}

func TestSunoBadKeyIsNotRetried(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	_, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-wrong"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "密钥无效") || !strings.Contains(err.Error(), "无效的令牌") {
		t.Fatalf("err %v", err)
	}
}

func TestSunoRetriesOnlyRefusedSubmits(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	f.submitSeq = []int{503}
	if _, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if f.submits != 2 {
		t.Fatalf("submits %d, want 2", f.submits)
	}
	f2 := newFakeSuno(t)
	f2.submitSeq = []int{500}
	if _, err := newSuno(Account{BaseURL: f2.srv.URL, APIKey: "sk-test"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"}); err == nil || f2.submits != 1 {
		t.Fatalf("a 500 may have started work and must not be resubmitted: submits %d err %v", f2.submits, err)
	}
}

func TestSunoGivesUpAfterMaxWait(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	f.pending = 1 << 30
	_, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test", Config: map[string]any{"maxWaitSec": 30, "pollSec": 1}}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "仍未完成") || !strings.Contains(err.Error(), "task-123") {
		t.Fatalf("err %v", err)
	}
}

func TestSunoRejectsNonAudioDownloads(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	f.audio = []byte("<html>blocked</html>")
	_, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test"}).GenerateMusicSet(context.Background(), MusicRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "不是音频") {
		t.Fatalf("err %v", err)
	}
}

func TestSunoCheckCostsNothing(t *testing.T) {
	fastSuno(t)
	f := newFakeSuno(t)
	msg, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-test", Model: "chirp-auk"}).Check(context.Background())
	if err != nil || !strings.Contains(msg, "chirp-auk") || !strings.Contains(msg, "不产生费用") {
		t.Fatalf("msg %q err %v", msg, err)
	}
	if _, err := newSuno(Account{BaseURL: f.srv.URL, APIKey: "sk-wrong"}).Check(context.Background()); err == nil || !strings.Contains(err.Error(), "密钥无效") {
		t.Fatalf("bad key: %v", err)
	}
	// an address without the Suno routes (e.g. a plain OpenAI endpoint)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); fmt.Fprint(w, "404 page not found") }))
	defer plain.Close()
	if _, err := newSuno(Account{BaseURL: plain.URL, APIKey: "sk-test"}).Check(context.Background()); err == nil || !strings.Contains(err.Error(), "根地址") {
		t.Fatalf("plain: %v", err)
	}
	if _, err := newSuno(Account{APIKey: "sk-test"}).Check(context.Background()); err == nil || !strings.Contains(err.Error(), "接口地址") {
		t.Fatalf("empty base: %v", err)
	}
	if f.submits != 0 {
		t.Fatalf("check submitted %d songs", f.submits)
	}
}

func TestMusicFeatures(t *testing.T) {
	if f := MusicFeaturesOf("suno"); !f.Vocals || !f.Lyrics || f.Duration || f.PerRequest != 2 {
		t.Fatalf("suno %+v", f)
	}
	if f := MusicFeaturesOf("placeholder"); f.Vocals || !f.Duration || f.PerRequest != 1 {
		t.Fatalf("placeholder %+v", f)
	}
}
