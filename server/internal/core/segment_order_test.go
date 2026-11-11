package core

import (
	"strings"
	"testing"

	"voicebook/internal/model"
)

// scriptFixture creates a book with one chapter whose script has the given lines (one paragraph per
// "|"-separated group) and returns an access that may edit it.
func scriptFixture(t *testing.T, groups ...string) (*App, *Access, *model.Chapter) {
	t.Helper()
	a, u := testApp(t)
	b := &model.Book{StudioID: u.PersonalStudio, Title: "测试书"}
	a.DB.Create(b)
	ch := &model.Chapter{BookID: b.ID, Index: 1, Title: "第一章"}
	a.DB.Create(ch)
	sc := &model.Script{ChapterID: ch.ID, BookID: b.ID, Version: 1, Origin: "manual", Status: "ready"}
	a.DB.Create(sc)
	a.DB.Model(ch).Update("current_script_id", sc.ID)
	ch.CurrentScriptID = sc.ID
	seq := 0.0
	for p, g := range groups {
		for _, txt := range strings.Split(g, ",") {
			seq += 1000
			s := model.Segment{ScriptID: sc.ID, ChapterID: ch.ID, BookID: b.ID, Seq: seq, Para: p, Type: model.SegNarration, Text: txt, Tags: model.Strings(), Effects: model.Strings()}
			if err := a.DB.Create(&s).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	x := &Access{User: u, StudioID: u.PersonalStudio, Book: b, perms: map[Perm]bool{PermScript: true}}
	return a, x, ch
}

func order(segs []model.Segment) string {
	var out []string
	for _, s := range segs {
		out = append(out, s.Text)
	}
	return strings.Join(out, ",")
}

func seg(t *testing.T, a *App, ch *model.Chapter, text string) *model.Segment {
	t.Helper()
	var s model.Segment
	if err := a.DB.First(&s, "chapter_id = ? AND text = ?", ch.ID, text).Error; err != nil {
		t.Fatalf("line %q: %v", text, err)
	}
	return &s
}

func TestMoveSegmentAfter(t *testing.T) {
	a, x, ch := scriptFixture(t, "A,B,C", "D,E")
	mv := func(text, after string) []model.Segment {
		t.Helper()
		id := ""
		if after != "" {
			id = seg(t, a, ch, after).ID
		}
		segs, err := a.MoveSegmentAfter(x, seg(t, a, ch, text), id)
		if err != nil {
			t.Fatal(err)
		}
		return segs
	}
	if got := order(mv("A", "D")); got != "B,C,D,A,E" {
		t.Fatalf("drag down: %s", got)
	}
	if p := seg(t, a, ch, "A").Para; p != 1 {
		t.Fatalf("a line dropped after D joins D's paragraph, got para %d", p)
	}
	if got := order(mv("E", "")); got != "E,B,C,D,A" {
		t.Fatalf("drag to the top: %s", got)
	}
	if p := seg(t, a, ch, "E").Para; p != 0 {
		t.Fatalf("a line moved to the top joins the first paragraph, got para %d", p)
	}
	if got := order(mv("B", "B")); got != "E,B,C,D,A" {
		t.Fatalf("dropping a line after itself changes nothing: %s", got)
	}
	if _, err := a.MoveSegmentAfter(x, seg(t, a, ch, "C"), "missing"); err == nil {
		t.Fatal("unknown target must fail instead of moving the line somewhere random")
	}
}

func TestMoveSegmentUpDown(t *testing.T) {
	a, x, ch := scriptFixture(t, "A,B,C")
	step := func(text string, dir int) string {
		t.Helper()
		segs, err := a.MoveSegment(x, seg(t, a, ch, text), dir)
		if err != nil {
			t.Fatal(err)
		}
		return order(segs)
	}
	if got := step("C", -1); got != "A,C,B" {
		t.Fatalf("up: %s", got)
	}
	if got := step("C", -1); got != "C,A,B" {
		t.Fatalf("up to the top: %s", got)
	}
	if got := step("C", -1); got != "C,A,B" {
		t.Fatalf("the first line cannot go higher: %s", got)
	}
	if got := step("C", 1); got != "A,C,B" {
		t.Fatalf("down: %s", got)
	}
	if got := step("B", 1); got != "A,C,B" {
		t.Fatalf("the last line cannot go lower: %s", got)
	}
	// hidden speech tags do not count as a position
	a.DB.Model(&model.Segment{}).Where("chapter_id = ? AND text = ?", ch.ID, "C").Update("removed", true)
	if got := step("B", -1); got != "B,A,C" {
		t.Fatalf("up over a hidden line: %s", got)
	}
}

func TestMoveSegmentRenumbersWhenTheGapRunsOut(t *testing.T) {
	a, x, ch := scriptFixture(t, "A,B,C")
	for i := 0; i < 70; i++ { // halving the A-B gap 70 times exhausts float precision
		mover, target := "C", "A"
		if i%2 == 1 {
			mover, target = "B", "A"
		}
		if _, err := a.MoveSegmentAfter(x, seg(t, a, ch, mover), seg(t, a, ch, target).ID); err != nil {
			t.Fatal(err)
		}
	}
	segs := a.scriptSegments(ch.CurrentScriptID)
	if got := order(segs); got != "A,B,C" {
		t.Fatalf("order after many moves: %s", got)
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].Seq <= segs[i-1].Seq {
			t.Fatalf("sequence numbers must stay strictly increasing: %v", segs)
		}
	}
}

func TestInsertSegmentPositions(t *testing.T) {
	a, x, ch := scriptFixture(t, "A,B", "C")
	ins := func(after, text string) string {
		t.Helper()
		id := ""
		if after != "" {
			id = seg(t, a, ch, after).ID
		}
		segs, err := a.InsertSegment(x, ch, id, "", text, "")
		if err != nil {
			t.Fatal(err)
		}
		return order(segs)
	}
	if got := ins("A", "X"); got != "A,X,B,C" {
		t.Fatalf("insert below A: %s", got)
	}
	if got := ins("", "Top"); got != "Top,A,X,B,C" {
		t.Fatalf("insert at the top: %s", got)
	}
	if got := ins("C", "End"); got != "Top,A,X,B,C,End" {
		t.Fatalf("append: %s", got)
	}
	if p := seg(t, a, ch, "End").Para; p != 1 {
		t.Fatalf("an appended line joins the last paragraph, got %d", p)
	}
	if _, err := a.InsertSegment(x, ch, "missing", "", "Lost", ""); err == nil {
		t.Fatal("inserting after an unknown line must fail instead of landing at a random spot")
	}
}
