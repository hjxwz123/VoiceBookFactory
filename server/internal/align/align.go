// Package align maps recognized speech (ASR words with timestamps) onto script segments.
// It powers smart alignment (slicing long recordings into per-line takes) and smart review
// (finding missing, extra or wrong words).
package align

import (
	"sort"
	"strings"
	"unicode"

	"voicebook/internal/text"
)

type Word struct {
	Text    string
	StartMs int64
	EndMs   int64
}

type SegText struct {
	ID   string
	Text string
}

type Match struct {
	SegmentID    string
	StartMs      int64
	EndMs        int64
	Confidence   float64
	Alternatives []Span
}

type Span struct {
	StartMs    int64
	EndMs      int64
	Text       string
	Similarity float64
}

type Result struct {
	Matches []Match
	Extras  []Span
	Missing []string
}

var digitCN = map[rune]rune{'0': '零', '1': '一', '2': '二', '3': '三', '4': '四', '5': '五', '6': '六', '7': '七', '8': '八', '9': '九'}

// Norm normalizes a string to comparable runes and returns the original rune offset of each.
func Norm(s string) ([]rune, []int) {
	var out []rune
	var pos []int
	i := 0
	for _, r := range strings.ToLower(s) {
		if d, ok := digitCN[r]; ok {
			r = d
		}
		if unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
			pos = append(pos, i)
		}
		i++
	}
	return out, pos
}

type tchar struct {
	r          rune
	start, end int64
}

func transcriptChars(words []Word) []tchar {
	var out []tchar
	for _, w := range words {
		rs, _ := Norm(w.Text)
		if len(rs) == 0 {
			continue
		}
		step := float64(w.EndMs-w.StartMs) / float64(len(rs))
		for i, r := range rs {
			out = append(out, tchar{r, w.StartMs + int64(float64(i)*step), w.StartMs + int64(float64(i+1)*step)})
		}
	}
	return out
}

// mapSequences returns for each index of a the matched index in b (or -1).
func mapSequences(a, b []rune) []int {
	m := make([]int, len(a))
	for i := range m {
		m[i] = -1
	}
	const n = 5
	type anchor struct{ sa, sb int }
	countA := map[string]int{}
	posA := map[string]int{}
	for i := 0; i+n <= len(a); i++ {
		g := string(a[i : i+n])
		countA[g]++
		posA[g] = i
	}
	countB := map[string]int{}
	posB := map[string]int{}
	for i := 0; i+n <= len(b); i++ {
		g := string(b[i : i+n])
		countB[g]++
		posB[g] = i
	}
	var anchors []anchor
	for g, c := range countA {
		if c == 1 && countB[g] == 1 {
			anchors = append(anchors, anchor{posA[g], posB[g]})
		}
	}
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].sa < anchors[j].sa })
	// longest increasing subsequence on sb keeps a monotonic, consistent set of anchors
	if len(anchors) > 0 {
		tails := []int{}
		prev := make([]int, len(anchors))
		idx := []int{}
		for i, an := range anchors {
			lo, hi := 0, len(tails)
			for lo < hi {
				mid := (lo + hi) / 2
				if anchors[tails[mid]].sb < an.sb {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			if lo > 0 {
				prev[i] = tails[lo-1]
			} else {
				prev[i] = -1
			}
			if lo == len(tails) {
				tails = append(tails, i)
			} else {
				tails[lo] = i
			}
		}
		for k := tails[len(tails)-1]; k >= 0; k = prev[k] {
			idx = append(idx, k)
		}
		var kept []anchor
		for i := len(idx) - 1; i >= 0; i-- {
			kept = append(kept, anchors[idx[i]])
		}
		anchors = kept
	}
	for _, an := range anchors {
		for k := 0; k < n; k++ {
			m[an.sa+k] = an.sb + k
		}
	}
	// align the gaps between anchored regions with edit-distance DP
	type gap struct{ a0, a1, b0, b1 int }
	var gaps []gap
	pa, pb := 0, 0
	i := 0
	for i < len(a) {
		if m[i] < 0 {
			i++
			continue
		}
		if i > pa {
			gaps = append(gaps, gap{pa, i, pb, m[i]})
		}
		for i < len(a) && m[i] >= 0 {
			pb = m[i] + 1
			i++
		}
		pa = i
	}
	if pa < len(a) {
		gaps = append(gaps, gap{pa, len(a), pb, len(b)})
	}
	for _, g := range gaps {
		if g.b1 < g.b0 {
			continue
		}
		dpAlign(a[g.a0:g.a1], b[g.b0:g.b1], func(ia, ib int) { m[g.a0+ia] = g.b0 + ib })
	}
	return m
}

func dpAlign(a, b []rune, set func(int, int)) {
	n, mm := len(a), len(b)
	if n == 0 || mm == 0 || n*mm > 6_000_000 {
		return
	}
	d := make([][]int32, n+1)
	for i := range d {
		d[i] = make([]int32, mm+1)
		d[i][0] = int32(i)
	}
	for j := 0; j <= mm; j++ {
		d[0][j] = int32(j)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= mm; j++ {
			c := int32(1)
			if a[i-1] == b[j-1] {
				c = 0
			}
			best := d[i-1][j-1] + c
			if v := d[i-1][j] + 1; v < best {
				best = v
			}
			if v := d[i][j-1] + 1; v < best {
				best = v
			}
			d[i][j] = best
		}
	}
	i, j := n, mm
	for i > 0 && j > 0 {
		c := int32(1)
		if a[i-1] == b[j-1] {
			c = 0
		}
		switch {
		case d[i][j] == d[i-1][j-1]+c:
			if c == 0 {
				set(i-1, j-1)
			}
			i--
			j--
		case d[i][j] == d[i-1][j]+1:
			i--
		default:
			j--
		}
	}
}

// Align maps segments onto a transcript.
func Align(segs []SegText, words []Word) Result {
	tc := transcriptChars(words)
	var a []rune
	var owner []int
	for si, s := range segs {
		rs, _ := Norm(s.Text)
		for _, r := range rs {
			a = append(a, r)
			owner = append(owner, si)
		}
	}
	b := make([]rune, len(tc))
	for i, c := range tc {
		b[i] = c.r
	}
	m := mapSequences(a, b)
	// collect mapped transcript positions per segment
	mapped := make([][]int, len(segs))
	totals := make([]int, len(segs))
	for i, si := range owner {
		totals[si]++
		if m[i] >= 0 {
			mapped[si] = append(mapped[si], m[i])
		}
	}
	used := make([]bool, len(b))
	var res Result
	for si, s := range segs {
		pos := mapped[si]
		// keep only the densest cluster so stray matches far away don't stretch the window
		if len(pos) > 0 {
			sort.Ints(pos)
			gapLimit := totals[si]/3 + 6
			bestS, bestE, curS := 0, 0, 0
			for k := 1; k <= len(pos); k++ {
				if k == len(pos) || pos[k]-pos[k-1] > gapLimit {
					if k-curS > bestE-bestS {
						bestS, bestE = curS, k
					}
					curS = k
				}
			}
			pos = pos[bestS:bestE]
		}
		conf := 0.0
		if totals[si] > 0 {
			conf = float64(len(pos)) / float64(totals[si])
		}
		if len(pos) == 0 || conf < 0.3 {
			res.Missing = append(res.Missing, s.ID)
			continue
		}
		for _, p := range pos {
			used[p] = true
		}
		res.Matches = append(res.Matches, Match{SegmentID: s.ID, StartMs: tc[pos[0]].start, EndMs: tc[pos[len(pos)-1]].end, Confidence: round2(conf)})
	}
	// pad boundaries without overlapping neighbours
	for i := range res.Matches {
		mt := &res.Matches[i]
		lo := mt.StartMs - 90
		hi := mt.EndMs + 140
		if i > 0 {
			mid := (res.Matches[i-1].EndMs + mt.StartMs) / 2
			if lo < mid {
				lo = mid
			}
		}
		if i+1 < len(res.Matches) {
			mid := (mt.EndMs + res.Matches[i+1].StartMs) / 2
			if hi > mid {
				hi = mid
			}
		}
		if lo < 0 {
			lo = 0
		}
		mt.StartMs, mt.EndMs = lo, hi
	}
	// unmatched transcript runs are extras (outtakes, chatter, repeated takes)
	for i := 0; i < len(b); {
		if used[i] {
			i++
			continue
		}
		j := i
		for j < len(b) && !used[j] {
			j++
		}
		if j-i >= 4 {
			var sb strings.Builder
			for k := i; k < j; k++ {
				sb.WriteRune(b[k])
			}
			res.Extras = append(res.Extras, Span{StartMs: tc[i].start, EndMs: tc[j-1].end, Text: sb.String()})
		}
		i = j
	}
	attachRepeats(segs, &res)
	return res
}

// attachRepeats turns extras that repeat a nearby line into alternatives; the last take becomes the default.
func attachRepeats(segs []SegText, res *Result) {
	segNorm := map[string][]rune{}
	for _, s := range segs {
		rs, _ := Norm(s.Text)
		segNorm[s.ID] = rs
	}
	var remaining []Span
	for _, ex := range res.Extras {
		best, bestSim := -1, 0.0
		exr := []rune(ex.Text)
		for i, mt := range res.Matches {
			if abs64(mt.StartMs-ex.StartMs) > 60_000 {
				continue
			}
			sim := text.Similarity(segNorm[mt.SegmentID], exr)
			// an outtake often starts with chatter ("嗯重来一下") before the retake: measure containment too
			if seg := segNorm[mt.SegmentID]; len(seg) > 0 && len(exr) <= 2*len(seg)+8 {
				if c := containment(seg, exr); c > sim {
					sim = c
				}
			}
			if sim > bestSim {
				best, bestSim = i, sim
			}
		}
		if best >= 0 && bestSim >= 0.72 {
			mt := &res.Matches[best]
			alt := Span{StartMs: ex.StartMs - 90, EndMs: ex.EndMs + 140, Text: ex.Text, Similarity: round2(bestSim)}
			if alt.StartMs > mt.StartMs {
				// later take wins by default; keep the earlier as an alternative
				mt.Alternatives = append(mt.Alternatives, Span{StartMs: mt.StartMs, EndMs: mt.EndMs, Similarity: mt.Confidence})
				mt.StartMs, mt.EndMs = alt.StartMs, alt.EndMs
				mt.Confidence = round2((mt.Confidence + bestSim) / 2)
			} else {
				mt.Alternatives = append(mt.Alternatives, alt)
			}
			continue
		}
		remaining = append(remaining, ex)
	}
	res.Extras = remaining
}

// Finding is a text discrepancy between the script and what was heard.
type Finding struct {
	Type     string // missing | extra | wrong
	Start    int    // rune offsets in the original expected text
	End      int
	Expected string
	Actual   string
}

// Compare diffs expected text against recognized text.
func Compare(expected, heard string) ([]Finding, float64) {
	ea, epos := Norm(expected)
	ha, _ := Norm(heard)
	if len(ea) == 0 {
		return nil, 1
	}
	ops := text.DiffRunes(ea, ha)
	var out []Finding
	exRunes := []rune(strings.ToLower(expected))
	_ = exRunes
	origRunes := []rune(expected)
	span := func(a0, a1 int) (int, int, string) {
		if a0 >= len(epos) {
			return len(origRunes), len(origRunes), ""
		}
		s := epos[a0]
		e := s + 1
		if a1-1 < len(epos) && a1 > a0 {
			e = epos[a1-1] + 1
		}
		if e > len(origRunes) {
			e = len(origRunes)
		}
		return s, e, string(origRunes[s:e])
	}
	matched := 0
	for _, op := range ops {
		switch op.Kind {
		case "equal":
			matched += op.AEnd - op.AStart
		case "delete":
			s, e, exp := span(op.AStart, op.AEnd)
			out = append(out, Finding{Type: "missing", Start: s, End: e, Expected: exp})
		case "insert":
			pos := len(origRunes)
			if op.AStart < len(epos) {
				pos = epos[op.AStart]
			}
			out = append(out, Finding{Type: "extra", Start: pos, End: pos, Actual: string(ha[op.BStart:op.BEnd])})
		case "replace":
			s, e, exp := span(op.AStart, op.AEnd)
			out = append(out, Finding{Type: "wrong", Start: s, End: e, Expected: exp, Actual: string(ha[op.BStart:op.BEnd])})
		}
	}
	return out, float64(matched) / float64(len(ea))
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// containment is the share of a's runes found in order inside b (LCS / len(a)).
func containment(a, b []rune) float64 {
	if len(a) == 0 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				cur[j] = prev[j-1] + 1
			case prev[j] >= cur[j-1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(b)]) / float64(len(a))
}
