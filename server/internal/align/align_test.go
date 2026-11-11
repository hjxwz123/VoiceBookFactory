package align

import "testing"

func words(s string, startMs int64, perChar int64) []Word {
	var out []Word
	t := startMs
	for _, r := range s {
		out = append(out, Word{Text: string(r), StartMs: t, EndMs: t + perChar})
		t += perChar
	}
	return out
}

func TestAlignBasic(t *testing.T) {
	segs := []SegText{
		{"s1", "夜色渐深，她推开窗。"},
		{"s2", "“你来做什么？”"},
		{"s3", "林黛玉冷冷地问。"},
	}
	var ws []Word
	ws = append(ws, words("夜色渐深她推开窗", 0, 200)...)
	ws = append(ws, words("嗯那个", 1800, 200)...) // chatter
	ws = append(ws, words("你来做什么", 2600, 200)...)
	ws = append(ws, words("林黛玉冷冷地问", 3800, 200)...)
	res := Align(segs, ws)
	if len(res.Matches) != 3 {
		t.Fatalf("want 3 matches, got %d (missing %v)", len(res.Matches), res.Missing)
	}
	if res.Matches[1].StartMs > 2600 || res.Matches[1].EndMs < 3600 {
		t.Fatalf("bad window for s2: %+v", res.Matches[1])
	}
	for _, m := range res.Matches {
		if m.Confidence < 0.9 {
			t.Fatalf("low confidence %+v", m)
		}
	}
}

func TestAlignMissingAndRepeat(t *testing.T) {
	segs := []SegText{
		{"a", "今天天气很好我们出去走走吧"},
		{"b", "这一句被漏掉了没有读"},
		{"c", "最后一句话说完就结束"},
	}
	var ws []Word
	ws = append(ws, words("今天天气很好我们出去走走吧", 0, 100)...)
	ws = append(ws, words("最后一句话说完就结束", 3000, 100)...)
	ws = append(ws, words("最后一句话说完就结束", 6000, 100)...) // retake
	res := Align(segs, ws)
	if len(res.Missing) != 1 || res.Missing[0] != "b" {
		t.Fatalf("want b missing, got %v", res.Missing)
	}
	var c *Match
	for i := range res.Matches {
		if res.Matches[i].SegmentID == "c" {
			c = &res.Matches[i]
		}
	}
	if c == nil {
		t.Fatal("c not matched")
	}
	if c.StartMs < 5000 {
		t.Fatalf("expected later retake to be default, got start %d", c.StartMs)
	}
	if len(c.Alternatives) != 1 {
		t.Fatalf("expected one alternative, got %d", len(c.Alternatives))
	}
}

func TestCompare(t *testing.T) {
	f, score := Compare("他推开了那扇旧门。", "他推开那扇新门")
	if score >= 1 {
		t.Fatal("expected differences")
	}
	var missing, wrong bool
	for _, x := range f {
		if x.Type == "missing" && x.Expected == "了" {
			missing = true
		}
		if x.Type == "wrong" && x.Expected == "旧" && x.Actual == "新" {
			wrong = true
		}
	}
	if !missing || !wrong {
		t.Fatalf("unexpected findings %+v", f)
	}
}

func TestOuttakeWithChatter(t *testing.T) {
	segs := []SegText{{"a", "第一句台词在这里"}, {"b", "最后一句话说完就结束"}}
	var ws []Word
	ws = append(ws, words("第一句台词在这里", 0, 100)...)
	ws = append(ws, words("最后一句话说完就结束", 1000, 100)...)
	ws = append(ws, words("嗯重来一下最后一句话说完就结束", 3000, 100)...)
	res := Align(segs, ws)
	for _, m := range res.Matches {
		if m.SegmentID == "b" && (len(m.Alternatives) != 1 || m.StartMs < 2500) {
			t.Fatalf("retake not detected: %+v extras=%+v", m, res.Extras)
		}
	}
}
