package tts

import (
	"sort"
	"strings"
)

// Cue is a performance tag inserted into a line at a rune offset (0 = before the first character,
// len = after the last). The line's text itself is never modified; cues are rendered per provider.
type Cue struct {
	At  int    `json:"at"`
	Tag string `json:"tag"`
}

// PerfTag describes one performance tag. Keys equal Qwen-Audio-TTS tag names.
//   - style tags change the delivery of everything after them until the next style tag
//   - sound tags insert a non-verbal sound at that point
type PerfTag struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // style | sound
	Hint  string `json:"hint"`
}

var PerformanceTags = []PerfTag{
	{"sad", "悲伤", "style", "低落、难过"},
	{"crying", "哭腔", "style", "哭着说、哽咽"},
	{"angry", "愤怒", "style", "生气、斥责"},
	{"shouting", "大喊", "style", "高声喊叫"},
	{"deep and loud shouting", "低沉呐喊", "style", "浑厚有力地吼"},
	{"excited", "兴奋", "style", "激动、欢快"},
	{"amazed", "惊叹", "style", "惊奇、赞叹"},
	{"curious", "好奇", "style", "探询、疑惑"},
	{"sarcastic", "讽刺", "style", "反讽、阴阳怪气"},
	{"scornful", "轻蔑", "style", "不屑、鄙夷"},
	{"mischievously", "调皮", "style", "俏皮、捉弄"},
	{"empathetic", "共情", "style", "体贴、安慰"},
	{"serious", "严肃", "style", "郑重、认真"},
	{"reluctantly", "不情愿", "style", "勉强、犹豫"},
	{"bored", "无聊", "style", "提不起劲"},
	{"tired", "疲倦", "style", "疲惫、虚弱"},
	{"panicked", "惊慌", "style", "慌乱、急促"},
	{"trembling", "颤抖", "style", "害怕、声音发抖"},
	{"whispers", "耳语", "style", "压低声音说悄悄话"},
	{"asmr", "气声", "style", "轻柔的气声"},
	{"like dracula", "阴森", "style", "诡异、阴冷"},
	{"very slowly", "放慢", "style", "一字一顿、很慢"},
	{"very fast", "加快", "style", "语速很快"},
	{"laughing", "大笑", "sound", "笑出声"},
	{"giggles", "轻笑", "sound", "咯咯笑、偷笑"},
	{"snorts", "嗤笑", "sound", "冷笑、哼一声"},
	{"sighing", "叹气", "sound", "叹一口气"},
	{"gasp", "倒吸气", "sound", "吃惊、喘息"},
	{"cough", "咳嗽", "sound", "咳嗽声"},
	{"clears throat", "清嗓", "sound", "清清嗓子"},
}

var perfIndex = func() map[string]PerfTag {
	m := map[string]PerfTag{}
	for _, t := range PerformanceTags {
		m[t.Key] = t
		m[t.Label] = t
	}
	return m
}()

// LegacyTagMap maps the earlier Chinese performance tags to cue keys.
var LegacyTagMap = map[string]string{
	"笑": "laughing", "冷笑": "snorts", "叹气": "sighing", "哭腔": "crying", "哽咽": "crying", "喘息": "gasp",
	"轻声": "whispers", "气声": "asmr", "惊呼": "gasp", "咳嗽": "cough", "清嗓": "clears throat", "咯咯笑": "giggles",
}

// LookupPerfTag accepts a key, a Chinese label or a legacy tag.
func LookupPerfTag(s string) (PerfTag, bool) {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if t, ok := perfIndex[strings.ToLower(s)]; ok {
		return t, true
	}
	if t, ok := perfIndex[s]; ok {
		return t, true
	}
	if k, ok := LegacyTagMap[s]; ok {
		return perfIndex[k], true
	}
	return PerfTag{}, false
}

// NormalizeCues validates tags, clamps offsets to the text and orders cues by position.
func NormalizeCues(cues []Cue, textLen int) []Cue {
	out := []Cue{}
	seen := map[Cue]bool{}
	for _, c := range cues {
		t, ok := LookupPerfTag(c.Tag)
		if !ok {
			continue
		}
		c.Tag = t.Key
		if c.At < 0 {
			c.At = 0
		}
		if c.At > textLen {
			c.At = textLen
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// InsertCues renders cues as inline "[tag]" markers (Qwen-Audio-TTS syntax).
func InsertCues(text string, cues []Cue) string {
	if len(cues) == 0 {
		return text
	}
	runes := []rune(text)
	cues = NormalizeCues(cues, len(runes))
	var b strings.Builder
	k := 0
	for i := 0; i <= len(runes); i++ {
		for k < len(cues) && cues[k].At == i {
			b.WriteString("[" + cues[k].Tag + "]")
			k++
		}
		if i < len(runes) {
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}
