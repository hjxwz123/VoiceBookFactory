package text

// Op is a diff operation over sequences.
type Op struct {
	Kind string `json:"kind"` // equal | insert | delete
	A    int    `json:"a"`    // index in old (-1 for insert)
	B    int    `json:"b"`    // index in new (-1 for delete)
}

// DiffStrings computes an LCS diff of two string slices (paragraph-level).
func DiffStrings(a, b []string) []Op {
	n, m := len(a), len(b)
	// trim common prefix/suffix to keep the table small
	pre := 0
	for pre < n && pre < m && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < n-pre && suf < m-pre && a[n-1-suf] == b[m-1-suf] {
		suf++
	}
	aa, bb := a[pre:n-suf], b[pre:m-suf]
	var ops []Op
	for i := 0; i < pre; i++ {
		ops = append(ops, Op{"equal", i, i})
	}
	if len(aa)*len(bb) > 4_000_000 {
		// too large: treat middle as replaced
		for i := range aa {
			ops = append(ops, Op{"delete", pre + i, -1})
		}
		for j := range bb {
			ops = append(ops, Op{"insert", -1, pre + j})
		}
	} else {
		dp := make([][]int32, len(aa)+1)
		for i := range dp {
			dp[i] = make([]int32, len(bb)+1)
		}
		for i := len(aa) - 1; i >= 0; i-- {
			for j := len(bb) - 1; j >= 0; j-- {
				if aa[i] == bb[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else if dp[i+1][j] >= dp[i][j+1] {
					dp[i][j] = dp[i+1][j]
				} else {
					dp[i][j] = dp[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < len(aa) && j < len(bb) {
			switch {
			case aa[i] == bb[j]:
				ops = append(ops, Op{"equal", pre + i, pre + j})
				i++
				j++
			case dp[i+1][j] >= dp[i][j+1]:
				ops = append(ops, Op{"delete", pre + i, -1})
				i++
			default:
				ops = append(ops, Op{"insert", -1, pre + j})
				j++
			}
		}
		for ; i < len(aa); i++ {
			ops = append(ops, Op{"delete", pre + i, -1})
		}
		for ; j < len(bb); j++ {
			ops = append(ops, Op{"insert", -1, pre + j})
		}
	}
	for k := 0; k < suf; k++ {
		ops = append(ops, Op{"equal", n - suf + k, m - suf + k})
	}
	return ops
}

// CharOp is a character-level diff op with rune ranges.
type CharOp struct {
	Kind   string `json:"kind"` // equal | insert | delete | replace
	AStart int    `json:"aStart"`
	AEnd   int    `json:"aEnd"`
	BStart int    `json:"bStart"`
	BEnd   int    `json:"bEnd"`
}

// DiffRunes computes a character-level diff (used by review to classify missing/extra/wrong).
func DiffRunes(a, b []rune) []CharOp {
	n, m := len(a), len(b)
	if n*m > 9_000_000 {
		return []CharOp{{Kind: "replace", AStart: 0, AEnd: n, BStart: 0, BEnd: m}}
	}
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	type step struct {
		k    byte
		i, j int
	}
	var steps []step
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			steps = append(steps, step{'=', i, j})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			steps = append(steps, step{'-', i, j})
			i++
		default:
			steps = append(steps, step{'+', i, j})
			j++
		}
	}
	for ; i < n; i++ {
		steps = append(steps, step{'-', i, m})
	}
	for ; j < m; j++ {
		steps = append(steps, step{'+', n, j})
	}
	// group consecutive steps
	var ops []CharOp
	for k := 0; k < len(steps); {
		s := steps[k]
		if s.k == '=' {
			e := k
			for e < len(steps) && steps[e].k == '=' {
				e++
			}
			ops = append(ops, CharOp{"equal", s.i, steps[e-1].i + 1, s.j, steps[e-1].j + 1})
			k = e
			continue
		}
		e := k
		dels, ins := 0, 0
		for e < len(steps) && steps[e].k != '=' {
			if steps[e].k == '-' {
				dels++
			} else {
				ins++
			}
			e++
		}
		op := CharOp{AStart: s.i, AEnd: s.i + dels, BStart: s.j, BEnd: s.j + ins}
		switch {
		case dels > 0 && ins > 0:
			op.Kind = "replace"
		case dels > 0:
			op.Kind = "delete"
		default:
			op.Kind = "insert"
		}
		ops = append(ops, op)
		k = e
	}
	return ops
}

// Similarity returns LCS ratio in [0,1].
func Similarity(a, b []rune) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a)*len(b) > 4_000_000 {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	l := prev[len(b)]
	mx := len(a)
	if len(b) > mx {
		mx = len(b)
	}
	return float64(l) / float64(mx)
}
