package text

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/mozillazg/go-pinyin"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var (
	pinyinOnce  sync.Once
	homophones  map[string]string // tone-marked pinyin -> common unambiguous char
	polyphonics map[rune]bool     // common characters with several readings
)

// isCommon reports whether r is a GB2312 level-1 (most frequently used) character.
func isCommon(r rune) bool {
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(string(r)))
	if err != nil || len(b) != 2 {
		return false
	}
	return b[0] >= 0xB0 && b[0] <= 0xD7 && b[1] >= 0xA1
}

func initPinyin() {
	homophones = map[string]string{}
	polyphonics = map[rune]bool{}
	keys := make([]int, 0, len(pinyin.PinyinDict))
	for k := range pinyin.PinyinDict {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		r := rune(k)
		if !unicode.Is(unicode.Han, r) || !isCommon(r) {
			continue
		}
		readings := strings.Split(pinyin.PinyinDict[k], ",")
		if len(readings) > 1 {
			polyphonics[r] = true
			continue
		}
		if _, ok := homophones[readings[0]]; !ok {
			homophones[readings[0]] = string(r)
		}
	}
}

// Readings returns tone-marked readings of a character (first is the most common).
func Readings(ch string) []string {
	r := []rune(ch)
	if len(r) == 0 {
		return nil
	}
	if v, ok := pinyin.PinyinDict[int(r[0])]; ok {
		return strings.Split(v, ",")
	}
	return nil
}

// IsPolyphonic reports whether a common character has multiple readings.
func IsPolyphonic(r rune) bool {
	pinyinOnce.Do(initPinyin)
	return polyphonics[r]
}

// PolyphonicsIn lists distinct polyphonic characters appearing in s.
func PolyphonicsIn(s string) []string {
	pinyinOnce.Do(initPinyin)
	seen := map[rune]bool{}
	var out []string
	for _, r := range s {
		if polyphonics[r] && !seen[r] {
			seen[r] = true
			out = append(out, string(r))
		}
	}
	return out
}

// Homophone returns a common single-reading character pronounced as py (tone marked), used as a
// fallback when a TTS provider cannot accept phoneme markup.
func Homophone(py string) string {
	pinyinOnce.Do(initPinyin)
	return homophones[strings.TrimSpace(py)]
}

// ToneNumber converts "háng" to "hang2" (some providers expect numbered tones).
func ToneNumber(py string) string {
	tones := map[rune][2]any{
		'ā': {'a', 1}, 'á': {'a', 2}, 'ǎ': {'a', 3}, 'à': {'a', 4},
		'ē': {'e', 1}, 'é': {'e', 2}, 'ě': {'e', 3}, 'è': {'e', 4},
		'ī': {'i', 1}, 'í': {'i', 2}, 'ǐ': {'i', 3}, 'ì': {'i', 4},
		'ō': {'o', 1}, 'ó': {'o', 2}, 'ǒ': {'o', 3}, 'ò': {'o', 4},
		'ū': {'u', 1}, 'ú': {'u', 2}, 'ǔ': {'u', 3}, 'ù': {'u', 4},
		'ǖ': {'v', 1}, 'ǘ': {'v', 2}, 'ǚ': {'v', 3}, 'ǜ': {'v', 4}, 'ü': {'v', 5},
	}
	var sb strings.Builder
	tone := 5
	for _, r := range py {
		if t, ok := tones[r]; ok {
			sb.WriteRune(t[0].(rune))
			if t[1].(int) != 5 {
				tone = t[1].(int)
			}
			continue
		}
		sb.WriteRune(r)
	}
	if tone == 5 {
		return sb.String() + "5"
	}
	return sb.String() + string(rune('0'+tone))
}

// PinyinOf returns the default reading for each Han rune (used for display hints).
func PinyinOf(s string) []string {
	args := pinyin.NewArgs()
	args.Style = pinyin.Tone
	var out []string
	for _, p := range pinyin.Pinyin(s, args) {
		if len(p) > 0 {
			out = append(out, p[0])
		}
	}
	return out
}
