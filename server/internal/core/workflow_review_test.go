package core

import (
	"strings"
	"testing"

	"voicebook/internal/model"
)

func TestGuessTitle(t *testing.T) {
	for raw, want := range map[string][2]string{
		"《长夜》\n作者：林舟\n第一章 雨\n正文": {"长夜", "林舟"},
		"长夜\n\n第一章 雨\n正文":        {"长夜", ""},
		"第一章 初见\n夜色渐深。":          {"", ""},
		"夜色渐深，她推开窗。\n“你来做什么？”":   {"", ""},
		"只有一行": {"", ""},
	} {
		if ti, au := GuessTitle(raw); ti != want[0] || au != want[1] {
			t.Errorf("%q -> %q/%q, want %q/%q", raw, ti, au, want[0], want[1])
		}
	}
}

// A name with a rare character that has several readings (嬛: huán / xuān / qióng) must follow the
// book lexicon, not only the common polyphonic characters.
func TestLexiconAppliesToRareCharacters(t *testing.T) {
	lex := []model.LexiconEntry{{Scope: "book", Term: "甄嬛", Pinyin: "zhēn huán", Status: "active"}}
	_, prons := BuildTTSInput("甄嬛进宫了", nil, lex, true)
	found := false
	for _, p := range prons {
		if p.Char == "嬛" && p.Pinyin == "huán" {
			found = true
		}
	}
	if !found {
		t.Fatalf("lexicon reading for 嬛 not applied: %+v", prons)
	}
	out, _ := BuildTTSInput("甄嬛进宫了", nil, lex, false)
	if strings.Contains(out, "嬛") {
		t.Fatalf("providers without phoneme input should get a homophone, got %q", out)
	}
}

func TestWordReadings(t *testing.T) {
	r := WordReadings("乐正")
	if len(r) != 2 || r[0]["char"] != "乐" || len(r[0]["readings"].([]string)) < 2 {
		t.Fatalf("readings: %+v", r)
	}
}

func TestStaleLinesWithTerms(t *testing.T) {
	a, x, ch := scriptFixture(t, "甄嬛进宫,皇上驾到", "甄嬛跪下")
	a.DB.Model(&model.Segment{}).Where("chapter_id = ?", ch.ID).Update("audio_status", model.AudioReady)
	a.DB.Model(&model.Segment{}).Where("text = ?", "甄嬛跪下").Update("audio_status", model.AudioNone)
	a.DB.Model(ch).Update("render_id", "r1")
	if n := a.StaleLinesWithTerms(x.Book.ID, "甄嬛", "甄嬛"); n != 1 {
		t.Fatalf("want 1 voiced line marked, got %d", n)
	}
	if s := seg(t, a, ch, "甄嬛进宫"); s.AudioStatus != model.AudioStale {
		t.Fatalf("voiced line with the term should be stale, got %s", s.AudioStatus)
	}
	if s := seg(t, a, ch, "皇上驾到"); s.AudioStatus != model.AudioReady {
		t.Fatal("lines without the term stay as they are")
	}
	var c model.Chapter
	a.DB.First(&c, "id = ?", ch.ID)
	if !c.RenderStale {
		t.Fatal("the chapter render must be flagged for re-rendering")
	}
}

func TestDeliveryChecklist(t *testing.T) {
	a, x, ch := scriptFixture(t, "第一句,第二句")
	a.DB.Model(&model.Chapter{}).Where("id = ?", ch.ID).Update("included", true)
	a.DB.Model(&model.Segment{}).Where("text = ?", "第一句").Update("audio_status", model.AudioReady)
	a.DB.Create(&model.Issue{BookID: x.Book.ID, ChapterID: ch.ID, Status: model.IssueOpen, Title: "错读"})
	checks := map[string]map[string]any{}
	var keys []string
	for _, c := range a.PrePublishCheck(x.Book, nil, nil) {
		checks[c["key"].(string)] = c
		keys = append(keys, c["key"].(string))
	}
	if strings.Join(keys[:6], ",") != "script,audio,renders,tech,review,issues" {
		t.Fatalf("production checks should come first in workflow order: %v", keys)
	}
	if checks["script"]["ok"] != true || checks["audio"]["ok"] != false || !strings.Contains(checks["audio"]["tip"].(string), "1 句还没有音频") {
		t.Fatalf("audio check: %+v / %+v", checks["script"], checks["audio"])
	}
	if checks["issues"]["ok"] != false || checks["review"]["ok"] != false {
		t.Fatalf("open issues and unreviewed chapters must block: %+v %+v", checks["issues"], checks["review"])
	}
}

func TestSegmentViewsCarryOpenIssues(t *testing.T) {
	a, x, ch := scriptFixture(t, "第一句,第二句")
	s := seg(t, a, ch, "第二句")
	a.DB.Create(&model.Issue{BookID: x.Book.ID, ChapterID: ch.ID, SegmentID: s.ID, Status: model.IssueOpen, Title: "错读"})
	a.DB.Create(&model.Issue{BookID: x.Book.ID, ChapterID: ch.ID, SegmentID: s.ID, Status: model.IssueClosed, Title: "旧的"})
	var segs []model.Segment
	a.DB.Where("chapter_id = ?", ch.ID).Order("seq").Find(&segs)
	v := a.SegmentViews(segs)
	if _, ok := v[0]["openIssues"]; ok {
		t.Fatal("a line without issues has no marker")
	}
	if v[1]["openIssues"] != 1 || v[1]["issueTitle"] != "错读" {
		t.Fatalf("open issue marker: %+v", v[1])
	}
}
