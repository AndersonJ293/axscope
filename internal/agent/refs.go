package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AndersonJ293/axscope/internal/browser"
)

func (a *Agent) setRefs(refs map[string]int, gen int) {
	a.mu.Lock()
	a.refs = refs
	a.snapGen = gen
	a.mu.Unlock()
}

// nextGen é a geração da próxima leitura (a atual + 1).
func (a *Agent) nextGen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapGen + 1
}

// refGen separa a ref da geração: "e12#7" -> ("e12", 7, true).
func refGen(spec string) (string, int, bool) {
	i := strings.LastIndex(spec, "#")
	if i <= 0 || i == len(spec)-1 {
		return spec, 0, false
	}
	n, err := strconv.Atoi(spec[i+1:])
	if err != nil {
		return spec, 0, false
	}
	return spec[:i], n, true
}

func (a *Agent) currentRefs() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refs
}

// resolve transforma um alvo (ref/css/text/pos) em Target, recusando ref de
// leitura antiga: usar uma geração antiga não erra com aviso — aponta para o
// que hoje ocupa aquela posição.
func (a *Agent) resolve(ctx context.Context, sess *browser.Session, target string) (*browser.Target, string, error) {
	sid, err := a.activeSID(sess)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(target, "css=") && !strings.HasPrefix(target, "text=") {
		if _, gen, ok := refGen(target); ok {
			a.mu.Lock()
			atual := a.snapGen
			a.mu.Unlock()
			if gen != atual {
				return nil, sid, fmt.Errorf(
					"ref %q é de uma leitura antiga (a atual é #%d) — rode `snap` de novo", target, atual)
			}
		}
	}
	t, err := browser.ResolveTarget(ctx, a.client(), sid, a.currentRefs(), target)
	if err != nil {
		return nil, sid, err
	}
	return t, sid, nil
}
