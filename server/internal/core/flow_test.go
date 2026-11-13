package core

import (
	"context"
	"math"
	"os/exec"
	"strings"
	"testing"

	"voicebook/internal/audio"
	"voicebook/internal/model"
	"voicebook/internal/storage"
)

func TestEndsClause(t *testing.T) {
	for text, want := range map[string]bool{
		"他抬起头，": true, "“你来了，”": true, "黛玉说：": true, "第一、": true, "很好；": true,
		"他走了。": false, "“坐吧。”": false, "真的吗？": false, "": false, "等等……": false,
	} {
		if got := endsClause(text); got != want {
			t.Errorf("%q: %v", text, got)
		}
	}
}

func TestSpeechSpansHoldMusicDownThroughShortPauses(t *testing.T) {
	// voice at 0-1s, 1.5-2.5s, 6-7s (frames = ms here); hold 1s bridges the first pause only
	got := audio.SpeechSpans([][2]int{{6000, 7000}, {0, 1000}, {1500, 2500}}, 1000)
	if len(got) != 2 || got[0] != [2]int{0, 2500} || got[1] != [2]int{6000, 7000} {
		t.Fatalf("spans = %v", got)
	}
	if got := audio.SpeechSpans([][2]int{{0, 1000}, {1500, 2500}}, 0); len(got) != 2 {
		t.Fatalf("without hold the music should rise between lines: %v", got)
	}
}

// flowFixture renders a voiced chapter: three sentences by the narrator, one split at a comma.
func flowFixture(t *testing.T) (*App, *model.Book, *model.Chapter) {
	t.Helper()
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
	long := strings.Repeat("夜色渐深", 30) // a sentence long enough to be split at its comma
	b, chs := bookFixture(t, a, u, f, []string{long + "，" + long + "。\n雨停了。\n远处传来几声更鼓。"})
	a.Grant(b.StudioID, 1000000, "grant", nil, "测试", "")
	ctx := context.Background()
	if _, err := a.Cast(ctx, owner(u, b), nil, true, nil); err != nil {
		t.Fatal(err)
	}
	ch := &chs[0]
	job := &model.Job{ID: model.NewID(), StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: u.ID, Payload: model.Map{V: map[string]any{"mode": "ai"}}}
	if err := a.scriptParse(ctx, job, nopReporter{}, true); err != nil {
		t.Fatal(err)
	}
	a.DB.First(ch, "id = ?", ch.ID)
	targets, err := a.generationTargets(ch, GenerateRequest{Scope: "missing"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	job.Payload = model.Map{V: map[string]any{"segmentIds": toAny(targetIDs(targets))}}
	if err := a.ttsSegments(ctx, job, nopReporter{}, true); err != nil {
		t.Fatal(err)
	}
	return a, b, ch
}

func setFlow(a *App, b *model.Book, pause model.PauseRules, flow model.FlowSettings) {
	s := b.Settings.V
	s.Pause, s.Flow = pause, flow
	b.Settings.V = s
	a.DB.Model(b).Update("settings", b.Settings)
}

func gaps(t *testing.T, a *App, ch *model.Chapter) []int64 {
	t.Helper()
	tl, err := a.BuildTimeline(ch, false)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, d := range tl.Dialogue[1:] {
		out = append(out, d.GapMs)
	}
	return out
}

func TestPausesAndVoiceSpeed(t *testing.T) {
	a, b, ch := flowFixture(t)
	// a book from before 0.1.3 lays out exactly as it did: 300 ms between every line of one speaker
	setFlow(a, b, model.LegacyPauseRules(), model.FlowSettings{})
	for _, g := range gaps(t, a, ch) {
		if g != 300 && g != 800 {
			t.Fatalf("legacy gaps changed: %v", gaps(t, a, ch))
		}
	}
	// the new rhythm: a short breath at the comma, a full pause between sentences
	setFlow(a, b, model.DefaultPauseRules(), model.DefaultFlow())
	g := gaps(t, a, ch)
	if len(g) < 3 || g[0] != 250 {
		t.Fatalf("gaps = %v, want 250 at the comma", g)
	}
	for _, x := range g[1:] {
		if x != 1000 && x != 550 { // new paragraph, or next sentence
			t.Fatalf("gaps = %v", g)
		}
	}

	render := func() model.Render {
		t.Helper()
		job := &model.Job{ID: model.NewID(), StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, Payload: model.Map{V: map[string]any{}}}
		if err := a.renderChapter(context.Background(), job, nopReporter{}); err != nil {
			t.Fatal(err)
		}
		var c model.Chapter
		a.DB.First(&c, "id = ?", ch.ID)
		var r model.Render
		a.DB.First(&r, "id = ?", c.RenderID)
		return r
	}
	normal := render()
	tl, _ := a.BuildTimeline(ch, false)
	setFlow(a, b, model.DefaultPauseRules(), model.FlowSettings{Smooth: true, FadeMs: 30, DuckHoldMs: 1500, Tempo: 1.25, RoomTone: true})
	fast := render()
	// the speech is 1.25x shorter; the opening and closing silence keep their length
	want := float64(tl.HeadMs) + float64(tl.SpeechEnd-tl.HeadMs)/1.25 + float64(tl.TailMs)
	if d := float64(fast.DurationMs); math.Abs(d-want) > 250 {
		t.Fatalf("at 1.25x the chapter lasts %d ms, want about %.0f (1x: %d)", fast.DurationMs, want, normal.DurationMs)
	}
	if fast.Report.V["tempo"] != 1.25 {
		t.Fatalf("report = %v", fast.Report.V)
	}
	// subtitles follow the faster voice
	last := func(r model.Render) model.SegmentTiming { return r.Timings.V[len(r.Timings.V)-1] }
	wantEnd := float64(tl.HeadMs) + float64(last(normal).EndMs-tl.HeadMs)/1.25
	if math.Abs(float64(last(fast).EndMs)-wantEnd) > 5 {
		t.Fatalf("last line ends at %d, want %.0f", last(fast).EndMs, wantEnd)
	}
	if first := fast.Timings.V[0]; first.StartMs != tl.HeadMs {
		t.Fatalf("first line starts at %d, the opening silence should stay %d", first.StartMs, tl.HeadMs)
	}
}

// A line can be given a voice of its own in the script editor: only that line changes, and re-casting
// its character leaves it alone.
func TestLineVoice(t *testing.T) {
	a, b, ch := flowFixture(t)
	var u model.User
	a.DB.First(&u, "id = ?", b.CreatedBy)
	x := owner(&u, b)
	segs := a.scriptSegments(ch.CurrentScriptID)
	var lines []model.Segment
	for _, s := range segs {
		if s.Type != model.SegSfx && s.Type != model.SegPause && !s.Removed {
			lines = append(lines, s)
		}
	}
	first, second := lines[0], lines[1]
	var narr model.Character
	a.DB.First(&narr, "id = ?", first.CharacterID)
	var other model.Voice
	for _, v := range a.CastingPool(b.StudioID, u.ID, "zh-CN") {
		if v.ID != narr.VoiceID && v.PriceTier != model.TierPremium {
			other = v
			break
		}
	}
	if _, err := a.UpdateSegments(x, []string{first.ID}, SegmentPatch{VoiceID: &other.ID}); err != nil {
		t.Fatal(err)
	}
	seg := func(id string) model.Segment {
		var s model.Segment
		a.DB.First(&s, "id = ?", id)
		return s
	}
	if s := seg(first.ID); s.VoiceID != other.ID || s.AudioStatus != model.AudioStale {
		t.Fatalf("line after a voice change: voice %q, audio %s", s.VoiceID, s.AudioStatus)
	}
	if s := seg(second.ID); s.AudioStatus != model.AudioReady || s.VoiceID != "" {
		t.Fatalf("another line changed: %+v", s)
	}
	job := &model.Job{ID: model.NewID(), StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, Payload: model.Map{V: map[string]any{"segmentIds": []any{first.ID}}}}
	if err := a.ttsSegments(context.Background(), job, nopReporter{}, true); err != nil {
		t.Fatal(err)
	}
	var take model.Take
	a.DB.First(&take, "id = ?", seg(first.ID).SelectedTakeID)
	if take.VoiceID != other.ID {
		t.Fatalf("line voiced with %s, want %s", take.VoiceID, other.ID)
	}
	// re-cast the narrator: every narrator line needs new audio except the one with its own voice
	var third model.Voice
	for _, v := range a.CastingPool(b.StudioID, u.ID, "zh-CN") {
		if v.ID != narr.VoiceID && v.ID != other.ID && v.PriceTier != model.TierPremium {
			third = v
			break
		}
	}
	if _, err := a.SetCharacterVoice(x, &narr, third.ID); err != nil {
		t.Fatal(err)
	}
	if s := seg(first.ID); s.AudioStatus != model.AudioReady {
		t.Fatalf("the line with its own voice was marked %s", s.AudioStatus)
	}
	if s := seg(second.ID); s.AudioStatus != model.AudioStale {
		t.Fatalf("a narrator line kept audio %s after re-casting", s.AudioStatus)
	}
	// back to the character's voice
	empty := ""
	a.UpdateSegments(x, []string{first.ID}, SegmentPatch{VoiceID: &empty})
	if s := seg(first.ID); s.VoiceID != "" || s.AudioStatus != model.AudioStale {
		t.Fatalf("after clearing: %+v", s)
	}
	// a voice the studio cannot cast is refused
	bad := "no-such-voice"
	if _, err := a.UpdateSegments(x, []string{first.ID}, SegmentPatch{VoiceID: &bad}); err == nil {
		t.Fatal("unknown voice accepted")
	}
}
