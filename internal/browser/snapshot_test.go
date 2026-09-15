package browser

import (
	"testing"
)

// ax monta um nó da árvore de acessibilidade como o CDP entrega. Só os campos
// que a leitura usa importam aqui.
func ax(nodeID, parentID, role, name string, backend int) axNode {
	return axNode{
		NodeID:           nodeID,
		ParentID:         parentID,
		Role:             axVal{Type: "role", Value: role},
		Name:             axVal{Type: "string", Value: name},
		BackendDOMNodeID: backend,
	}
}

// fixtureÁrvore cobre os cortes que o código já faz hoje: rodapé, blocos de
// pular navegação, eco do nome do ancestral, irmãos idênticos, invólucro
// anônimo e texto só de separadores.
func fixtureÁrvore() []axNode {
	return []axNode{
		ax("root", "", "RootWebArea", "", 0),

		ax("home", "root", "link", "Home", 1),

		ax("skip", "root", "link", "Skip to main content", 0),

		ax("footer", "root", "contentinfo", "", 0),
		ax("foottext", "footer", "StaticText", "© 2026", 0),

		// Invólucro anônimo que não é interessante: some e o texto sobe.
		ax("wrap", "root", "generic", "", 0),
		ax("hellotext", "wrap", "StaticText", "Hello", 0),

		// Invólucro anônimo estrutural (paragraph) sem texto próprio: some e o
		// alvo de dentro sobe no lugar.
		ax("anon", "root", "paragraph", "", 0),
		ax("buylink", "anon", "link", "Buy", 5),

		// Texto só de separadores: não é conteúdo.
		ax("sep", "root", "StaticText", "· · ·", 0),

		// Container sem alvo, sem texto e sem filho: a linha some.
		ax("vazio", "root", "region", "", 0),

		// Irmãos idênticos: o mesmo alvo oferecido duas vezes, fica o primeiro.
		ax("grupo", "root", "region", "Grupo", 0),
		ax("same1", "grupo", "link", "Same", 20),
		ax("same2", "grupo", "link", "Same", 21),

		// Eco do nome do ancestral via texto coletado (repeatOf).
		ax("echolink", "root", "link", "Docs", 12),
		ax("echopara", "echolink", "paragraph", "", 0),
		ax("echotext", "echopara", "StaticText", "Docs", 0),

		// Invólucro que repete o nome do pai some no childIDs.
		ax("xlink", "root", "link", "X", 10),
		ax("xwrap", "xlink", "generic", "X", 0),
		ax("xtext", "xwrap", "StaticText", "X", 0),
	}
}

func TestMontarTexto_CortaCromoERuido(t *testing.T) {
	snap := montarTexto(fixtureÁrvore(), SnapshotOptions{})

	esperado := `- link "Home" [ref=e1]
- text: Hello
- link "Buy" [ref=e2]
- region "Grupo"
  - link "Same" [ref=e3]
- link "Docs" [ref=e4]
- link "X" [ref=e5]`
	if snap.Text != esperado {
		t.Errorf("texto divergiu:\n--- obtido ---\n%s\n--- esperado ---\n%s", snap.Text, esperado)
	}
	if snap.Count != 7 {
		t.Errorf("Count = %d, esperado 7", snap.Count)
	}

	refs := map[string]int{"e1": 1, "e2": 5, "e3": 20, "e4": 12, "e5": 10}
	if len(snap.Refs) != len(refs) {
		t.Fatalf("Refs = %v, esperado %v", snap.Refs, refs)
	}
	for k, v := range refs {
		if snap.Refs[k] != v {
			t.Errorf("Refs[%q] = %d, esperado %d", k, snap.Refs[k], v)
		}
	}
}

// Com Tudo, o corte de cromo é desligado: rodapé e skip-link reaparecem.
func TestMontarTexto_TudoDesligaCorte(t *testing.T) {
	snap := montarTexto(fixtureÁrvore(), SnapshotOptions{Tudo: true})

	esperado := `- link "Home" [ref=e1]
- link "Skip to main content"
- contentinfo: © 2026
- text: Hello
- link "Buy" [ref=e2]
- region "Grupo"
  - link "Same" [ref=e3]
- link "Docs" [ref=e4]
- link "X" [ref=e5]`
	if snap.Text != esperado {
		t.Errorf("texto divergiu:\n--- obtido ---\n%s\n--- esperado ---\n%s", snap.Text, esperado)
	}
}

// A geração da leitura entra na ref, para ref antiga ser recusada depois.
func TestMontarTexto_GeracaoNaRef(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("b", "root", "button", "Enviar", 7),
	}
	snap := montarTexto(nodes, SnapshotOptions{Gen: 9})
	if snap.Text != `- button "Enviar" [ref=e1#9]` {
		t.Errorf("texto = %q", snap.Text)
	}
	if snap.Refs["e1#9"] != 7 {
		t.Errorf("Refs = %v", snap.Refs)
	}
}

// RefsOnly lista só os alvos acionáveis, sem texto solto.
func TestMontarTexto_RefsOnly(t *testing.T) {
	nodes := []axNode{
		ax("root", "", "RootWebArea", "", 0),
		ax("txt", "root", "StaticText", "texto solto", 0),
		ax("b", "root", "button", "Enviar", 3),
	}
	snap := montarTexto(nodes, SnapshotOptions{RefsOnly: true})
	if snap.Text != `- button "Enviar" [ref=e1]` {
		t.Errorf("texto = %q", snap.Text)
	}
}

// Sem MaxNodes, o teto padrão é 1500; com teto baixo, a leitura trunca.
func TestMontarTexto_Trunca(t *testing.T) {
	nodes := []axNode{ax("root", "", "RootWebArea", "", 0)}
	for i := 0; i < 5; i++ {
		nodes = append(nodes, ax("t"+string(rune('a'+i)), "root", "button", "b", i+1))
	}
	snap := montarTexto(nodes, SnapshotOptions{MaxNodes: 2})
	if !snap.Truncated {
		t.Fatalf("esperava Truncated; texto=%q", snap.Text)
	}
	if snap.Count != 2 {
		t.Errorf("Count = %d, esperado 2", snap.Count)
	}
}
