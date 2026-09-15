# Pendências

O que ficou fora do trabalho de hoje, por que ficou, e o que já foi decidido **não**
fazer. Cada item traz a medição que o justifica — nada aqui é palpite.

Ordem: as três primeiras são de ferramenta.

---

## 1. A leitura não carrega estado de rolagem

**O que falta:** saber, sem agir, se um container rola e onde ele está
(`32380/41672`). Hoje a leitura mostra as linhas e nunca a posição — então
"rolar até o item 777 de 1000" vira chute ou conta de guardanapo.

**Medido:** na missão 10 do laboratório eu precisei de `eval` duas vezes — uma
para a geometria (42.000 de conteúdo, 328 de janela) e outra para a posição.

**Por que não é barato:** testei a hipótese óbvia e **ela caiu**. O Chromium
**não** serializa `scrollY`/`scrollYMax` no `Accessibility.getFullAXTree` — os
nomes de propriedade que o CDP expõe não incluem estado de rolagem. A primeira
implementação que fiz (ler essas propriedades e imprimir `[rolagem N/M]`) não
imprimiu nada; virou código morto e foi removida.

**O que daria:** uma passada paralela no DOM. O desenho possível é marcar os
scrollers numa avaliação só (`document.querySelectorAll('*')` → quem tem
`overflow` e `scrollHeight > clientHeight` ganha um atributo), depois
`DOM.querySelectorAll` no atributo e `DOM.describeNode` em cada um para casar
`backendNodeId` com os nós da árvore. Custa ~N+2 chamadas por leitura (N =
quantidade de caixas roláveis), o que é aceitável, mas é peso no caminho quente
do `snap` e mexe no artefato central — por isso não foi feito de improviso.

**O que já temos no lugar:** a resposta do `scroll` diz **quem** rolou e **onde
parou** (`ok: scroll 300 em css=#virtual — agora em #virtual 32680/41672`), e
caixa sem nome é alcançável por `pos=`/`css=`. Isso cobre "onde estou" no
momento em que importa (depois de rolar), sem custo por leitura.

---

## 2. Linha de tabela custa 4 linhas de leitura

**Medido** (missão 7, tabela de 60 linhas): **240 das 489 linhas** da leitura —
49% — e 12,8 KB. Cada linha da tabela vira uma linha por célula.

**A proposta:** linha cujas células sejam **só texto** vira uma linha só —
`- row: 30, Pessoa 30, Barreiras, 100`. Linha com link/botão dentro continua
expandida, porque aí há alvo a preservar. Seria 240 → 60 linhas, **−37% da
leitura inteira, sem perder valor nenhum**.

**Por que não foi feito:** muda o formato do artefato central e eu só tinha
**um** caso medido. A lista de infinite scroll, que eu supunha igual, custa
**~1,15 linha por item** — ou seja, o peso é específico de tabela, não de
repetição. Com dois casos na mão dá para decidir com dado; a suíte de testes do
snapshot (criada hoje) é a rede para mexer nisso.

---

## 3. Iframe: a leitura não entra, a ação entra

**Estado hoje:** a missão 13 do laboratório **passa**, por coordenada — mas o
caminho tem fricção e vale registrar por quê.

**O que funciona:** `click pos=x,y` com o ponto do botão de dentro. Evento de
mouse é do navegador, não da página: ele é entregue por hit-test no viewport e
atravessa a fronteira do iframe sem que ninguém precise saber que ela existe.

**O que falta:** a leitura. O `snap` mostra o iframe como uma linha só (`-
Iframe`, sem ref e sem conteúdo), porque a árvore de acessibilidade do frame
principal não inclui o documento de dentro. Então, para saber **onde** fica o
botão de dentro, hoje é preciso um `eval` que leia `contentDocument` e some o
deslocamento do iframe — foi o que eu fiz para fechar a missão. Não é gambiarra,
mas é conta que a ferramenta deveria poupar.

**O desenho, em dois degraus:**

- **Mesma origem** (o caso do `srcdoc` do laboratório): `iframe.contentDocument`
  é alcançável, então a mira por `text=`/`css=` pode descer nele como já desce em
  shadow root — e aí a geometria precisa do deslocamento do frame, porque
  `getBoundingClientRect` de dentro responde no sistema de coordenadas do iframe.
  É o mesmo cuidado que o shadow root não exigiu (lá não há viewport própria).
- **Origem diferente (OOPIF)**: aí não há `contentDocument`; cada frame vira
  sessão CDP própria (`Target.setAutoAttach`) e a leitura compõe os pedaços.
  É mudança de modelo — hoje `Session` assume "uma aba = uma sessão".

**O que já foi corrigido nesta rodada:** `pos=x,y` agia no centro do elemento
sob o ponto, e não no ponto. Sobre um iframe isso é o centro do iframe — a
dezenas de pixels do lugar pedido — e o clique acertava o vazio. Agora o ponto
pedido é onde a ação acontece, e o `Rect` do elemento continua servindo ao
destaque e ao "entrar de fora" do hover.

---

## 4. Upload de arquivo (missão 11 do laboratório) — resolvido

Ficou aqui a correção, porque a previsão que eu tinha escrito estava **errada**:
"`DataTransfer.files` não dá para preencher por JS". Dá. A atribuição direta é
que é só-leitura; `items.add(new File(...))` **popula** `files`, e é assim que
um dropzone de verdade recebe um arquivo forjado na página.

Entrou como `bu upload <arquivo> [alvo=]`, com dois caminhos, porque a web
recebe arquivo de duas formas:

- **`<input type=file>`** (formulário, quase sempre escondido atrás de um botão)
  → `DOM.setFileInputFiles` do CDP, que dispara `input`/`change` como se o
  arquivo tivesse sido escolhido. É o caminho padrão, quando não se passa alvo.
- **dropzone** → o conteúdo vira um `File` dentro da página, num `DataTransfer`
  de verdade, e `dragenter`/`dragover`/`drop` são emitidos sobre o alvo. O
  `Input.dispatchDragEvent` que eu tinha planejado não foi preciso.

Ambos medidos no laboratório: `[input]` e `[dropzone]`, cada um marcando o
`check(11)`. No caminho da dropzone o input escondido fica com `files.length = 0`
— é a prova de que o arquivo veio por arraste e não por baixo.

---

## 5. Shadow DOM (missão 12 do laboratório) — resolvido

Duas suposições minhas caíram de uma vez, e a correção ficou registrada aqui
porque o desenho da ferramenta se apoia no que se aprendeu:

- **"a leitura não atravessa shadow root"** — atravessa. A árvore de
  acessibilidade **achata** shadow DOM: o botão de dentro aparecia na leitura,
  com nome e com `ref`.
- **"o ref não alcança o que está dentro"** — alcança. Ele resolve por
  `backendNodeId` no CDP, que atravessa a fronteira do shadow root sem precisar
  saber que ela existe.

O que **não** atravessava era a mira por DOM: o `text=` montava a lista de
candidatos com `document.querySelectorAll` e o `css=` usava
`document.querySelector` — os dois no documento claro. A leitura mostrava e a
mira não alcançava: exatamente a assimetria que o README descreve como armadilha.

Corrigido: o `text=` coleta candidatos também dentro de shadow roots **abertos**
(recursão em `el.shadowRoot`), e o `css=` mantém o documento claro primeiro — é
a semântica do seletor — caindo na sombra só quando o claro não acha nada. A
ordem dos candidatos do documento claro ficou idêntica, então página sem web
component não muda de alvo.

---

## Decidido **não** fazer (com o porquê)

- **Aceitar ref de leitura antiga quando ela aponta para o mesmo nó.** O guarda
  estrito custa um `snap` a mais e evita clique no alvo errado — e o erro é
  alto, com instrução do que fazer. Não troco segurança por 0,2s.
- **Rolar "até o texto aparecer"** como comando. É composto (rolar → ler →
  repetir) e o agente monta com o que existe.
- **Destaque do alvo (o contorno roxo).** Removido por preferência: ficava aceso
  depois da ação e, com a página rolando, apontava para o nada. Volta com
  `BROWSER_USE_DESTAQUE=1`.

---

## Laboratório: missões pendentes

Feitas: **1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13** (placar em 6/16 porque
o botão *Resetar estado* apaga as concluídas — é o desenho dele).

| Missão | Assunto | Observação |
|---|---|---|
| 14 | alvo desenhado em canvas | não existe elemento no DOM: só por `pos=` |
| 15 | job assíncrono + polling | depende de `wait`/leitura; deve passar |
| 16 | elemento mutante (clicar quando disser AGORA) | depende de `wait` + clique; deve passar |
