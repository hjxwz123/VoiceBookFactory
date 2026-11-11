package core

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"voicebook/internal/ai/tts"
	"voicebook/internal/model"
)

// LLMCue is a performance tag as returned by the script annotator: anchored by the text right before it.
type LLMCue struct {
	Tag   string `json:"tag"`
	After string `json:"after"`
}

func toTTSCues(cs []model.Cue) []tts.Cue {
	out := make([]tts.Cue, 0, len(cs))
	for _, c := range cs {
		out = append(out, tts.Cue{At: c.At, Tag: c.Tag})
	}
	return out
}

// NormalizeSegmentCues validates tags and clamps offsets to the line.
func NormalizeSegmentCues(cs []model.Cue, text string) []model.Cue {
	norm := tts.NormalizeCues(toTTSCues(cs), utf8.RuneCountInString(text))
	out := make([]model.Cue, 0, len(norm))
	for _, c := range norm {
		out = append(out, model.Cue{At: c.At, Tag: c.Tag})
	}
	return out
}

// CuesFromLLM resolves anchored cues to rune offsets. Earlier-style whole-line tags are placed at the start.
func CuesFromLLM(text string, cues []LLMCue, legacy []string) []model.Cue {
	var out []model.Cue
	for _, t := range legacy {
		if k, ok := tts.LegacyTagMap[t]; ok {
			out = append(out, model.Cue{At: 0, Tag: k})
		}
	}
	for _, c := range cues {
		tag, ok := tts.LookupPerfTag(c.Tag)
		if !ok {
			continue
		}
		at, found := anchorOffset(text, c.After)
		if !found {
			if tag.Kind != "style" {
				continue // a sound with an unknown position would land in the wrong place
			}
			at = 0
		}
		out = append(out, model.Cue{At: at, Tag: tag.Key})
	}
	return NormalizeSegmentCues(out, text)
}

// anchorOffset returns the rune offset right after `after` in text ("" = start of line).
func anchorOffset(text, after string) (int, bool) {
	after = strings.TrimSpace(after)
	if after == "" {
		return 0, true
	}
	if i := strings.Index(text, after); i >= 0 {
		return utf8.RuneCountInString(text[:i+len(after)]), true
	}
	// tolerate punctuation / whitespace differences: match on letters and ideographs only
	key := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }
	var want []rune
	for _, r := range after {
		if key(r) {
			want = append(want, r)
		}
	}
	if len(want) == 0 {
		return 0, false
	}
	runes := []rune(text)
	for start := range runes {
		k, i := 0, start
		for i < len(runes) && k < len(want) {
			if !key(runes[i]) {
				i++
				continue
			}
			if runes[i] != want[k] {
				break
			}
			k++
			i++
		}
		if k == len(want) {
			return i, true
		}
	}
	return 0, false
}

// offsetRemapper maps rune offsets of `old` onto `updated` around the edited region
// (common prefix stays put, common suffix shifts, offsets inside the edit move to its end).
func offsetRemapper(old, updated string) func(int) (int, bool) {
	o, n := []rune(old), []rune(updated)
	p := 0
	for p < len(o) && p < len(n) && o[p] == n[p] {
		p++
	}
	s := 0
	for s < len(o)-p && s < len(n)-p && o[len(o)-1-s] == n[len(n)-1-s] {
		s++
	}
	delta := len(n) - len(o)
	return func(i int) (int, bool) {
		switch {
		case i <= p:
			return i, true
		case i >= len(o)-s:
			return i + delta, true
		default:
			return len(n) - s, false // inside the rewritten part
		}
	}
}

// RemapAnnotations keeps pronunciations and cues attached to the right characters after a text edit.
func RemapAnnotations(old, updated string, prons []model.Pronunciation, cues []model.Cue) ([]model.Pronunciation, []model.Cue) {
	if old == updated {
		return prons, cues
	}
	m := offsetRemapper(old, updated)
	nr := []rune(updated)
	outP := []model.Pronunciation{}
	for _, p := range prons {
		if at, ok := m(p.Offset); ok && at >= 0 && at < len(nr) && string(nr[at]) == p.Char {
			p.Offset = at
			outP = append(outP, p)
		}
	}
	var outC []model.Cue
	for _, c := range cues {
		at, _ := m(c.At)
		c.At = at
		outC = append(outC, c)
	}
	return outP, NormalizeSegmentCues(outC, updated)
}

// CueDisplayText renders cues as readable markers (e.g. "你啊【大笑】，真是…") for scripts handed to voice actors.
func CueDisplayText(text string, cues []model.Cue) string {
	if len(cues) == 0 {
		return text
	}
	runes := []rune(text)
	cues = NormalizeSegmentCues(cues, text)
	var b strings.Builder
	k := 0
	for i := 0; i <= len(runes); i++ {
		for k < len(cues) && cues[k].At == i {
			if t, ok := tts.LookupPerfTag(cues[k].Tag); ok {
				b.WriteString("【" + t.Label + "】")
			}
			k++
		}
		if i < len(runes) {
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}

func withoutSoundCues(cs []model.Cue) []model.Cue {
	out := cs[:0:0]
	for _, c := range cs {
		if t, ok := tts.LookupPerfTag(c.Tag); ok && t.Kind == "style" {
			out = append(out, c)
		}
	}
	return out
}

// instructionMeta are words that mark an explanation of the line rather than a direction for reading it.
var instructionMeta = []string{"台词", "旁白", "叙述", "提示语", "说话人", "广播剧", "删除", "标明", "表达", "本句", "该句", "原文", "此句", "这句"}

const maxInstructionRunes = 24

// CleanInstruction keeps only the performance direction from an LLM-written instruction:
// clauses that explain the line ("角色台词", "说话提示语，广播剧模式可删除") are dropped
// and the result is cut at a clause boundary to a short, speakable direction.
func CleanInstruction(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	clauses := strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("，,；;。.！!、\n", r) })
	var kept []string
	n := 0
	for _, c := range clauses {
		c = strings.TrimSpace(c)
		if c == "" || containsAny(c, instructionMeta) {
			continue
		}
		l := utf8.RuneCountInString(c)
		if len(kept) == 0 && l > maxInstructionRunes {
			return string([]rune(c)[:maxInstructionRunes])
		}
		if n+l+len(kept) > maxInstructionRunes {
			break
		}
		kept = append(kept, c)
		n += l
	}
	return strings.Join(kept, "，")
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
