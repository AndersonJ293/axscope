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
make install                    # → ~/.local/bin/browser-use (atalho: bu)
browser-use install --engine all  # Chrome for Testing + chrome-headless-shell
browser-use engines             # o que está disponível
```

Se `~/.local/bin` não estiver no seu `PATH`, use `PREFIX=/usr/local/bin make install`.

Os binários ficam em `~/.local/share/browser-use/browsers/`. O perfil fica em
`~/.local/share/browser-use/profiles/<sessão>/` — é persistente, então login
sobrevive entre rodadas.

`--engine` aceita `chrome` (padrão), `shell` (chrome-headless-shell) ou `all`.

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

## Disco

| Onde | Tamanho | O quê |
|---|---|---|
| `~/.local/share/browser-use/browsers/` | ~650 MB | Chrome + headless-shell baixados |
| `~/.local/share/browser-use/profiles/` | varia | perfil (logins, estado) |
| `~/.local/share/browser-use/logs/` | ≤ 2 MB por sessão | log do daemon, truncado ao subir |

`bu shot <arquivo>` grava **exatamente onde você manda** — não existe pasta
padrão nem acúmulo automático. A ferramenta só escreve sozinha o que é
necessário: perfil, browsers baixados (no `install`) e o log (limitado).

```bash
browser-use clean          # logs e sessões mortas
browser-use clean --tudo   # inclui perfis e browsers baixados
```

## Ciclo de vida

O daemon mantém o browser vivo de propósito: a próxima chamada responde na hora
e o estado (login, abas) sobrevive entre comandos. Ele **não** fica pendurado
para sempre: se ninguém o usa por 30 minutos, ele se encerra e fecha o browser
sozinho (`BROWSER_USE_IDLE_MINUTES` ajusta; `0` desliga).

Para encerrar na hora, quando quiser:

```bash
browser-use stop          # só a sessão atual
browser-use stop --all    # todas as sessões e todos os browsers
```

## Modo extensão: seu próprio navegador

Em vez de subir um navegador dedicado, a extensão dirige o **seu Brave** — com os
logins que você já tem. É o modo mais útil no dia a dia.

```bash
browser-use --ext open https://exemplo.com
browser-use --ext snap
browser-use --ext click e3
```

### Por que precisa de extensão

Desde o Chrome/Chromium **136**, `--remote-debugging-port` é **ignorado** quando
se usa o perfil padrão (medida de segurança para não expor senhas e cookies).
Ou seja: não existe caminho por porta de debug no seu perfil real. A extensão usa
`chrome.debugger`, que funciona no navegador já aberto, sem reiniciar nada.

### Montagem (uma vez)

1. **Carregue a extensão no Brave**
   `brave://extensions` → ligue **Modo do desenvolvedor** → **Carregar sem
   compactação** → aponte para `extension/` neste repositório.

2. **Esconda a faixa de depuração** (opcional, mas recomendado)
   A API `chrome.debugger` faz o Chromium mostrar uma faixa *"browser-use started
   debugging this browser"* em todas as abas. Para não ver isso:

   ```bash
   scripts/brave-sem-faixa.sh instalar   # cria um override do .desktop, sem sudo
   # feche o Brave por completo e abra de novo
   scripts/brave-sem-faixa.sh remover    # para reverter
   ```

3. Confira a conexão no ícone da extensão (deve dizer **conectado**).

### Como funciona

```
extensão (Brave)  ⇄  uma conexão por sessão  ⇄  daemon (Go)  ⇄  CLI / MCP
  chrome.debugger → CDP real na aba
  chrome.tabs     → domínio Target (abas)
  chrome.tabGroups→ um grupo por sessão (o isolamento)
```

Cada sessão ocupa **uma porta da faixa 8787–8802** e recebe o seu **próprio grupo
de abas** no Brave, com nome `browser-use · <sessão>`. A extensão só enxerga e só
toca nas abas do grupo daquela sessão.

Isso resolve três coisas de uma vez:

- **vários agentes ao mesmo tempo**, cada um com o seu grupo, sem disputar porta;
- **cada agente com quantas abas quiser** dentro do próprio grupo;
- **suas abas pessoais intocadas** — e como é o mesmo perfil, as abas do agente
  já nascem logadas nos seus sites.

A extensão sintetiza **apenas** o domínio `Target` (abas ↔ `chrome.tabs`) e
repassa todo o resto — `Accessibility`, `DOM`, `Input`, `Runtime`, `Page` — para
o `chrome.debugger`. Por isso o driver inteiro funciona sem mudança: a árvore de
acessibilidade é a **real**, o clique é por coordenada e o cursor é renderizado
igual aos outros modos.

Cada aba é anexada **sob demanda** — só a que está sendo usada. Abrir o agente
não varre nem instrumenta as suas abas.

### Nome do grupo

O grupo aparece como **`<Agente> <N>`** — `Opencode 1`, `Opencode 2`, `Claude 1`.
O número é atribuído pela extensão (o próximo livre daquele agente).

O nome do agente vem da configuração do MCP que está dirigindo:

```json
{
  "mcp": {
    "browser-use": {
      "type": "local",
      "command": ["/home/USUARIO/.local/bin/browser-use", "mcp"],
      "environment": { "BROWSER_USE_AGENT": "Opencode" }
    }
  }
}
```

Sem isso, o MCP tenta o nome do cliente (`clientInfo.name`) e a CLI usa
`browser-use`.

### Dar e tirar acesso: arraste a aba

O grupo **é** a interface de permissão. Não há menu nem configuração:

- **arraste uma aba sua para dentro do grupo** de um agente → ele passa a
  enxergá-la e a poder dirigi-la (útil para trabalhar numa aba onde você já
  está logado, com o estado que você já montou);
- **arraste para fora** → o acesso é revogado na hora, e o depurador é solto
  daquela aba junto.

As abas que o agente abre sozinho já nascem dentro do grupo dele.



Mesmo motor, mesmo CDP, mesmo conjunto de ações. A diferença é a janela.

| Modo | Motor | RAM (1 aba) | Processos | Vê a tela? |
|---|---|---|---|---|
| Modo | Motor | RAM | Vê a tela? |
|---|---|---|---|
| **(padrão) extensão** | o seu Brave, já logado | — (já está aberto) | ✅ abas + cursor |
| **`--ver`** | Chrome for Testing | ~1850 MB | ✅ abas + cursor |
| **`--leve`** | chrome-headless-shell | ~505 MB | ❌ |
| anexar | um Chromium seu com porta de debug | — | depende |

```bash
# padrão: o seu Brave, pela extensão (é o modo do dia a dia)
browser-use open https://exemplo.com

# Chrome dedicado, quando quiser um navegador separado
browser-use --ver open https://exemplo.com

# sem janela nenhuma, para lote
browser-use --leve script roteiro.txt

# encerra tudo (todos os modos e seus browsers)
browser-use stop --all
```

Cada modo é uma **sessão separada** (`default` = extensão, `ver`, `leve`), então
coexistem: dá para deixar um roteiro rodando sem janela enquanto você olha outra
coisa no Brave.

O motor é propriedade da **sessão**: o daemon sobe o browser com o motor escolhido
na primeira chamada. Trocar de motor numa sessão já viva exige `stop` (ou use
outra sessão). `browser-use engines` mostra o que está instalado.

O modo leve **não é** um Chromium capado: é o mesmo motor com o mesmo CDP, a
mesma árvore de acessibilidade, a mesma geometria real e o mesmo clique por
coordenada. Só não há janela — então não há cursor desenhado.

Anexar continua valendo para qualquer Chromium já aberto com
`--remote-debugging-port=PORTA` (`BROWSER_USE_ATTACH=host:porta`), e aí você usa
o seu navegador do dia a dia, com seus logins.

## Motor: o que a pesquisa provou

A pergunta "existe browser mais leve?" tem resposta medida, não de opinião.

| Engine | Renderiza? | CDP | Veredito |
|---|---|---|---|
| **Chrome for Testing** | sim | completo | padrão do modo **ver** |
| **chrome-headless-shell** | sim (sem janela) | completo | modo **leve**: mesmo motor, ~3,7x menos RAM |
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
DOM.resolveNode                    OK    => refs funcionam
Accessibility (2 conexões)         OK    duas páginas independentes
geometria: <a> getBoundingClientRect  {width:5, height:5}  ← não é layout real
```

Conclusões (corrigidas por medição, não por suposição):

- **Abas: tem.** Cada conexão é uma sessão independente, com página, cookies e
  memória próprios — é o `session_new` do MCP dele e o "MultiClient" do blog.
  Não são abas numa barra visível, mas são páginas independentes gerenciáveis.
- **Cursor: não tem, e não pode ter.** A doc do screenshot é explícita: *"the
  text layout Lightpanda computes, not a pixel-accurate browser rendering (no
  images, fonts or CSS colours)"*. Sem pixel fiel, mouse desenhado é decoração.
- **Ação por coordenada: não dá.** O `<a>` mediu 5×5 — não há layout real. Por
  isso todos os tools de clique dele são por `selector`/`backendNodeId`. O nosso
  clique (centro + `Input.dispatchMouseEvent`) não se aplica; seria trocar por
  disparo de evento no nó.
- **AX + refs: funciona.** 15 nós, 11 com `backendNodeId`, e `DOM.resolveNode` OK.
- **Memória é o ganho real**: 36 MB contra ~505 MB do headless-shell. Ainda
  assim, o headless-shell entrega tudo (geometria, clique por coordenada,
  screenshot fiel) — exceto a janela.

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
| `BROWSER_USE_ENGINE` | `shell` (leve, padrão) ou `chrome` (ver) |
| `BROWSER_USE_IDLE_MINUTES` | encerra o daemon após N min ocioso (padrão 30; 0 desliga) |
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
