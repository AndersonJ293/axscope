# Pendências

O que ficou fora do trabalho de hoje, por que ficou, e o que já foi decidido **não**
fazer. Cada item traz a medição que o justifica — nada aqui é palpite.

Ordem: as três primeiras são de ferramenta.

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
