# Pendências

O que ficou fora do trabalho, por que ficou, e o que já foi decidido **não**
fazer. Cada item traz a medição que o justifica — nada aqui é palpite.

**Aberto hoje:**

- **Iframe de outra origem (OOPIF)** não é lido: aquela árvore de acessibilidade
  vive no processo do outro site e exige sessão CDP própria por frame (item 3).
- **Rolagem horizontal** não entra no cabeçalho da leitura: o eixo vertical é o
  que morde, e reportar os dois inventaria formato para um caso ainda não
  medido (item 1).
- **Conteúdo que carrega por `IntersectionObserver` não avança em aba oculta.**
  Não é da ferramenta — é do navegador —, e a saída é `tab <n> --focus`; o
  `scroll` avisa quando chega ao fim nessa condição. Está no README.

O resto abaixo é histórico: o que fechou, com o que ensinou.

---

## 1. A leitura não carregava estado de rolagem — resolvido

**O que faltava:** saber, sem agir, se uma área rola e onde ela está
(`32380/41672`). A leitura mostrava as linhas e nunca a posição — então "rolar
até o item 777 de 1000" era chute ou conta de guardanapo. Na missão 10 eu
precisei de `eval` duas vezes: uma para a geometria, outra para a posição.

**A hipótese que caiu:** o Chromium **não** serializa `scrollY`/`scrollYMax` no
`Accessibility.getFullAXTree`. A implementação que lia essas propriedades não
imprimiu nada e foi removida em vez de ficar como código morto.

**Feito:** o estado vem do DOM, numa avaliação só — a mesma que já buscava
título e URL, então não custou ida e volta a mais. O cabeçalho da leitura diz:

    -- 261 linhas, 57 refs · rolagem: página 2075/2844 · #virtual 5000/41672 ·
       div.table-wrap 0/1899 · #lazyBox 0/262 · #infinite 0/182 (+1)

Cada área sai com um seletor curto (`#id`, `tag.classe`), para o agente poder
mirá-la por `css=` sem tradução. As **maiores vêm primeiro**: a área que rola
mais é a que costuma importar, e o cabeçalho é curto por definição (cinco áreas,
depois `(+N)`). O teste barato (`scrollHeight > clientHeight`) vem antes do
`getComputedStyle`, que é caro.

**O que ficou de fora:** o eixo horizontal. Existe (barra de código, painel
largo) e não foi reportado porque inventaria um formato para um caso que ainda
não mordeu — quando morder, o lugar é `rolagem.go`.

**Medido:** `scroll 5000 alvo=css=#virtual` e o `snap` seguinte já diz
`#virtual 5000/41672` com o item visível ao lado (`Virtual item 117`), sem
`eval` nenhum.

---

## 2. Linha de tabela custava 4 linhas de leitura — resolvido

**Medido antes** (tabela de 60 linhas do laboratório): **305 das 508 linhas** da
leitura, 60%. Cada linha de dados custava cinco: a linha e as quatro células.

**Feito:** linha cujas células sejam **só texto** vira uma linha —
`- row: 1 · Pessoa 01 · Salvador · 37`. A regra de segurança é a que separa
"cabe" de "some": a linha só achata se **nenhum** descendente puder receber ref,
não tiver propriedade que a leitura mostra (`[checked]`, `[level=2]`…) e não
carregar estrutura própria — imagem, lista ou tabela aninhada continuam valendo
linha. Papel desconhecido também não achata: o pior erro aqui é esconder alguma
coisa, e o comportamento antigo é o padrão.

**Medido depois:** a leitura inteira foi de **508 → 264 linhas** (−48%), e as
linhas de tabela (linha + células), de **305 → 61**. O `--refs` não mostra linha
achatada (ela não tem alvo), e
mirar no cabeçalho por `text=Score ↕` continua funcionando — a ação anda no DOM,
não na leitura.

**O separador:** ` · `. Se o texto de uma célula já contiver o separador, a linha
não achata — senão a leitura inventaria uma coluna.

---

## 3. Iframe na leitura: mesma origem resolvido, OOPIF não

**Feito (mesma origem):** a leitura agora entra no iframe. A árvore de
acessibilidade vem **por frame** — a do frame principal mostra o iframe como uma
linha só —, e o CDP entrega a do documento de dentro separada
(`Accessibility.getFullAXTree` com `frameId`) e diz qual elemento hospeda cada
frame (`DOM.getFrameOwner`). As duas viram uma, penduradas no nó do iframe:

    - Iframe
      - heading "Iframe zone" [level=3]
      - button "Clique dentro do iframe" [ref=e55]

O ref de dentro **funciona**: resolve por `backendNodeId` e a geometria do
`DOM.getBoxModel` já vem no sistema de coordenadas da página — medido, o clique
pelo ref fecha o `check(13)` do laboratório. Então a missão deixou de depender de
`pos=x,y` calculado à mão.

Três detalhes que a implementação exigiu:

- **Ids prefixados por frame.** Cada árvore numera os nós a partir do próprio
  root; sem prefixo, os ids de dois frames colidem no mesmo mapa e a leitura sai
  misturada.
- **A raiz do frame não vira linha.** `RootWebArea "título do documento"` dentro
  do iframe é ruído; o que interessa é o conteúdo, que se pendura direto no nó do
  iframe.
- **O iframe não coleta texto.** Ele não tem texto próprio, e a coleta pescaria o
  texto do documento de dentro — que já aparece logo abaixo. Saía
  `- Iframe: FRAME-991` duplicando o conteúdo.

O custo fica com quem tem iframe: sem um nó de papel `Iframe` na árvore, nada é
buscado.

**O que fica (OOPIF):** origem diferente continua mostrando `- Iframe` sem
conteúdo. Medido com uma página de teste (`file://` com iframe para
`https://example.com`): a árvore não vem, porque ela vive no processo do outro
site. Alcançar exige sessão CDP própria por frame (`Target.setAutoAttach`, com as
sessões que o cliente já sabe usar) — mudança de modelo, não de detalhe.

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

## 6. `wait` não enxergava shadow root nem iframe — resolvido

**Medido** quando a leitura passou a mostrar os dois: `wait "Iframe zone"` e
`wait "SHADOW-321"` estouravam o tempo, embora o `snap` mostrasse o conteúdo. A
varredura do `wait` era `document.body.innerText` mais
`document.querySelectorAll('body *')`, e nenhuma das duas atravessa fronteira.

**Feito:** a varredura desce em `el.shadowRoot` e em `iframe.contentDocument`
(mesma origem) — o mesmo padrão da mira por texto e da leitura. A resposta
inclusive diz quando achou dentro de um iframe:

    wait "Iframe zone"  →  ok: apareceu em 12ms — em h3 (dentro de iframe)

---

## 7. O clique que não chegava: três defeitos empilhados — resolvido

Achado ao **refazer o desafio v2** depois de corrigir o bug do lab. O agente que
testou tinha concluído que precisava de `eval` para a lista virtualizada; a
investigação mostrou três defeitos meus no caminho do clique:

1. **Ancestral no DOM claro passava como caminho livre.** A conferência aceitava
   "o elemento do ponto contém o alvo" — mas evento borbulha para cima, não
   desce: um container na frente do filho nunca entrega o clique a ele. Medido
   com o botão em (1133,272), o clique enviado exatamente ali, e quem recebia era
   o `div.card.padded` que **contém** o botão.
2. **A rolagem achava visível o que estava recortado.** A lista virtualizada
   recorta por `overflow`, e uma linha 85px acima da janela do container contava
   como visível. Agora "visível" é a mesma pergunta do clique — *o que está no
   ponto?* —, e o centro é o do próprio scroller, não o da janela.
3. **A resposta não conferia se o evento passou pelo alvo.** O alvo recebe uma
   escuta de captura antes do clique; se o evento não passar por ele, a resposta
   avisa. `elementFromPoint` enxerga camadas mas não sabe para onde o navegador
   reentrega o evento (shadow host, iframe); a escuta sabe.

As duas perguntas — a do clique e a da rolagem — ficaram numa definição só:
elas discordarem foi o que fez a rolagem dizer "já está visível" e o clique
recusar logo depois.

**Medido no desafio:** `snap` → ref do "Abrir" do Candidato 413 → clique →
`CHECK 17`, sem `eval` e sem conta de seletor. E os oito checks que refiz
(2, 8, 11, 12, 17, 18, 19, 20) saíram todos com comando de primeira classe.

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
- **Adivinhar alvo dentro de canvas.** Não há o que procurar: o desenho não é
  DOM — não está na árvore de acessibilidade nem para o `text=`, e não existe
  elemento sob o ponto (o `elementFromPoint` devolve o próprio canvas). O
  caminho é `pos=x,y`, com o ponto vindo de quem sabe onde desenhou. Medido na
  missão 14: o círculo está em (470,95) nas coordenadas do canvas, e o ponto só
  chega à página depois de mapeado pela escala do elemento.

---

## Laboratório: missões

Todas as **16** feitas. O placar do laboratório não reflete isso porque o botão
*Resetar estado* apaga as concluídas a cada uso — é o desenho dele.

As que pediram mudança na ferramenta estão nos itens acima, com o que ensinaram:
upload (11 → item 4), Shadow DOM (12 → item 5), iframe (13 → item 3) e clique em
alvo que recusa ação (15 → README, na seção *Uso*). Canvas (14) e
elemento mutante (16) passaram com o que já existia — nos dois o trabalho foi
escolher o ponto e o momento, não mexer na ferramenta.
