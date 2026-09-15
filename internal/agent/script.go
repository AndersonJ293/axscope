package agent

import (
	"fmt"
	"strings"
)

// squeeze reduz linhas em branco repetidas.
func squeeze(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// splitTokens divide uma linha respeitando aspas simples e duplas.
func splitTokens(line string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("aspas não fechadas")
	}
	if has || cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}
