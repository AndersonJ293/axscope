# Arquitetura do browser-use

Este documento é **contrato**: descreve as camadas, quem pode importar quem, e
as fases do refatoramento que tira o projeto do estado atual. Quem executa segue
isto; quem revisa confere isto.

## Diagnóstico (medido, não achismo)

| Problema | Evidência |
|---|---|
| `internal/browser` acumula 6 responsabilidades | 2910 linhas = 46% do código |
| ...e depende da **apresentação** | 26 chamadas a `overlay.` em `actions.go`/`session.go` |
| `internal/agent` é arquivo-Deus | 1007 linhas: ciclo de vida + roteador + 25 handlers + refs + parser |
| Roteador é `switch` paralelo à tabela de comandos | comando novo exige mexer em spec, switch e handler |
| `eval` **triplicado** | `page.Eval*`, `agent.evalString`, `browser.evalObject/evalString` |
| `page` existe para um só consumidor | apenas `overlay` usa |
| `cli` não diz o que é | é o cliente do daemon, usado por `cmd/bu` e `mcpsrv` |

## Camadas e direção das dependências

A dependência aponta **sempre para baixo**. Nunca para cima, nunca em ciclo.

```
   cmd/*                  entradas (CLI, probe)
     |
   agent                  orquestração: ciclo de vida + handlers de comando
     |
   browser                DOMÍNIO: abas, alvos, ações, leitura da tela
     |        \
   dom         render     (render é plugado por interface, ver abaixo)
     |
   cdp                    cliente do protocolo, cru
     |
   protocol  paths  command  installer     folhas (sem deps internas)
```

Regras:

1. **`browser` não conhece `render`.** O domínio declara uma porta
   (`browser.Presenter`) e o `agent` injeta a implementação. Hoje o domínio
   chama `overlay.Spotlight/…` direto — é inversão de dependência.
2. **`agent` não fala CDP direto.** Ele usa `browser` e `dom`. Exceções
   conscientes viram comentário explicando por quê.
3. **Uma responsabilidade por arquivo, ~400 linhas como teto.** Passou disso, é
   sinal de que duas coisas moram juntas.
4. **Comando novo se registra num lugar só.** Um registro `nome → handler`
   substitui o `switch`. A tabela de `command.Specs` continua sendo a fonte da
   verdade do que existe (CLI, ajuda e MCP derivam dela).
5. **Um único helper de `eval`/DOM** (`internal/dom`). Nada de terceira cópia.
6. **`cdp` é burro**: manda comando, espera resposta, entrega evento. Não sabe o
   que é uma aba, um alvo ou um clique.

## Estrutura alvo

```
cmd/bu/                 entrada: flags globais, install/engines/clean/mcp/serve/stop
cmd/cdpprobe/           entrada de diagnóstico

internal/
  protocol/             pedido/resposta (folha)
  command/              tabela de specs + parser (folha, usa protocol)
  paths/                caminhos em disco (folha)
  installer/            download de motores (folha)
  cdp/                  cliente CDP cru
  dom/                  acesso a DOM/runtime: Eval, EvalAwait, EvalString,
                        EvalObject, BoxOf, ScrollTo — um lugar só
  browser/              DOMÍNIO
    session.go            abas, anexação, navegação, convergência
    launcher.go           subir/anexar navegador, motores
    observe.go            console, rede, diálogos
    target.go             resolução de alvo: ref/css/text/pos
    input.go              clique, arraste, teclado, rolagem
    snapshot.go           árvore de acessibilidade → texto
    presenter.go          PORTA de apresentação (interface)
  render/               implementa browser.Presenter: cursor, HUD, ripple
  agent/                ORQUESTRAÇÃO
    agent.go              ciclo de vida (ensure, Close, setAgent)
    dispatch.go           registro nome → handler
    refs.go               refs e geração da leitura
    script.go             parser de roteiro
    cmd_navigate.go       open, back, forward, reload, wait, waitgone
    cmd_interact.go       click, hover, drag, fill, type, press, select, check, scroll
    cmd_inspect.go        snap, read, console, net, eval, status
    cmd_tabs.go           tabs, tab, newtab, closetab
    cmd_capture.go        shot, script
  daemon/               servidor de socket
  bridge/               ponte no navegador (extensão)
  daemonclient/         cliente do daemon (era `cli`)
  mcpsrv/               servidor MCP (stdio)
```

## Invariantes do refatoramento

- **Não muda comportamento.** É refatoramento: mesmas mensagens, mesmos
  resultados, mesmas flags. Se um comportamento precisa mudar, isso é outro
  commit, com justificativa.
- **Build e vet verdes ao fim de cada fase** (`go build ./... && go vet ./...`).
- **Sem gambiarra**: nada de alias/reexport para "não quebrar", nada de
  `catch` vazio, nada de arquivo morto. Quem sai, sai de verdade.
- **Comentário diz o porquê**, não o quê. É a convenção do projeto.
- **Nada de dependência nova** sem justificar (o binário é estático e leve de
  propósito).
- **Smoke test ao fim**: `tabs`, `snap`, `hover`, `drag`, `shot` no laboratório.

## Fases

Cada fase é independente e verificável. Não comece a seguinte sem a anterior
verde.

### Fase 0 — rede de segurança (antes de mexer em qualquer coisa)

Não existe teste nenhum hoje. Refatorar sem rede é aposta.

- Extrair de `snapshot.go` a parte **pura** (`montarTexto(nodes) string`) e
  testá-la com um fixture de árvore de acessibilidade. É onde mora todo o corte
  de ruído — a lógica mais frágil do projeto.
- Testar `command.Parse` (tabela de entradas → pedido esperado).
- Testar `refGen` e `splitTokens`.

Critério: `go test ./...` verde, com os testes cobrindo o corte de snapshot
(rodapé, skip-links, eco de nome, irmãos idênticos, invólucro anônimo).

### Fase 1 — inverter a apresentação

- Declarar `browser.Presenter` (cursor, press, spotlight, HUD).
- `browser` para de importar `overlay`; recebe a porta na sessão/agente.
- `overlay` vira `render` e implementa a interface.

Critério: `grep -r "internal/overlay" internal/browser` vazio; build verde.

### Fase 2 — um `dom` só

- `internal/dom` com `Eval`, `EvalAwait`, `EvalString`, `EvalObject`, `BoxOf`.
- `page` é absorvido; `agent.evalString`, `browser.evalObject`,
  `browser.evalString` e `boxOf` saem.

Critério: uma única implementação de cada helper; `internal/page` não existe
mais.

### Fase 3 — partir o `agent`

- `dispatch.go` com registro `map[string]handler`; o `switch` sai.
- Handlers nos arquivos `cmd_*.go` por área.
- `refs.go`, `script.go`.

Critério: nenhum arquivo em `internal/agent` acima de ~400 linhas; adicionar um
comando exige mexer em **dois** lugares (spec + handler) e não em três.

### Fase 4 — partir `browser/actions.go`

- `target.go` (resolução) e `input.go` (ações).

Critério: nenhum arquivo em `internal/browser` acima de ~450 linhas.

### Fase 5 — nomes honestos

- `overlay` → `render` (se a Fase 1 não fez), `cli` → `daemonclient`.
- `cmd/cdpprobe` reaproveita o que já existe em vez de duplicar motor.

Critério: build verde e smoke test passando.

## O que NÃO entra aqui

- Mudança de comportamento, otimização de snapshot, recurso novo.
- Suíte de teste de integração com navegador de verdade (fica para depois; a
  Fase 0 cobre o que é puro).
