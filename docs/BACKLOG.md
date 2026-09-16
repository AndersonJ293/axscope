# Backlog

What was left out of the work, why it was left out, and what has already been
decided **not** to do. Each item carries the measurement that justifies it —
nothing here is a guess.

**Open today:**

- **Iframe from another origin (OOPIF)** is not read: that accessibility tree
  lives in the other site's process and requires its own CDP session per frame
  (item 3).
- **Horizontal scroll** does not enter the snapshot header: the vertical axis is
  the one that bites, and reporting both would invent a format for a case not
  yet measured (item 1).
- **Content that loads via `IntersectionObserver` does not advance in a hidden
  tab.** It is not the tool's fault — it is the browser's — and the way out is
  `tab <n> --focus`; `scroll` warns when it reaches the end under that condition.
  It is in the README.

The rest below is history: what closed, with what it taught.

---

## 1. The snapshot did not carry scroll state — resolved

**What was missing:** knowing, without acting, whether an area scrolls and where
it is (`32380/41672`). The snapshot showed the lines and never the position — so
"scroll to item 777 of 1000" was a guess or napkin math. In scenario 10 I needed
`eval` twice: once for the geometry, another for the position.

**The hypothesis that fell:** Chromium **does not** serialize `scrollY`/`scrollYMax`
in `Accessibility.getFullAXTree`. The implementation that read those properties
printed nothing and was removed instead of staying as dead code.

**Done:** the state comes from the DOM, in a single evaluation — the same one
that already fetched title and URL, so it cost no extra round trip. The snapshot
header says:

    -- 261 lines, 57 refs · scroll: page 2075/2844 · #virtual 5000/41672 ·
       div.table-wrap 0/1899 · #lazyBox 0/262 · #infinite 0/182 (+1)

Each area comes out with a short selector (`#id`, `tag.class`), so the agent can
target it via `css=` without translation. The **largest come first**: the area
that scrolls the most is usually the one that matters, and the header is short by
definition (five areas, then `(+N)`). The cheap test (`scrollHeight > clientHeight`)
comes before `getComputedStyle`, which is expensive.

**What was left out:** the horizontal axis. It exists (code bar, wide panel) and
was not reported because it would invent a format for a case that has not bitten
yet — when it bites, the place is `scroll.go`.

**Measured:** `scroll 5000 target=css=#virtual` and the following `snap` already
says `#virtual 5000/41672` with the item visible next to it (`Virtual item 117`),
without any `eval`.

---

## 2. A table row cost 4 snapshot lines — resolved

**Measured before** (60-row table of the harness page): **305 of the 508 lines** of
the snapshot, 60%. Each data row cost five: the row and its four cells.

**Done:** a row whose cells are **text only** becomes one line —
`- row: 1 · Person 01 · Salvador · 37`. The safety rule is what separates "fits"
from "disappears": the row only flattens if **no** descendant can receive a ref,
has no property the snapshot shows (`[checked]`, `[level=2]`…) and carries no
structure of its own — an image, list or nested table still count as a row. An
unknown role also does not flatten: the worst error here is hiding something, and
the old behavior is the default.

**Measured after:** the whole snapshot went from **508 → 264 lines** (−48%), and
the table lines (row + cells), from **305 → 61**. `--refs` does not show a
flattened row (it has no target), and targeting the header via `text=Score ↕`
still works — the action walks the DOM, not the snapshot.

**The separator:** ` · `. If a cell's text already contains the separator, the
row does not flatten — otherwise the snapshot would invent a column.

---

## 3. Iframe in the snapshot: same origin resolved, OOPIF not

**Done (same origin):** the snapshot now enters the iframe. The accessibility
tree comes **per frame** — the main frame's shows the iframe as a single line —
and CDP delivers the inner document's separately (`Accessibility.getFullAXTree`
with `frameId`) and says which element hosts each frame (`DOM.getFrameOwner`).
The two become one, hung on the iframe node:

    - Iframe
      - heading "Iframe zone" [level=3]
      - button "Click inside the iframe" [ref=e55]

The ref from inside **works**: it resolves by `backendNodeId` and the geometry
from `DOM.getBoxModel` already comes in the page coordinate system — measured,
the click by ref closes check 13 of the harness. So the scenario stopped
depending on hand-calculated `pos=x,y`.

Three details the implementation demanded:

- **Ids prefixed by frame.** Each tree numbers nodes starting from its own root;
  without a prefix, ids from two frames collide in the same map and the snapshot
  comes out mixed.
- **The frame root does not become a line.** `RootWebArea "document title"`
  inside the iframe is noise; what matters is the content, which hangs directly
  on the iframe node.
- **The iframe does not collect text.** It has no text of its own, and the
  collection would fish the inner document's text — which already appears just
  below. It came out `- Iframe: FRAME-991` duplicating the content.

The cost stays with whoever has an iframe: without an `Iframe` role node in the
tree, nothing is fetched.

**What remains (OOPIF):** a different origin still shows `- Iframe` with no
content. Measured with the harness (`file://` with an iframe to
`https://example.com`): the tree does not come, because it lives in the other
site's process. Reaching it requires its own CDP session per frame
(`Target.setAutoAttach`, with the sessions the client already knows how to use) —
a model change, not a detail.

---

## 4. File upload (scenario 11 of the harness) — resolved

The correction stayed here, because the forecast I had written was **wrong**:
"`DataTransfer.files` cannot be filled by JS". It can. Direct assignment is what
is read-only; `items.add(new File(...))` **populates** `files`, and that is how a
real dropzone receives a file forged on the page.

It came in as `axscope upload <file> [target=]`, with two paths, because the web
receives a file in two ways:

- **`<input type=file>`** (form, almost always hidden behind a button) →
  CDP's `DOM.setFileInputFiles`, which fires `input`/`change` as if the file had
  been chosen. It is the default path, when no target is passed.
- **dropzone** → the content becomes a `File` inside the page, in a real
  `DataTransfer`, and `dragenter`/`dragover`/`drop` are emitted over the target.
  The `Input.dispatchDragEvent` I had planned was not needed.

Both measured on the harness: `[input]` and `[dropzone]`, each marking check 11.
On the dropzone path the hidden input ends up with `files.length = 0` — it is the
proof that the file came by drag and not underneath.

---

## 5. Shadow DOM (scenario 12 of the harness) — resolved

Two assumptions of mine fell at once, and the correction stayed recorded here
because the tool's design leans on what was learned:

- **"the snapshot does not cross shadow root"** — it does. The accessibility
  tree **flattens** shadow DOM: the inner button appeared in the snapshot, with
  name and with `ref`.
- **"the ref does not reach what is inside"** — it does. It resolves by
  `backendNodeId` in CDP, which crosses the shadow root boundary without needing
  to know it exists.

What did **not** cross was targeting by DOM: `text=` built the candidate list
with `document.querySelectorAll` and `css=` used `document.querySelector` — both
in the light document. The snapshot showed it and the target did not reach it:
exactly the asymmetry the README describes as a trap.

Fixed: `text=` now collects candidates also inside **open** shadow roots
(recursion into `el.shadowRoot`), and `css=` keeps the light document first —
that is the selector's semantics — falling back to the shadow only when the
light one finds nothing. The order of the light-document candidates stayed
identical, so a page without web components does not change target.

---

## 6. `wait` did not see shadow root or iframe — resolved

**Measured** when the snapshot started showing both: `wait "Iframe zone"` and
`wait "SHADOW-321"` timed out, although `snap` showed the content. `wait`'s scan
was `document.body.innerText` plus `document.querySelectorAll('body *')`, and
neither crosses a boundary.

**Done:** the scan descends into `el.shadowRoot` and `iframe.contentDocument`
(same origin) — the same pattern as text targeting and the snapshot. The response
even says when it found it inside an iframe:

    wait "Iframe zone"  →  ok: appeared in 12ms — in h3 (inside iframe)

---

## 7. The click that did not arrive: three stacked defects — resolved

Found while **redoing the second test round** after fixing a harness bug.
The agent that tested had concluded it needed `eval` for the virtualized list;
the investigation showed three of my defects in the click path:

1. **An ancestor in the light DOM passed as a free path.** The check accepted
   "the element at the point contains the target" — but the event bubbles up, it
   does not descend: a container in front of the child never delivers the click
   to it. Measured with the button at (1133,272), the click sent exactly there,
   and the receiver was the `div.card.padded` that **contains** the button.
2. **Scroll found visible what was clipped.** The virtualized list clips by
   `overflow`, and a row 85px above the container's window counted as visible.
   Now "visible" is the same question as the click — *what is at the point?* —,
   and the center is the scroller's own, not the window's.
3. **The response did not check whether the event passed through the target.**
   The target receives a capture listener before the click; if the event does not
   pass through it, the response warns. `elementFromPoint` sees layers but does
   not know where the browser re-delivers the event (shadow host, iframe); the
   listener does.

The two questions — the click's and the scroll's — became a single definition:
their disagreeing was what made scroll say "already visible" and the click refuse
right after.

**Measured in the test round:** `snap` → ref of Candidate 413's "Open" → click →
`CHECK 17`, without `eval` and without selector math. And the eight checks I
redid (2, 8, 11, 12, 17, 18, 19, 20) all came out with a first-class command.

---

## Decided **not** to do (with the why)

- **Accept a ref from an old snapshot when it points to the same node.** The
  strict guard costs one extra `snap` and prevents clicking the wrong target —
  and the error is loud, with instructions on what to do. I do not trade safety
  for 0.2s.
- **Scroll "until the text appears"** as a command. It is composite (scroll →
  read → repeat) and the agent builds it with what exists.
- **Target highlight (the purple outline).** Removed by preference: it stayed lit
  after the action and, with the page scrolling, pointed at nothing. It comes
  back with `AXSCOPE_SPOTLIGHT=1`.
- **Guess a target inside canvas.** There is nothing to look for: the drawing is
  not DOM — it is not in the accessibility tree nor for `text=`, and there is no
  element under the point (the `elementFromPoint` returns the canvas itself). The
  path is `pos=x,y`, with the point coming from whoever knows where it drew.
  Measured in scenario 14: the circle is at (470,95) in the canvas coordinates,
  and the point only reaches the page after being mapped by the element's scale.

---

## Harness: scenarios

All **16** done. The harness scoreboard does not reflect this because the
*Reset state* button erases the completed ones on each use — that is its design.

The ones that asked for a change in the tool are in the items above, with what
they taught: upload (11 → item 4), Shadow DOM (12 → item 5), iframe (13 → item
3) and click on a target that refuses the action (15 → README, in the *Usage*
section). Canvas (14) and mutating element (16) passed with what already existed —
in both, the work was choosing the point and the moment, not touching the tool.
