# browser-use

Ferramenta de **browser dirigido por agente**: lê a tela como texto, age por
identidade e mostra um cursor renderizado — para o agente e para quem olha.

Um binário só, três papéis:

```bash
bu <comando>   # cliente: fala com o daemon (subindo-o se preciso)
bu serve       # o daemon (browser vivo, socket unix)
bu mcp         # servidor MCP sobre stdio, apontando para o mesmo daemon
bu install     # baixa o Chrome for Testing
```

## Princípios

Os mesmos da ferramenta de QA do app, transportados para o navegador:

1. **Ler a tela como texto** (`snap`) — árvore de acessibilidade com `ref`
   estável, nunca HTML cru nem pixel.
2. **Agir por identidade** (`click e12`, `css=...`, `text=...`) — coordenada
   nunca é a primeira opção.
3. **Convergir, não dormir** (`wait`/`waitgone`, `Settle`) — falha alto se a
   página não estabiliza em vez de mascarar com `sleep`.
4. **Lote** (`script`) — N passos numa conexão só, sem cold start por passo.
5. **Ver** — cursor, halo, destaque do alvo e HUD de abas injetados na página.

## Por que CDP cru (e não Playwright)

Playwright é um framework de _teste_: quer ser dono do ciclo do browser, sobe um
Chromium próprio com perfil limpo (perde login) e esconde o protocolo — o
snapshot com `ref` que ele usa no MCP é API interna (`_snapshotForAI`).

O CDP entrega direto o que importa:

| Necessidade | Onde vem |
|---|---|
| Abas nativas | `Target.*` |
| A "tela" em texto | `Accessibility.getFullAXTree` |
| Cursor em coordenada exata | `Input.dispatchMouseEvent` |
| Overlay que sobrevive à navegação | `Page.addScriptToEvaluateOnNewDocument` |

E Node não é dependência: o cliente CDP é Go + `WebSocket`.

## Instalação

```bash
make install        # → ~/.local/bin/browser-use (atalho: bu)
browser-use install # baixa o Chrome for Testing, perfil dedicado
```

Se `~/.local/bin` não estiver no seu `PATH`, use `PREFIX=/usr/local/bin make install`.

O Chrome baixado vive em `~/.local/share/browser-use/browsers/`. O perfil fica
em `~/.local/share/browser-use/profiles/<sessão>/` — é persistente, então login
sobrevive entre rodadas.

### MCP no opencode

```json
{
  "mcp": {
    "browser-use": {
      "type": "local",
      "command": ["/home/USUARIO/.local/bin/browser-use", "mcp"]
    }
  }
}
```

O MCP expõe um conjunto **enxuto** de 16 ferramentas (schema de ferramenta custa
contexto em toda requisição). Para abrir todas: `BROWSER_USE_MCP_TOOLS=all`.

## Uso

```bash
bu open https://example.com        # navega (ou usa a aba ativa)
bu snap                            # lê a tela (o "tela" do app)
bu click e1                        # age pela ref do último snap
bu fill e5 "dono@exemplo.com"
bu press Enter
bu wait "Painel"                   # converge, não dorme
bu tabs                            # abas abertas (a ativa vem com *)
bu shot /tmp/evidencia.png         # captura (com cursor e destaque)
bu script cenario.txt              # roteiro em lote
```

Aliases em português existem (`tela`, `clicar`, `digitar`, `esperar`, `abas`…).

Alvo aceita três formas: `e12` (ref), `css=.botao`, `text=Entrar`.

### Roteiro

Uma linha por passo, `#` comenta, aspas para espaços:

```
# cenário: entrar no painel
open https://exemplo.com/login
snap
fill e1 "dono@talher.com"
fill e2 "senha"
click e3
wait "Painel"
snap
shot /tmp/painel.png
```

## Modos

| Modo | Como | Quando |
|---|---|---|
| Visual (padrão) | `bu open ...` | você quer ver abas + cursor |
| Cego | `BROWSER_USE_HEADLESS=1` | rodada sem janela |
| Anexar | `BROWSER_USE_ATTACH=host:porta` | usar um Chromium já aberto |

Para anexar, o Chromium precisa ter sido iniciado com
`--remote-debugging-port=PORTA`. Assim o mesmo driver serve no seu Brave/Chrome
do dia a dia.

## Motor: o que a pesquisa provou

A pergunta "existe browser mais leve?" tem resposta medida, não de opinião.

| Engine | Renderiza? | CDP | Veredito |
|---|---|---|---|
| **Chrome for Testing** | sim | completo | padrão |
| Thorium / Helium / ungoogled / Cromite | sim | completo | mesmo Chromium com patch; não é menor |
| Servo / WebKitGTK / QtWebEngine | sim | não (WebDriver) | perde o CDP |
| **Lightpanda** | **não** (sem engine de renderização) | parcial | ver abaixo |

### Lightpanda, medido

Sonda em `cmd/cdpprobe` contra `lightpanda serve`:

```
Browser.getVersion                 OK
Target.getTargets                  OK    0 targets   ← não usa o modelo de targets
Target.createTarget                OK    FID-0000000001
Accessibility.getFullAXTree        OK    (árvore real, com role/name)
Runtime.evaluate                   OK    document.title = "Example Domain"
DOM.getDocument                    OK
Page.captureScreenshot             OK    (PNG de renderização textual)
Input.dispatchMouseEvent           OK
```

Conclusões:

- **Não serve para o objetivo visual**: sem engine de renderização não há
  janela, abas visíveis nem cursor pintado. As duas exigências caem.
- **É mais completo do que parece**: implementa a Accessibility domain e o
  Protocol completo o bastante para um modo `headless` de extração.
- **O bootstrap difere**: como `Target.getTargets` volta vazio, é preciso
  `Target.createTarget` em vez de descobrir e anexar. Nosso `bootstrap` assume
  o modelo do Chromium.
- **Memória é o ganho real**: ~16x menos RAM e ~9x mais rápido para crawl em
  lote — mas isso é outro caso de uso, não "browser use com cursor".

Uso recomendado do Lightpanda: o MCP nativo dele, para crawl/extração em massa.
Não como motor visual.

## Overlay do cursor

Injetado em toda navegação via `Page.addScriptToEvaluateOnNewDocument`. Cuidados
que custaram bugs reais:

- **sem `innerHTML`** — páginas com Trusted Types recusariam a atribuição;
- **CSS por `adoptedStyleSheets`** — imune a `style-src`;
- **posicionamento por CSSOM** (`el.style.*`), não por atributo `style`;
- **`aria-hidden`** no host — senão o HUD aparece no próprio `snap`;
- **`top: auto`** no HUD — a regra base fixa `top:0` e a caixa esticava.

Ajuste de visibilidade: `BROWSER_USE_CURSOR_DELAY` (ms, padrão 160) controla
quanto o cursor "chega antes" de agir; `0` remove a pausa.

## Variáveis

| Variável | Efeito |
|---|---|
| `BROWSER_USE_SESSION` | nome da sessão (default `default`) |
| `BROWSER_USE_HOME` | diretório de dados |
| `BROWSER_USE_CHROME` | executável do Chromium |
| `BROWSER_USE_ATTACH` | `host:porta` de um Chromium já aberto |
| `BROWSER_USE_HEADLESS` | sobe sem janela |
| `BROWSER_USE_CURSOR_DELAY` | pausa do cursor antes de agir (ms) |

## Estrutura

```
cmd/bu/            entrypoint (cliente, serve, mcp, install)
cmd/cdpprobe/      sonda de diagnóstico de engines CDP
internal/cdp/      cliente CDP sobre WebSocket
internal/browser/  launcher, sessão/abas, snapshot, ações, observação
internal/overlay/  overlay.inject.js + ponte
internal/agent/    despachante dos comandos
internal/command/  parser compartilhado (CLI, roteiro, MCP)
internal/daemon/   servidor do socket unix
internal/mcpsrv/   servidor MCP (stdio, JSON-RPC à mão)
internal/installer/ download do Chrome for Testing
```

## Limitações conhecidas

- Iframes cross-origin (OOPIF) ainda não viram sessões próprias: o `snap`/ação
  enxerga só o frame principal.
- Diálogos nativos são sempre descartados (`dismiss`), configurável depois.
- O `bootstrap` assume o modelo de targets do Chromium; engines alternativos
  precisam de um caminho próprio.
