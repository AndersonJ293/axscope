# Pendências

O que ficou fora do trabalho de hoje, por que ficou, e o que já foi decidido **não**
fazer. Cada item traz a medição que o justifica — nada aqui é palpite.

Ordem: as três primeiras são de ferramenta; a última é do laboratório.

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

## 3. Iframe (OOPIF) não é alcançado

**O que falta:** o `snap` e as ações enxergam só o frame principal. Missão 13 do
laboratório (ler `FRAME-991` dentro de um iframe) não é possível hoje.

**O desenho:** cada frame vira uma sessão própria (`Target.setAutoAttach` nos
targets do tipo `iframe`), e a leitura compõe os pedaços. Não é difícil, mas é
uma mudança de modelo (hoje `Session` assume "uma aba = uma sessão CDP").

Já está anotado em `README.md` → *Limitações conhecidas*.

---

## 4. Upload de arquivo (missão 11 do laboratório)

**Suspeita:** vai falhar. O arraste que a ferramenta monta usa `DataTransfer`
criado na página, e **`DataTransfer.files` não dá para preencher por JS** — é
proteção do navegador.

**O caminho certo, quando formos enfrentar:**
- `<input type=file>` → `DOM.setFileInputFiles` (CDP) resolve direto, e é o caso
  mais comum em formulário;
- dropzone de verdade (arrastar e soltar arquivo) → `Input.dispatchDragEvent`
  com `dragData.files`, que é caminho próprio de arquivo, não o arraste
  sintético que a gente monta hoje.

Vale decidir na hora se isso entra ou se fica como limite declarado.

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

Feitas: **1, 2, 3, 4, 5, 6, 7, 8, 9, 10** (placar em 5/16 porque o botão
*Resetar estado* apaga as concluídas — é o desenho dele).

| Missão | Assunto | Observação |
|---|---|---|
| 11 | upload de `.txt` | ver item 4 acima |
| 12 | botão dentro de Shadow DOM | a leitura e o `text=` não atravessam shadow root |
| 13 | iframe `srcdoc` | ver item 3 acima |
| 14 | alvo desenhado em canvas | não existe elemento no DOM: só por `pos=` |
| 15 | job assíncrono + polling | depende de `wait`/leitura; deve passar |
| 16 | elemento mutante (clicar quando disser AGORA) | depende de `wait` + clique; deve passar |
