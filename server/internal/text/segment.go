package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Unit is the smallest annotatable piece of a chapter. Units are produced by deterministic rules
// so the original text is preserved exactly; the LLM only labels them.
type Unit struct {
	Index      int    `json:"index"`
	Para       int    `json:"para"`
	Text       string `json:"text"`
	Quote      bool   `json:"quote"`
	SceneBreak bool   `json:"sceneBreak"`
}

var closeOf = map[rune]rune{'“': '”', '「': '」', '『': '』', '"': '"', '﹁': '﹂'}

const trailPunct = "，。！？、；：,.!?…～~"

func trimPara(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '　' })
}

// Paragraphs returns cleaned non-empty paragraphs and whether a scene break precedes each.
func Paragraphs(raw string) ([]string, []bool) {
	var paras []string
	var breaks []bool
	pendingBreak := false
	for _, l := range strings.Split(Clean(raw), "\n") {
		t := trimPara(l)
		if t == "" {
			continue
		}
		if sepRe.MatchString(t) {
			pendingBreak = true
			continue
		}
		paras = append(paras, t)
		breaks = append(breaks, pendingBreak)
		pendingBreak = false
	}
	return paras, breaks
}

// Segment splits chapter text into units: dialogue inside quotes vs narration, with long pieces
// split at sentence boundaries.
func Segment(raw string, maxLen int) []Unit {
	if maxLen <= 0 {
		maxLen = 120
	}
	paras, breaks := Paragraphs(raw)
	var units []Unit
	for pi, p := range paras {
		pieces := splitQuotes(p)
		first := true
		for _, pc := range pieces {
			for _, part := range splitLong(pc.text, pc.quote, maxLen) {
				u := Unit{Para: pi, Text: part, Quote: pc.quote}
				if first {
					u.SceneBreak = breaks[pi]
					first = false
				}
				units = append(units, u)
			}
		}
	}
	for i := range units {
		units[i].Index = i
	}
	return units
}

type piece struct {
	text  string
	quote bool
}

func isPunctOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) && r != '　' {
			return false
		}
	}
	return true
}

func splitQuotes(p string) []piece {
	var out []piece
	var buf []rune
	runes := []rune(p)
	flush := func(quote bool) {
		if len(buf) == 0 {
			return
		}
		out = append(out, piece{text: string(buf), quote: quote})
		buf = nil
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		closer, isOpen := closeOf[r]
		if !isOpen {
			buf = append(buf, r)
			continue
		}
		// find the matching close quote, honoring nesting of the same pair
		depth := 0
		end := -1
		for j := i + 1; j < len(runes); j++ {
			if r != '"' && runes[j] == r {
				depth++
			} else if runes[j] == closer {
				if depth == 0 {
					end = j
					break
				}
				depth--
			}
		}
		if end < 0 {
			buf = append(buf, r)
			continue
		}
		flush(false)
		buf = append(buf, runes[i:end+1]...)
		k := end + 1
		for k < len(runes) && strings.ContainsRune(trailPunct, runes[k]) {
			buf = append(buf, runes[k])
			k++
		}
		flush(true)
		i = k - 1
	}
	flush(false)
	// merge punctuation-only narration into neighbours
	var merged []piece
	for _, pc := range out {
		if !pc.quote && isPunctOnly(pc.text) && len(merged) > 0 {
			merged[len(merged)-1].text += pc.text
			continue
		}
		merged = append(merged, pc)
	}
	if len(merged) > 1 && !merged[0].quote && isPunctOnly(merged[0].text) {
		merged[1].text = merged[0].text + merged[1].text
		merged = merged[1:]
	}
	return merged
}

const sentenceEnd = "。！？!?；;…"

func splitLong(s string, quote bool, maxLen int) []string {
	limit := maxLen
	if quote {
		limit = maxLen * 4 / 3
	}
	if utf8.RuneCountInString(s) <= limit {
		return []string{s}
	}
	// split into sentences, keeping enders (and closing quotes/brackets that follow them)
	var sentences []string
	runes := []rune(s)
	start := 0
	for i := 0; i < len(runes); i++ {
		if strings.ContainsRune(sentenceEnd, runes[i]) {
			j := i + 1
			for j < len(runes) && (strings.ContainsRune(sentenceEnd, runes[j]) || strings.ContainsRune("”」』）)", runes[j])) {
				j++
			}
			sentences = append(sentences, string(runes[start:j]))
			start = j
			i = j - 1
		}
	}
	if start < len(runes) {
		sentences = append(sentences, string(runes[start:]))
	}
	var out []string
	cur := ""
	for _, sen := range sentences {
		if cur != "" && utf8.RuneCountInString(cur)+utf8.RuneCountInString(sen) > limit {
			out = append(out, cur)
			cur = ""
		}
		cur += sen
	}
	if cur != "" {
		out = append(out, cur)
	}
	// very long single sentences: split at commas
	var final []string
	for _, o := range out {
		if utf8.RuneCountInString(o) <= limit*3/2 {
			final = append(final, o)
			continue
		}
		rs := []rune(o)
		st := 0
		for i := 0; i < len(rs); i++ {
			if (rs[i] == '，' || rs[i] == ',') && i-st >= limit/2 {
				final = append(final, string(rs[st:i+1]))
				st = i + 1
			}
		}
		if st < len(rs) {
			final = append(final, string(rs[st:]))
		}
	}
	return final
}

// Normalize keeps letters/digits/Han for comparison (alignment, review).
func Normalize(s string) []rune {
	var out []rune
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}
