package agent

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// reading is what the last snap of a tab said, kept so the next one can say only
// what changed and keep the same ref numbers.
type reading struct {
	url   string
	key   string // the options that shaped it: a delta compares like with like
	gen   int
	lines []string
	nums  map[int]int // backend id -> N of eN
}

var refGenRe = regexp.MustCompile(`\[ref=(e\d+)#\d+\]`)

// stable drops the generation from a line's ref: with stable numbers, a line
// that did not change reads the same in both generations.
func stable(line string) string { return refGenRe.ReplaceAllString(line, "[ref=$1]") }

// refNumbers turns a reading's refs into backend id -> number, the form the next
// reading reuses.
func refNumbers(refs map[string]int) map[int]int {
	out := make(map[int]int, len(refs))
	for ref, backend := range refs {
		name, _, _ := strings.Cut(strings.TrimPrefix(ref, "e"), "#")
		if n, err := strconv.Atoi(name); err == nil {
			out[backend] = n
		}
	}
	return out
}

// delta renders what changed between two readings as hunks of added (+) and
// removed (-) lines with one line of context, or ok=false when a full reading
// would be as short (most of the page changed).
func delta(prev, cur []string) (text string, added, removed int, ok bool) {
	a := make([]string, len(prev))
	for i, l := range prev {
		a[i] = stable(l)
	}
	b := make([]string, len(cur))
	for i, l := range cur {
		b[i] = stable(l)
	}
	ops := diffLines(a, b)

	var out []string
	const context = 1
	for i, op := range ops {
		if op.kind != ' ' {
			if op.kind == '+' {
				added++
				out = append(out, "+ "+cur[op.j])
			} else {
				removed++
				out = append(out, "- "+prev[op.i])
			}
			continue
		}
		near := false
		for d := 1; d <= context; d++ {
			if (i-d >= 0 && ops[i-d].kind != ' ') || (i+d < len(ops) && ops[i+d].kind != ' ') {
				near = true
			}
		}
		if near {
			out = append(out, "  "+cur[op.j])
		} else if len(out) > 0 && out[len(out)-1] != "  …" {
			out = append(out, "  …")
		}
	}
	for len(out) > 0 && out[len(out)-1] == "  …" {
		out = out[:len(out)-1]
	}
	if added+removed == 0 {
		return "", 0, 0, true
	}
	if len(out) >= len(cur) {
		return "", added, removed, false
	}
	return strings.Join(out, "\n"), added, removed, true
}

type diffOp struct {
	kind byte // ' ', '+', '-'
	i, j int  // index in prev (for ' ' and '-') and in cur (for ' ' and '+')
}

// diffLines is a longest-common-subsequence diff. A reading is capped at 1500
// lines, so the quadratic table stays small.
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', i, j})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', i, j})
			i++
		default:
			ops = append(ops, diffOp{'+', i, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', i, j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', i, j})
	}
	return ops
}

// defaultSnapBytes caps what one snap puts in the agent's context (~6k tokens).
// Past it the whole reading goes to a file and the answer carries its start and
// the path; AXSCOPE_SNAP_MAX_BYTES overrides it, 0 turns the cap off.
const defaultSnapBytes = 24000

func snapCap() int {
	n := defaultSnapBytes
	if v := os.Getenv("AXSCOPE_SNAP_MAX_BYTES"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	return max(n, 0)
}

// capReading keeps a reading within limit bytes: the start, cut at a line, and
// where the rest is. An agent that needs the whole page reads the file; one that
// needs less narrows the snap, which the note names.
func capReading(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	f, err := os.CreateTemp("", "axscope-snap-*.txt")
	path := ""
	if err == nil {
		_, _ = f.WriteString(text)
		_ = f.Close()
		path = f.Name()
	}
	cut := strings.LastIndexByte(text[:limit], '\n')
	if cut <= 0 {
		cut = limit
	}
	head := text[:cut]
	shown := strings.Count(head, "\n") + 1
	total := strings.Count(text, "\n") + 1
	where := "it could not be saved"
	if path != "" {
		where = "the whole reading is in " + path
	}
	return fmt.Sprintf("%s\n-- cut at %d of %d lines (%d KB); %s — or narrow it: within=<target>, depth=2, --viewport",
		head, shown, total, len(text)/1024, where)
}
