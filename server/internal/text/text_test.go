package text

import (
	"strings"
	"testing"
)

const sample = `书名：测试

第一章 初见
夜色渐深，她推开窗。
“你来做什么？”林黛玉冷冷地问。
宝玉笑道：“我来看看妹妹。”他顿了顿，又说：“外面下雨了。”
***
第二天清晨，雨停了。

第二章 别离
“走吧。”她说。
`

func TestSplitChapters(t *testing.T) {
	chs, ok := SplitChapters(sample)
	if !ok {
		t.Fatal("expected chapters")
	}
	if len(chs) != 2 {
		t.Fatalf("want 2 chapters, got %d: %+v", len(chs), chs)
	}
	if chs[0].Title != "第一章 初见" || chs[1].Title != "第二章 别离" {
		t.Fatalf("titles: %q %q", chs[0].Title, chs[1].Title)
	}
	if !chs[0].Included || chs[0].Kind != "body" {
		t.Fatal("chapter 1 should be included body")
	}
}

func TestSegmentPreservesText(t *testing.T) {
	chs, _ := SplitChapters(sample)
	units := Segment(chs[0].Text, 120)
	paras, _ := Paragraphs(chs[0].Text)
	joined := map[int]string{}
	for _, u := range units {
		joined[u.Para] += u.Text
	}
	for i, p := range paras {
		if joined[i] != p {
			t.Fatalf("para %d not preserved:\n%q\n%q", i, joined[i], p)
		}
	}
	var quotes []string
	sceneBreak := false
	for _, u := range units {
		if u.Quote {
			quotes = append(quotes, u.Text)
		}
		if u.SceneBreak {
			sceneBreak = true
		}
	}
	if len(quotes) != 3 {
		t.Fatalf("want 3 dialogue units, got %d: %v", len(quotes), quotes)
	}
	if quotes[1] != "“我来看看妹妹。”" {
		t.Fatalf("unexpected dialogue unit %q", quotes[1])
	}
	if !sceneBreak {
		t.Fatal("expected scene break before 第二天清晨")
	}
}

func TestSplitLong(t *testing.T) {
	long := strings.Repeat("这是一个很长的句子，用来测试切分。", 20)
	units := Segment(long, 60)
	if len(units) < 3 {
		t.Fatalf("expected long paragraph to be split, got %d", len(units))
	}
	var sb strings.Builder
	for _, u := range units {
		sb.WriteString(u.Text)
	}
	if sb.String() != long {
		t.Fatal("split must preserve text")
	}
}

func TestDiffAndPinyin(t *testing.T) {
	ops := DiffStrings([]string{"a", "b", "c"}, []string{"a", "x", "c", "d"})
	kinds := ""
	for _, o := range ops {
		kinds += o.Kind[:1]
	}
	if kinds != "ediei" && kinds != "eidei" {
		t.Fatalf("unexpected diff %s", kinds)
	}
	if !IsPolyphonic('行') {
		t.Fatal("行 should be polyphonic")
	}
	if h := Homophone("háng"); h == "" {
		t.Fatal("expected a homophone for háng")
	}
	if ToneNumber("háng") != "hang2" {
		t.Fatalf("tone number: %s", ToneNumber("háng"))
	}
}

func TestWordCount(t *testing.T) {
	if n := WordCount("你好 world 2024！"); n != 4 {
		t.Fatalf("word count %d", n)
	}
}
