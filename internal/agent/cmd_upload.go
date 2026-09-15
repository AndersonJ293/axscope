package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AndersonJ293/axscope/internal/browser"
	"github.com/AndersonJ293/axscope/internal/protocol"
)

// upload manda um arquivo para a página.
//
// Dois caminhos, porque a web recebe arquivo de duas formas: o `<input
// type=file>` — caminho de formulário, quase sempre escondido atrás de um botão
// — e a dropzone, que só entende arraste. O alvo decide qual; sem alvo vale o
// primeiro `<input type=file>`, que é o palpite que costuma acertar.
func (a *Agent) upload(ctx context.Context, sess *browser.Session, req protocol.Request) protocol.Response {
	caminho := req.String("arquivo")
	if caminho == "" {
		return protocol.Fail(fmt.Errorf("uso: axscope upload <arquivo> [alvo=<ref|texto|css=>]"))
	}
	info, err := os.Stat(caminho)
	if err != nil {
		return protocol.Fail(fmt.Errorf("arquivo: %w", err))
	}
	if info.IsDir() {
		return protocol.Fail(fmt.Errorf("%s é uma pasta, não um arquivo", caminho))
	}

	var (
		t   *browser.Target
		sid string
	)
	spec := req.String("alvo")
	if spec != "" {
		t, sid, err = a.resolve(ctx, sess, spec)
		if err != nil {
			return protocol.Fail(err)
		}
	} else {
		sid, err = a.activeSID(sess)
		if err != nil {
			return protocol.Fail(err)
		}
		obj, err := browser.PrimeiroInputDeArquivo(ctx, a.client(), sid)
		if err != nil {
			return protocol.Fail(err)
		}
		t = &browser.Target{ObjectID: obj}
	}
	ehInput, err := browser.EhInputDeArquivo(ctx, a.client(), sid, t.ObjectID)
	if err != nil {
		return protocol.Fail(err)
	}

	before := a.errCount(sess, sid)
	via := "input"
	if ehInput {
		err = browser.SetFileInput(ctx, a.client(), sid, t.ObjectID, caminho)
	} else {
		via = "dropzone"
		err = browser.SoltaArquivo(ctx, a.client(), sid, t, caminho, sess.Presenter)
	}
	if err != nil {
		return protocol.Fail(err)
	}

	label := fmt.Sprintf("upload %s [%s]", filepath.Base(caminho), via)
	if spec != "" {
		label = fmt.Sprintf("upload %s em %s [%s]", filepath.Base(caminho), spec, via)
	}
	return ok(a.finish(ctx, sess, sid, label, before))
}
