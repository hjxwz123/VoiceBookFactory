package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/storage"
	"voicebook/internal/text"
)

// fakeLLM is an OpenAI-compatible chat server that answers like the platform's prompts expect. It
// records every request and can be told to fail some of them.
type fakeLLM struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []fakeCall
	fail  func(n int, system, user string) bool // n counts from 1
}

type fakeCall struct{ system, user string }

var (
	unitLine  = regexp.MustCompile(`(?m)^\[(\d+)\] \((引号内|叙述)\) (.*)$`)
	tableLine = regexp.MustCompile(`(?m)^(C\d+) ([^（\n]+)(?:（别名：([^；）]+))?`)
	saidBy    = regexp.MustCompile(`^(.+?)(说|笑道|问道|喊道)`)
)

func newFakeLLM(t *testing.T) *fakeLLM {
	f := &fakeLLM{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		var system, user string
		for _, m := range body.Messages {
			if m.Role == "system" {
				system += m.Content
			} else {
				user += m.Content
			}
		}
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{system, user})
		n := len(f.calls)
		fail := f.fail != nil && f.fail(n, system, user)
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
			return
		}
		content, _ := json.Marshal(fakeAnswer(system, user))
		json.NewEncoder(w).Encode(map[string]any{"id": "x", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": string(content)}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) script() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if strings.Contains(c.system, "画本师") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeLLM) count(marker string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c.system, marker) {
			n++
		}
	}
	return n
}

var fakeProfiles = map[string]CharacterInfo{
	"林黛玉": {Name: "林黛玉", Aliases: []string{"黛玉"}, Kind: "lead", Gender: "female", AgeGroup: "teen", Identity: "表小姐", Personality: "敏感", VoiceTraits: "清冷"},
	"贾宝玉": {Name: "贾宝玉", Aliases: []string{"宝玉"}, Kind: "lead", Gender: "male", AgeGroup: "teen", Identity: "公子", Personality: "温柔", VoiceTraits: "清亮"},
	"王大锤": {Name: "王大锤", Aliases: []string{"大锤"}, Kind: "minor", Gender: "male", AgeGroup: "middle", Identity: "送信的", Personality: "憨厚", VoiceTraits: "浑厚的中年男声", Profile: "给贾府送信的胖子。"},
}

func fakeAnswer(system, user string) any {
	switch {
	case strings.Contains(system, "有声书导演"): // character analysis
		var chars []CharacterInfo
		for name, p := range fakeProfiles {
			if strings.Contains(user, name) || strings.Contains(user, p.Aliases[0]) {
				p.Lines = 1
				chars = append(chars, p)
			}
		}
		return map[string]any{"characters": chars}
	case strings.Contains(system, "画本师"):
		codes := map[string]string{}
		for _, m := range tableLine.FindAllStringSubmatch(user, -1) {
			codes[strings.TrimSpace(m[2])] = m[1]
			for _, al := range strings.Split(m[3], "、") {
				if al != "" {
					codes[al] = m[1]
				}
			}
		}
		todo := user[strings.Index(user, "【需要标注的文本单元】"):]
		lines := unitLine.FindAllStringSubmatch(todo, -1)
		var units []map[string]any
		newChars := []CharacterInfo{}
		for i, m := range lines {
			id := 0
			fmt.Sscan(m[1], &id)
			if m[2] == "叙述" {
				tag := strings.HasSuffix(m[3], "说。") || strings.HasSuffix(m[3], "笑道。")
				units = append(units, map[string]any{"id": id, "type": "narration", "speaker": "NARRATOR", "emotion": "neutral", "intensity": 1, "cues": []any{}, "instruction": "", "confidence": 0.95, "speechTag": tag, "prons": []any{}})
				continue
			}
			speaker := "UNKNOWN"
			if i+1 < len(lines) {
				if sm := saidBy.FindStringSubmatch(lines[i+1][3]); sm != nil {
					name := sm[1]
					if code, ok := codes[name]; ok {
						speaker = code
					} else {
						speaker = "NEW:" + name
						if p, ok := fakeProfiles[name]; ok {
							newChars = append(newChars, p)
						}
					}
				}
			}
			units = append(units, map[string]any{"id": id, "type": "dialogue", "speaker": speaker, "emotion": "neutral", "intensity": 1, "cues": []any{}, "instruction": "", "confidence": 0.9, "speechTag": false, "prons": []any{}})
		}
		return map[string]any{"units": units, "sfx": []any{}, "newCharacters": newChars}
	case strings.Contains(system, "选角导演"):
		chars := regexp.MustCompile(`(?m)^(C\d+) `).FindAllStringSubmatch(user, -1)
		voices := regexp.MustCompile(`(?m)^(V\d+) `).FindAllStringSubmatch(user, -1)
		var out []any
		for i, c := range chars {
			var cands []any
			for k := 0; k < 3 && len(voices) > 0; k++ {
				cands = append(cands, map[string]any{"voiceId": voices[(i+k)%len(voices)][1], "score": 0.9 - float64(k)/10, "reason": "合适"})
			}
			out = append(out, map[string]any{"characterId": c[1], "candidates": cands})
		}
		return map[string]any{"castings": out}
	case strings.Contains(system, "内容安全"):
		return map[string]any{"verdict": "pass", "categories": []any{}, "reason": "正常"}
	}
	return map[string]any{"reply": "ok"}
}

// bookFixture builds a multi-voice book from chapter texts, with a narrator and the given cast, and
// points the platform's language model at f.
func bookFixture(t *testing.T, a *App, u *model.User, f *fakeLLM, chapters []string, cast ...model.Character) (*model.Book, []model.Chapter) {
	t.Helper()
	a.DB.Create(&model.ProviderAccount{Kind: model.KindLLM, Provider: "openai_chat", Scope: "platform", Name: "本机模拟", BaseURL: f.srv.URL + "/v1", Model: "fake-model", CredentialsEnc: a.Encrypt("sk-test"), Enabled: true, Priority: 1000, Config: model.EmptyMap()})
	b := &model.Book{StudioID: u.PersonalStudio, Title: "测试书", Mode: model.ModeMulti, Status: model.BookPreparing, Tags: model.Strings(), CreatedBy: u.ID}
	b.Settings.V = a.defaultBookSettings(u.PersonalStudio, model.TierStandard)
	if err := a.DB.Create(b).Error; err != nil {
		t.Fatal(err)
	}
	narr := model.Character{BookID: b.ID, Name: "旁白", Kind: model.CharNarrator, Gender: "unknown", AgeGroup: "adult", Mode: "ai", Aliases: model.Strings()}
	a.DB.Create(&narr)
	for i := range cast {
		cast[i].BookID = b.ID
		if cast[i].Mode == "" {
			cast[i].Mode = "ai"
		}
		cast[i].SortOrder = i + 1
		a.DB.Create(&cast[i])
	}
	var chs []model.Chapter
	words := 0
	for i, txt := range chapters {
		ch := model.Chapter{BookID: b.ID, Index: i + 1, Title: fmt.Sprintf("第%d章", i+1), Kind: "body", Included: true, SourceText: txt, WordCount: text.WordCount(txt)}
		a.DB.Create(&ch)
		chs = append(chs, ch)
		words += ch.WordCount
	}
	a.DB.Model(b).Update("word_count", words)
	b.WordCount = words
	return b, chs
}

func character(name, kind string, aliases ...string) model.Character {
	c := model.Character{Name: name, Kind: kind, Gender: "female", AgeGroup: "teen", Aliases: model.Strings(aliases...)}
	c.Params.V = model.VoiceParams{}
	c.Casting.V = []model.CastingCandidate{}
	return c
}

func owner(u *model.User, b *model.Book) *Access {
	x := &Access{User: u, StudioID: b.StudioID, Role: model.RoleOwner, Book: b, perms: map[Perm]bool{}}
	for _, p := range allPerms {
		x.perms[p] = true
	}
	return x
}

func TestChapterCastPicksWhoTheChapterNeeds(t *testing.T) {
	a, u := testApp(t)
	f := newFakeLLM(t)
	cast := []model.Character{character("林黛玉", model.CharLead, "黛玉"), character("贾宝玉", model.CharSupporting, "宝玉", "二爷"), character("史湘云", model.CharMinor), character("袭人", model.CharMinor, "花袭人")}
	for i := 0; i < 120; i++ { // a long novel's worth of minor characters nobody mentions here
		cast = append(cast, character(fmt.Sprintf("路人%03d", i), model.CharMinor))
	}
	_, chs := bookFixture(t, a, u, f, []string{"“二爷来了。”花袭人说。", "宝玉进门时，屋里只有一盏灯。"}, cast...)
	var chars []model.Character
	a.DB.Where("book_id = ?", chs[0].BookID).Order("sort_order asc").Find(&chars)
	names := func(cs []model.Character) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	// named by alias, plus the lead; nobody else
	if got := names(a.chapterCast(&chs[0], chars)); got != "林黛玉,贾宝玉,袭人" {
		t.Fatalf("chapter 1 cast = %s", got)
	}
	// chapter 2 has a script in chapter 1 to continue from: whoever spoke there is offered too
	sc := model.Script{ChapterID: chs[0].ID, BookID: chs[0].BookID, Version: 1, Origin: "ai", Status: "ready"}
	a.DB.Create(&sc)
	a.DB.Model(&chs[0]).Update("current_script_id", sc.ID)
	var xiren model.Character
	a.DB.First(&xiren, "name = ?", "袭人")
	a.DB.Create(&model.Segment{ScriptID: sc.ID, ChapterID: chs[0].ID, BookID: chs[0].BookID, Seq: 1000, Type: model.SegDialogue, CharacterID: xiren.ID, Text: "二爷来了。", Tags: model.Strings(), Effects: model.Strings()})
	if got := names(a.chapterCast(&chs[1], chars)); got != "林黛玉,贾宝玉,袭人" {
		t.Fatalf("chapter 2 cast = %s", got)
	}
}

func TestSpeechTagOption(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		cfg  model.PipelineConfig
		want bool
	}{
		{model.PipelineConfig{Mode: model.ModeMulti}, false},
		{model.PipelineConfig{Mode: model.ModeDrama}, true}, // API clients that do not send the field
		{model.PipelineConfig{Mode: model.ModeMulti, RemoveSpeechTags: &on}, true},
		{model.PipelineConfig{Mode: model.ModeDrama, RemoveSpeechTags: &off}, false},
		{model.PipelineConfig{Mode: model.ModeSingle, RemoveSpeechTags: &on}, false},
	} {
		if got := c.cfg.SpeechTagsRemoved(); got != c.want {
			t.Errorf("%+v: got %v", c.cfg, got)
		}
	}
}

// A speaker the model calls new but who is an alias of a character in the book is matched in code;
// one who is really new joins the cast with the profile the model wrote, and only once.
func TestNewSpeakersJoinTheCast(t *testing.T) {
	a, u := testApp(t)
	f := newFakeLLM(t)
	decoy := character("史湘云", model.CharMinor)
	b, chs := bookFixture(t, a, u, f, []string{
		"门外进来一个胖子。\n“俺来送信。”王大锤说。\n“放下吧。”黛玉说。",
		"第二天他又来了。\n“又是俺。”王大锤说。",
	}, character("林黛玉", model.CharLead, "黛玉"), decoy)
	a.Grant(b.StudioID, 100000, "grant", nil, "测试", "")
	for _, ch := range chs {
		job := &model.Job{ID: model.NewID(), StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: u.ID, Payload: model.Map{V: map[string]any{"mode": "ai"}}}
		if err := a.scriptParse(context.Background(), job, nopReporter{}, true); err != nil {
			t.Fatal(err)
		}
	}
	var chars []model.Character
	a.DB.Where("book_id = ?", b.ID).Find(&chars)
	byName := map[string]model.Character{}
	for _, c := range chars {
		byName[c.Name] = c
	}
	w, ok := byName["王大锤"]
	if !ok {
		t.Fatalf("王大锤 not added: %v", byName)
	}
	if w.Gender != "male" || w.AgeGroup != "middle" || w.VoiceTraits != "浑厚的中年男声" || w.Candidate || w.FirstChapter != 1 {
		t.Fatalf("王大锤 = %+v", w)
	}
	if len(chars) != 4 { // narrator, 林黛玉, 史湘云, 王大锤
		t.Fatalf("%d characters: %v", len(chars), byName)
	}
	// the second chapter offered 王大锤 to the model in its table and did not add him again
	calls := f.script()
	last := calls[len(calls)-1].user
	if !regexp.MustCompile(`(?m)^C\d+ 王大锤`).MatchString(last) {
		t.Fatalf("second chapter table: %s", last)
	}
	var lines int64
	a.DB.Model(&model.Segment{}).Where("book_id = ? AND character_id = ?", b.ID, w.ID).Count(&lines)
	if lines != 2 {
		t.Fatalf("王大锤 has %d lines", lines)
	}
	for _, c := range calls {
		if strings.Contains(c.user, "史湘云") {
			t.Fatalf("a character the chapter never names was sent to the model:\n%s", c.user)
		}
	}
}

// With an alias known to the book, a NEW: answer resolves to the existing character.
func TestNewSpeakerMatchesAnAlias(t *testing.T) {
	a, u := testApp(t)
	f := newFakeLLM(t)
	// the model is shown no cast at all, so it answers NEW:颦儿; the book knows 颦儿 as 林黛玉
	b, chs := bookFixture(t, a, u, f, []string{"“你走吧。”颦儿说。"}, character("林黛玉", model.CharSupporting, "颦儿"))
	a.Grant(b.StudioID, 100000, "grant", nil, "测试", "")
	var chars []model.Character
	a.DB.Where("book_id = ?", b.ID).Order("sort_order asc").Find(&chars)
	h, err := a.LLMFor(FeatScript, "", b.StudioID)
	if err != nil {
		t.Fatal(err)
	}
	units := text.Segment(chs[0].SourceText, 120)
	res, err := a.AnnotateUnits(context.Background(), h, units, chars, AnnotateOptions{Cast: []model.Character{}}, func(float64, string) {})
	if err != nil {
		t.Fatal(err)
	}
	var lin model.Character
	a.DB.First(&lin, "name = ?", "林黛玉")
	if got := res.Labels[0].Speaker; got != speakerRef+lin.ID {
		t.Fatalf("speaker = %q, want the existing character", got)
	}
	segs, added := a.labelUnits(b, 1, units, res, chars, false)
	if len(added) != 0 || segs[0].CharacterID != lin.ID {
		t.Fatalf("added %v, line goes to %s", added, segs[0].CharacterID)
	}
}

// A chapter whose annotation fails halfway continues from the last finished batch.
func TestAnnotationResumesAfterAFailure(t *testing.T) {
	a, u := testApp(t)
	f := newFakeLLM(t)
	var sb strings.Builder
	for i := 0; i < 240; i++ {
		fmt.Fprintf(&sb, "这是第%d段叙述，屋外的雨一直下着，灯火在风里摇晃，远处传来几声更鼓。\n", i+1)
	}
	b, chs := bookFixture(t, a, u, f, []string{sb.String()})
	a.Grant(b.StudioID, 100000, "grant", nil, "测试", "")
	batches := 0
	f.fail = func(n int, system, user string) bool {
		if strings.Contains(system, "画本师") {
			batches++
			return batches == 2
		}
		return false
	}
	job := &model.Job{ID: model.NewID(), StudioID: b.StudioID, BookID: b.ID, ChapterID: chs[0].ID, CreatedBy: u.ID, Payload: model.Map{V: map[string]any{"mode": "ai"}}}
	if err := a.scriptParse(context.Background(), job, nopReporter{}, true); err == nil {
		t.Fatal("first attempt should fail on the second batch")
	}
	var draft model.AnnotationDraft
	if err := a.DB.First(&draft, "chapter_id = ?", chs[0].ID).Error; err != nil {
		t.Fatalf("no draft kept: %v", err)
	}
	before := len(f.script())
	if err := a.scriptParse(context.Background(), job, nopReporter{}, true); err != nil {
		t.Fatal(err)
	}
	total := (utf8Len(sb.String()) + 2199) / 2200 // about this many batches in the chapter
	if again := len(f.script()) - before; again >= total || again < 1 {
		t.Fatalf("second attempt sent %d batches; the chapter has about %d and the first was done", again, total)
	}
	if strings.Contains(f.script()[before].user, "[0] (叙述)") {
		t.Fatal("the finished first batch was sent again")
	}
	var n int64
	a.DB.Model(&model.AnnotationDraft{}).Where("chapter_id = ?", chs[0].ID).Count(&n)
	if n != 0 {
		t.Fatal("draft left behind after the script was saved")
	}
	var segs int64
	a.DB.Model(&model.Segment{}).Where("chapter_id = ?", chs[0].ID).Count(&segs)
	if segs == 0 {
		t.Fatal("no script")
	}
}

func utf8Len(s string) int { return len([]rune(s)) }

// startQueue runs the job queue for the length of a test.
func startQueue(t *testing.T, a *App) {
	ctx, cancel := context.WithCancel(context.Background())
	a.Q.Start(ctx)
	t.Cleanup(func() {
		cancel()
		a.Q.Wait()
	})
}

func waitJob(t *testing.T, a *App, id string) model.Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var j model.Job
		a.DB.First(&j, "id = ?", id)
		if j.Status == model.JobSucceeded || j.Status == model.JobFailed || j.Status == model.JobCanceled {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return model.Job{}
}

// Identifying the characters of chosen chapters reads those chapters in full and records where each
// character first appears; the chapter list is part of the job from the start.
func TestCharacterAnalysisOfChosenChapters(t *testing.T) {
	a, u := testApp(t)
	f := newFakeLLM(t)
	long := strings.Repeat("宝玉在园子里走了很久，", 1000) // ~11,000 characters: two pieces
	b, chs := bookFixture(t, a, u, f, []string{"黛玉独自坐在窗前。", long, "王大锤来送信。"})
	a.Grant(b.StudioID, 100000, "grant", nil, "测试", "")
	startQueue(t, a)
	j, err := a.EnqueueCharacterAnalysis(owner(u, b), b.ID, []string{chs[1].ID, chs[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if ids, _ := j.Payload.V["chapterIds"].([]any); len(ids) != 2 {
		t.Fatalf("payload = %v", j.Payload.V)
	}
	if done := waitJob(t, a, j.ID); done.Status != model.JobSucceeded {
		t.Fatalf("job %s: %s", done.Status, done.Error)
	}
	if n := f.count("有声书导演"); n != 3 { // chapter 2 in two pieces, chapter 3 in one; chapter 1 not read
		t.Fatalf("%d analysis calls", n)
	}
	var chars []model.Character
	a.DB.Where("book_id = ? AND kind <> ?", b.ID, model.CharNarrator).Find(&chars)
	first := map[string]int{}
	for _, c := range chars {
		first[c.Name] = c.FirstChapter
	}
	if first["贾宝玉"] != 2 || first["王大锤"] != 3 || len(first) != 2 {
		t.Fatalf("first chapters = %v", first)
	}
}

// One-click production over a chosen range: chapters move through the line side by side, a speaker
// met in chapter 2 joins the cast with a voice, "他说道" lines go when the switch is on, and when
// credits run out the run waits and later carries on from where it stopped.
func TestPipelineProducesChosenChaptersAndWaitsForCredits(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	a, u := testApp(t)
	a.FF = &audio.FF{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	local, err := storage.NewLocal(t.TempDir(), []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	a.Store = storage.NewStore(local)
	a.seedPricing()
	a.seedProviders()
	a.seedVoices(context.Background())
	f := newFakeLLM(t)
	chapter := func(n int, extra string) string {
		return fmt.Sprintf("第%d天，林黛玉看着窗外的雨。\n“你来了。”黛玉说。\n“我来了。”宝玉笑道。%s", n, extra)
	}
	b, chs := bookFixture(t, a, u, f, []string{
		chapter(1, ""),
		chapter(2, "\n门外进来一个胖子。\n“俺来送信。”王大锤说。"),
		chapter(3, ""),
		chapter(4, ""),
	}, character("林黛玉", model.CharLead, "黛玉"), character("贾宝玉", model.CharLead, "宝玉"), character("史湘云", model.CharMinor))
	a.DB.Model(&model.Character{}).Where("name = ?", "贾宝玉").Update("gender", "male")
	startQueue(t, a)
	x := owner(u, b)
	on := true
	cfg := model.PipelineConfig{Mode: model.ModeMulti, Tier: model.TierStandard, ChapterIDs: []string{chs[0].ID, chs[1].ID, chs[2].ID}, RemoveSpeechTags: &on}

	// enough credits for about one chapter
	est := a.EstimatePipeline(b, cfg)
	per := a.chapterCredits(&chs[0], a.CastingPolicy(model.TierStandard))
	if per <= 0 || est["chapters"] != 3 {
		t.Fatalf("estimate %v, per chapter %d", est, per)
	}
	a.Grant(b.StudioID, per+per/2, "grant", nil, "测试", "")
	run, err := a.StartPipeline(x, cfg, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	waitRun := func(states ...string) model.PipelineRun {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		var r model.PipelineRun
		for time.Now().Before(deadline) {
			a.DB.First(&r, "id = ?", run.ID)
			for _, s := range states {
				if r.Status == s {
					return r
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("run is %s (%s), want %v", r.Status, r.Error, states)
		return r
	}
	r := waitRun("waiting", "failed", "completed")
	if r.Status != "waiting" || r.WaitReason != "credits" {
		var bad []model.Segment
		a.DB.Where("book_id = ? AND audio_status <> ?", b.ID, model.AudioReady).Find(&bad)
		for _, s := range bad {
			t.Logf("%s [%s] %s: %s", s.Text, s.CharacterID, s.AudioStatus, s.LastError)
		}
		t.Logf("balance %+v, per chapter %d", a.Balance(b.StudioID), per)
		t.Fatalf("run %s/%s: %s", r.Status, r.WaitReason, r.Error)
	}
	view := a.PipelineView(&r)
	done := 0
	for _, c := range view["chapters"].([]map[string]any) {
		if c["status"] == "done" {
			done++
		}
	}
	if done == 3 {
		t.Fatal("all chapters done although credits ran out")
	}
	for _, c := range view["chapters"].([]map[string]any) {
		if c["status"] == "failed" {
			t.Fatalf("a chapter stopped by the balance is marked failed: %v", c)
		}
	}
	// the run says "waiting" a moment before the queue has parked the job and released its hold
	var j model.Job
	for i := 0; i < 100; i++ {
		a.DB.First(&j, "id = ?", r.JobID)
		if j.Status == model.JobPaused {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if bal := a.Balance(b.StudioID); bal.Frozen != 0 || j.Status != model.JobPaused {
		t.Fatalf("waiting run still holds %d credits (job %s)", bal.Frozen, j.Status)
	}

	// top up and carry on
	a.Grant(b.StudioID, 1000000, "grant", nil, "测试", "")
	scriptsBefore := len(f.script())
	if err := a.PipelineAction(x, &r, "resume"); err != nil {
		t.Fatal(err)
	}
	r = waitRun("completed", "failed")
	if r.Status != "completed" {
		var bad []model.Segment
		a.DB.Where("book_id = ? AND audio_status <> ?", b.ID, model.AudioReady).Find(&bad)
		for _, s := range bad {
			t.Logf("%s [%s] %s: %s", s.Text, s.CharacterID, s.AudioStatus, s.LastError)
		}
		t.Fatalf("run failed: %s", r.Error)
	}
	if again := len(f.script()) - scriptsBefore; again > 3-done {
		t.Fatalf("resume scripted %d chapters, %d were already done", again, done)
	}
	for i, ch := range chs {
		var c model.Chapter
		a.DB.First(&c, "id = ?", ch.ID)
		if rendered := c.RenderID != ""; rendered != (i < 3) {
			t.Fatalf("chapter %d rendered = %v", i+1, rendered)
		}
	}
	var book model.Book
	a.DB.First(&book, "id = ?", b.ID)
	if book.Status == model.BookCompleted {
		t.Fatal("book marked completed with chapter 4 not made")
	}
	var w model.Character
	if err := a.DB.First(&w, "book_id = ? AND name = ?", b.ID, "王大锤").Error; err != nil {
		t.Fatal("王大锤 not added to the cast")
	}
	if w.VoiceID == "" || w.Gender != "male" || w.FirstChapter != 2 {
		t.Fatalf("王大锤 = %+v", w)
	}
	var tags, removed int64
	a.DB.Model(&model.Segment{}).Where("book_id = ? AND type = ? AND text LIKE ?", b.ID, model.SegNarration, "%说。").Count(&tags)
	a.DB.Model(&model.Segment{}).Where("book_id = ? AND type = ? AND text LIKE ? AND removed = ?", b.ID, model.SegNarration, "%说。", true).Count(&removed)
	if tags == 0 || removed != tags {
		t.Fatalf("speech tags removed %d of %d", removed, tags)
	}
	for _, c := range f.script() {
		if strings.Contains(c.user, "史湘云") {
			t.Fatal("a character the chapter never names was sent to the model")
		}
	}
	if bal := a.Balance(b.StudioID); bal.Frozen != 0 {
		t.Fatalf("finished run still holds %d credits", bal.Frozen)
	}
	_ = jobs.LanePipeline
}
