// Package text implements chapter detection, script segmentation, diffing and pinyin helpers.
package text

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	titleRe  = regexp.MustCompile(`^\s*(第[0-9０-９零〇一二三四五六七八九十百千万两]+[章回节集篇幕话]|序章|序言|序|楔子|引子|引言|前言|后记|尾声|番外[0-9一二三四五六七八九十]*|终章|附录|作者的话|写在前面|Chapter\s+[0-9IVXLCivxlc]+|CHAPTER\s+[0-9IVXLC]+|Prologue|Epilogue)([\s:：、.．\-—·]|$)`)
	volumeRe = regexp.MustCompile(`^\s*(第[0-9０-９零〇一二三四五六七八九十百千万两]+[卷部]|卷[0-9零〇一二三四五六七八九十]+|Volume\s+\d+|Part\s+\d+)([\s:：、.．\-—·]|$)`)
	frontRe  = regexp.MustCompile(`^\s*(版权|目录|作者的话|写在前面|前言|序言|序\s|序$|内容简介|简介|出版说明)`)
	backRe   = regexp.MustCompile(`^\s*(后记|附录|致谢|出版后记)`)
	sepRe    = regexp.MustCompile(`^[\s\*\-—=~·•◆◇★☆#＊※○●…_]{3,}$`)
)

type ChapterDraft struct {
	Title     string `json:"title"`
	Volume    string `json:"volume"`
	Kind      string `json:"kind"` // body | front | back
	Included  bool   `json:"included"`
	Text      string `json:"text"`
	WordCount int    `json:"wordCount"`
}

// Clean normalizes line endings, BOMs and trailing spaces.
func Clean(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(l, unicode.IsSpace)
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n\n", "\n\n\n")
	}
	return strings.TrimSpace(out)
}

// WordCount counts Han characters plus latin words / digit runs (the publishing convention).
func WordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			n++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !inWord {
				n++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return n
}

func IsTitleLine(line string) bool {
	l := strings.TrimSpace(line)
	return l != "" && utf8.RuneCountInString(l) <= 50 && titleRe.MatchString(l)
}

func IsVolumeLine(line string) bool {
	l := strings.TrimSpace(line)
	return l != "" && utf8.RuneCountInString(l) <= 40 && volumeRe.MatchString(l) && !titleRe.MatchString(l)
}

func classify(title string) (kind string, included bool) {
	t := strings.TrimSpace(title)
	switch {
	case strings.HasPrefix(t, "序章") || strings.HasPrefix(t, "楔子") || strings.HasPrefix(t, "引子") || strings.HasPrefix(t, "尾声") || strings.HasPrefix(t, "番外") || strings.HasPrefix(t, "终章"):
		return "body", true
	case frontRe.MatchString(t):
		return "front", false
	case backRe.MatchString(t):
		return "back", false
	}
	return "body", true
}

// SplitChapters detects chapter headings with rules. found=false means no headings were found.
func SplitChapters(raw string) ([]ChapterDraft, bool) {
	lines := strings.Split(Clean(raw), "\n")
	var idx []int
	for i, l := range lines {
		if IsTitleLine(l) || IsVolumeLine(l) {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return nil, false
	}
	return SplitAtLines(lines, idx), true
}

// SplitAtLines builds chapters from explicit heading line indices.
func SplitAtLines(lines []string, idx []int) []ChapterDraft {
	var out []ChapterDraft
	if len(idx) > 0 && idx[0] > 0 {
		pre := strings.TrimSpace(strings.Join(lines[:idx[0]], "\n"))
		if WordCount(pre) >= 80 {
			out = append(out, ChapterDraft{Title: "正文前内容", Kind: "front", Included: false, Text: pre})
		}
	}
	volume := ""
	for k, start := range idx {
		end := len(lines)
		if k+1 < len(idx) {
			end = idx[k+1]
		}
		title := strings.TrimSpace(lines[start])
		if IsVolumeLine(title) && !IsTitleLine(title) {
			volume = title
			body := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
			if WordCount(body) >= 80 {
				out = append(out, ChapterDraft{Title: title, Volume: volume, Kind: "body", Included: true, Text: body})
			}
			continue
		}
		body := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
		kind, inc := classify(title)
		out = append(out, ChapterDraft{Title: title, Volume: volume, Kind: kind, Included: inc, Text: body})
	}
	// Drop table-of-contents style headings: an (almost) empty heading whose title repeats later,
	// or that belongs to a run of three or more consecutive empty headings.
	seen := map[string]int{}
	for _, c := range out {
		seen[c.Title]++
	}
	empty := make([]bool, len(out))
	for i, c := range out {
		empty[i] = WordCount(c.Text) < 10
	}
	var cleaned []ChapterDraft
	for i, c := range out {
		if empty[i] {
			run := 1
			for j := i - 1; j >= 0 && empty[j]; j-- {
				run++
			}
			for j := i + 1; j < len(out) && empty[j]; j++ {
				run++
			}
			if seen[c.Title] > 1 || run >= 3 || strings.TrimSpace(c.Text) == "" {
				seen[c.Title]--
				continue
			}
		}
		cleaned = append(cleaned, c)
	}
	for i := range cleaned {
		cleaned[i].WordCount = WordCount(cleaned[i].Text)
	}
	return cleaned
}

// CandidateLines returns short lines that could be headings (for LLM-assisted detection).
func CandidateLines(raw string, limit int) ([]string, []int) {
	lines := strings.Split(Clean(raw), "\n")
	var texts []string
	var idx []int
	for i, l := range lines {
		t := strings.TrimSpace(l)
		n := utf8.RuneCountInString(t)
		if n == 0 || n > 30 {
			continue
		}
		if strings.ContainsAny(t, "。，！？”") && n > 12 {
			continue
		}
		texts = append(texts, t)
		idx = append(idx, i)
		if len(texts) >= limit {
			break
		}
	}
	return texts, idx
}

// SplitBySize splits text into chunks of about target words at paragraph boundaries.
func SplitBySize(raw string, target int, titleFmt string) []ChapterDraft {
	if target < 500 {
		target = 3000
	}
	paras := strings.Split(Clean(raw), "\n")
	var out []ChapterDraft
	var cur []string
	words := 0
	flush := func() {
		body := strings.TrimSpace(strings.Join(cur, "\n"))
		if body == "" {
			return
		}
		out = append(out, ChapterDraft{Kind: "body", Included: true, Text: body, WordCount: WordCount(body)})
		cur, words = nil, 0
	}
	for _, p := range paras {
		cur = append(cur, p)
		words += WordCount(p)
		if words >= target {
			flush()
		}
	}
	flush()
	// merge a tiny tail into the previous chunk
	if len(out) > 1 && out[len(out)-1].WordCount < target/4 {
		last := out[len(out)-1]
		out = out[:len(out)-1]
		out[len(out)-1].Text += "\n" + last.Text
		out[len(out)-1].WordCount += last.WordCount
	}
	if titleFmt == "" {
		titleFmt = "第%d集"
	}
	for i := range out {
		out[i].Title = fmt.Sprintf(titleFmt, i+1)
	}
	return out
}

var cnParts = []string{"上", "下"}
var cnNums = []string{"一", "二", "三", "四", "五", "六", "七", "八", "九", "十", "十一", "十二", "十三", "十四", "十五", "十六"}

// ResplitChapter splits one chapter into parts of about target words, naming them 原标题（上/下）.
func ResplitChapter(title, body string, target int) []ChapterDraft {
	parts := SplitBySize(body, target, "%d")
	if len(parts) <= 1 {
		return []ChapterDraft{{Title: title, Kind: "body", Included: true, Text: body, WordCount: WordCount(body)}}
	}
	for i := range parts {
		suffix := fmt.Sprint(i + 1)
		if len(parts) == 2 {
			suffix = cnParts[i]
		} else if i < len(cnNums) {
			suffix = cnNums[i]
		}
		parts[i].Title = fmt.Sprintf("%s（%s）", title, suffix)
	}
	return parts
}
