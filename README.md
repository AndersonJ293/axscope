# axscope

**Agent-driven browser:** it reads the screen as text, acts by identity, and
renders a cursor — for the agent and for whoever is watching.

One binary, four roles:

```bash
axscope <command>   # client: talks to the daemon (starting it if needed)
axscope serve       # the daemon (live browser, unix socket)
axscope mcp         # MCP server over stdio, pointing at the same daemon
axscope install     # downloads Chrome for Testing
```

## Principles

1. **Read the screen as text** (`snap`) — an accessibility tree with stable
   `ref`s, never raw HTML or pixels.
2. **Act by identity** (`click e12`, `css=...`, `text=...`) — a coordinate is
   never the first option.
3. **Converge, don't sleep** (`wait`/`waitgone`, `Settle`) — fail loudly when
   the page will not settle instead of masking it with `sleep`.
4. **Batch** (`script`) — N steps on one connection, no cold start per step.
5. **See** — cursor, halo, target spotlight and a tab HUD injected into the page.

## Why raw CDP (and not Playwright)

Playwright is a _testing_ framework: it wants to own the browser lifecycle,
boots its own Chromium with a clean profile (losing your logins) and hides the
protocol — the `ref` snapshot it uses in its MCP is an internal API
(`_snapshotForAI`).

CDP hands over exactly what matters:

| Need | Where it comes from |
|---|---|
| Native tabs | `Target.*` |
| The screen as text | `Accessibility.getFullAXTree` |
| Cursor at an exact coordinate | `Input.dispatchMouseEvent` |
| An overlay that survives navigation | `Page.addScriptToEvaluateOnNewDocument` |

And Node is not a dependency: the CDP client is Go + `WebSocket`.

## Install

```bash
make install                       # → ~/.local/bin/axscope
axscope install --engine all       # Chrome for Testing + chrome-headless-shell
axscope engines                    # what is available
```

If `~/.local/bin` is not on your `PATH`, use `PREFIX=/usr/local/bin make install`.

The binaries live in `~/.local/share/axscope/browsers/`. The profile lives in
`~/.local/share/axscope/profiles/<session>/` — it is persistent, so logins
survive across runs.

`--engine` accepts `chrome` (default), `shell` (chrome-headless-shell) or `all`.

### MCP in opencode

```json
{
  "mcp": {
    "axscope": {
      "type": "local",
      "command": ["/home/USER/.local/bin/axscope", "mcp"]
    }
  }
}
```

The MCP exposes a **lean** set of tools (tool schemas cost context on every
request). To open all of them: `AXSCOPE_MCP_TOOLS=all`.

## Usage

```bash
axscope open https://example.com     # navigates (or uses the active tab)
axscope snap                         # reads the screen
axscope click e1                     # acts by the ref from the last snap
axscope fill e5 "owner@example.com"
axscope press Enter
axscope wait "Dashboard"             # converges, doesn't sleep
axscope wait "Ready" within=css=#list   # the text, but only inside the container
axscope wait css=#submit --enabled   # waits for the state, not the text
axscope tabs                         # open tabs (the active one is marked *)
axscope shot /tmp/evidence.png       # capture (with cursor and spotlight)
axscope script scenario.txt          # batch script
```

A target accepts four forms: `e12` (ref), `css=.button`, `text=Sign in` and
`pos=x,y` (the element under the point — and **the action happens at the
point**, not at its center, which is what lets you click inside an iframe). Open
shadow roots are traversed: the accessibility tree flattens them — the snapshot
shows what is inside, with a ref — and aiming by `text=`/`css=` reaches in too.

When the target refuses the action, the action **is not sent** and the response
says why and what to do next: the target is disabled (`wait --enabled`), the
target is covered (`to click the point anyway, use pos=x,y`), or the target is
outside the window of the container that clips it. Clicking a covered button
answers `ok` and does nothing — and still lands on the top layer, with whatever
side effect the page gives it. Text that did not make it into the field
(`the field is still empty`) and a click that was sent but the target never saw
are also not silently ignored.

The snapshot header says where you are, including inside a scrollable area:

```
-- 261 lines, 57 refs · scroll: page 2075/2844 · #virtual 5000/41672
```

The accessibility tree does not carry scrolling, so this comes from the DOM. The
largest areas come first, and the short selector (`#id`, `tag.class`) works
directly with `css=`.

The end of the snapshot lists the **clickables the tree does not mark** — a `div`
with a handler and `cursor: pointer`, the case of the chat that never becomes a
target:

```
-- clickables without a role in the tree (the snapshot does not mark them; here is the selector):
  div[data-id="ana"] — "Ana Souza — Hi Ana! I'm interested"
```

The criterion is deliberately narrow: only a container **with no target inside**.
A card that wraps buttons already has targets, and listing it would be noise —
the same page produced 249 candidates by raw `cursor: pointer` against 3 by this
criterion. The selector is checked against the page itself and prefers the data
attribute over the class: a state class (`active`) changes, and a selector that
carries it breaks on its own.

### Scripts

One step per line, `#` comments, quotes for spaces:

```
# scenario: sign in to the dashboard
open https://example.com/login
snap
fill e1 "owner@example.com"
fill e2 "password"
click e3
wait "Dashboard"
snap
shot /tmp/dashboard.png
```

## Modes: which browser

The engine is a property of the **session**: the daemon starts the browser with
the chosen engine on the first call. Changing the engine on a live session
requires `stop` (or use another session). `axscope engines` shows what is
installed.

| Mode | Engine | RAM (1 tab) | Sees the screen? |
|---|---|---|---|
| **(default) extension** | your Brave, already logged in | — (already open) | ✅ tabs + cursor |
| **`--chrome`** | Chrome for Testing | ~1850 MB | ✅ tabs + cursor |
| **`--headless`** | chrome-headless-shell | ~505 MB | ❌ |

```bash
# default: your Brave, through the extension (the everyday mode)
axscope open https://example.com

# dedicated Chrome, when you want a separate browser
axscope --chrome open https://example.com

# no window at all, for batch work
axscope --headless script script.txt

# stop everything (all modes and their browsers)
axscope stop --all
```

Each mode is a **separate session** (`ext`, `chrome`, `headless`), so they
coexist: you can leave a script running windowless while you watch something
else in Brave.

The light mode is **not** a stripped-down Chromium: it is the same engine with
the same CDP, the same accessibility tree, the same real geometry and the same
coordinate click. There is just no window — so no drawn cursor.

Attaching still works for any Chromium already open with
`--remote-debugging-port=PORT` (`AXSCOPE_ATTACH=host:port`), letting you use your
everyday browser with your logins.

## The engine: what the research proved

The question "is there a lighter browser?" has a measured answer, not an opinion.

| Engine | Renders? | CDP | Verdict |
|---|---|---|---|
| **Chrome for Testing** | yes | complete | default of the **chrome** mode |
| **chrome-headless-shell** | yes (no window) | complete | **headless** mode: same engine, ~3.7x less RAM |
| Thorium / Helium / ungoogled / Cromite | yes | complete | the same Chromium with a patch; not smaller |
| Servo / WebKitGTK / QtWebEngine | yes | no (WebDriver) | loses CDP |
| **Lightpanda** | **no** (no rendering engine) | partial | see below |

### Lightpanda, measured

Probe in `cmd/cdpprobe` against `lightpanda serve`:

```
Browser.getVersion                 OK
Target.getTargets                  OK    0 targets   ← does not use the target model
Target.createTarget                OK    FID-0000000001
Accessibility.getFullAXTree        OK    (real tree, with role/name)
Runtime.evaluate                   OK    document.title = "Example Domain"
DOM.getDocument                    OK
Page.captureScreenshot             OK    (PNG of textual rendering)
DOM.resolveNode                    OK    => refs work
Accessibility (2 connections)      OK    two independent pages
geometry: <a> getBoundingClientRect  {width:5, height:5}  ← not real layout
```

Conclusions (corrected by measurement, not assumption):

- **Tabs: yes.** Each connection is an independent session, with its own page,
  cookies and memory — it is its `session_new` and the "MultiClient" from the
  blog. Not tabs in a visible bar, but independent, manageable pages.
- **Cursor: no, and it cannot.** The screenshot doc is explicit: *"the text
  layout Lightpanda computes, not a pixel-accurate browser rendering (no images,
  fonts or CSS colours)"*. With no faithful pixel, a drawn mouse is decoration.
- **Action by coordinate: no.** The `<a>` measured 5×5 — there is no real layout.
  That is why all of its click tools go by `selector`/`backendNodeId`. Our click
  (center + `Input.dispatchMouseEvent`) does not apply; it would mean firing an
  event on the node instead.
- **AX + refs: works.** 15 nodes, 11 with `backendNodeId`, and `DOM.resolveNode`
  OK.
- **Memory is the real gain**: 36 MB against ~505 MB for the headless shell. Even
  so, the headless shell delivers everything (geometry, coordinate click, faithful
  screenshot) — except the window.

Recommended use of Lightpanda: its native MCP, for crawl/extraction at scale. Not
as a visual engine.

## Extension mode: your own browser

Instead of starting a dedicated browser, the extension drives **your Brave** —
with the logins you already have. It is the most useful mode day to day.

```bash
axscope --ext open https://example.com
axscope --ext snap
axscope --ext click e3
```

### Why it needs an extension

Since Chrome/Chromium **136**, `--remote-debugging-port` is **ignored** when
using the default profile (a security measure so passwords and cookies are not
exposed). In other words: there is no debug-port path into your real profile. The
extension uses `chrome.debugger`, which works on the browser already open,
without restarting anything.

### Setup (once)

1. **Load the extension in Brave**
   `brave://extensions` → enable **Developer mode** → **Load unpacked** → point
   to `extension/` in this repository.

2. **Hide the debug banner** (optional, but recommended)
   The `chrome.debugger` API makes Chromium show a *"axscope started debugging
   this browser"* banner on every tab. To hide it:

   ```bash
   scripts/brave-hide-debug-banner.sh install   # creates a .desktop override, no sudo
   # close Brave completely and open it again
   scripts/brave-hide-debug-banner.sh remove    # to revert
   ```

3. Check the connection in the extension icon (it should say **connected**).

### How it works

```
extension (Brave)  ⇄  one connection per session  ⇄  daemon (Go)  ⇄  CLI / MCP
  chrome.debugger → real CDP on the tab
  chrome.tabs     → Target domain (tabs)
  chrome.tabGroups→ one group per session (the isolation)
```

Each session takes a port in the **8787–8802** range and gets its **own tab
group** in Brave, named `axscope · <session>`. The extension only sees and only
touches the tabs in that session's group.

This solves three things at once:

- **several agents at the same time**, each with its own group, without
  fighting over a port;
- **each agent with as many tabs as it wants** inside its own group;
- **your personal tabs untouched** — and since it is the same profile, the
  agent's tabs are already logged in to your sites.

The extension synthesizes **only** the `Target` domain (tabs ↔ `chrome.tabs`) and
forwards everything else — `Accessibility`, `DOM`, `Input`, `Runtime`, `Page` —
to `chrome.debugger`. That is why the whole driver works unchanged: the
accessibility tree is the **real** one, the click is by coordinate and the cursor
is rendered just like in the other modes.

Each tab is attached **on demand** — only the one being used. Opening the agent
does not sweep or instrument your tabs.

### Group name

The group appears as **`<Agent> <N>`** — `Opencode 1`, `Opencode 2`, `Claude 1`.
The number is assigned by the extension (the next free one for that agent).

The agent name comes from the MCP configuration that is driving it:

```json
{
  "mcp": {
    "axscope": {
      "type": "local",
      "command": ["/home/USER/.local/bin/axscope", "mcp"],
      "environment": { "AXSCOPE_AGENT": "Opencode" }
    }
  }
}
```

Without that, the MCP tries the client name (`clientInfo.name`) and the CLI uses
`axscope`.

### Grant and revoke access: drag the tab

The group **is** the permission interface. There is no menu or configuration:

- **drag one of your tabs into an agent's group** → it can now see and drive it
  (useful for working on a tab where you are already logged in, with the state
  you already set up);
- **drag it out** → the access is revoked immediately, and the debugger is
  released from that tab too.

Tabs the agent opens itself are born inside its group.

## Cursor overlay

Injected on every navigation via `Page.addScriptToEvaluateOnNewDocument`. Cautions
that cost real bugs:

- **no `innerHTML`** — pages with Trusted Types would reject the assignment;
- **CSS via `adoptedStyleSheets`** — immune to `style-src`;
- **positioning via CSSOM** (`el.style.*`), not via a `style` attribute;
- **`aria-hidden`** on the host — otherwise the HUD shows up in `snap` itself;
- **`top: auto`** on the HUD — the base rule sets `top:0` and the box stretched.

Visibility tuning: `AXSCOPE_CURSOR_DELAY` (ms, default 160) controls how long the
cursor "arrives" before acting; `0` removes the pause.

## Disk

| Where | Size | What |
|---|---|---|
| `~/.local/share/axscope/browsers/` | ~650 MB | downloaded Chrome + headless shell |
| `~/.local/share/axscope/profiles/` | varies | profile (logins, state) |
| `~/.local/share/axscope/logs/` | ≤ 2 MB per session | daemon log, truncated on startup |

`axscope shot <file>` writes **exactly where you tell it** — there is no default
folder and no automatic accumulation. The tool only writes on its own what is
necessary: profile, downloaded browsers (on `install`) and the log (bounded).

```bash
axscope clean          # logs and dead sessions
axscope clean --all    # includes profiles and downloaded browsers
```

## Lifecycle

The daemon keeps the browser alive on purpose: the next call answers instantly
and the state (login, tabs) survives between commands. It does **not** hang
around forever if you ask it not to: `AXSCOPE_IDLE_MINUTES` shuts it down after N
idle minutes (default `0` = disabled, because a new Chrome start brings the
window to the front).

To stop it right away, whenever you want:

```bash
axscope stop          # just the current session
axscope stop --all    # all sessions and all browsers
```

## Environment variables

| Variable | Effect |
|---|---|
| `AXSCOPE_SESSION` | session name (default `default`) |
| `AXSCOPE_ENGINE` | `ext`, `chrome` or `shell` (default `ext`) |
| `AXSCOPE_IDLE_MINUTES` | shuts the daemon down after N idle min (default 0 = off) |
| `AXSCOPE_HOME` | data directory |
| `AXSCOPE_CHROME` | Chromium executable |
| `AXSCOPE_ATTACH` | `host:port` of an already-open Chromium |
| `AXSCOPE_HEADLESS` | start without a window |
| `AXSCOPE_FORCE_AX` | `1` enables the accessibility tree when the browser starts (costs memory; the tree is normally enabled on demand) |
| `AXSCOPE_CURSOR_DELAY` | cursor pause before acting (ms) |
| `AXSCOPE_AGENT` | agent name for the tab group |
| `AXSCOPE_BRIDGE_PORT` | first port for the extension bridge (default `8787`) |
| `AXSCOPE_MCP_TOOLS` | `all` exposes every MCP tool |
| `AXSCOPE_SPOTLIGHT` | `1` re-enables the target outline |

## Project layout

```
cmd/axscope/       entrypoint (client, serve, mcp, install)
cmd/cdpprobe/      CDP engine diagnostic probe
internal/cdp/      CDP client over WebSocket
internal/browser/  launcher, session/tabs, snapshot, actions, observation
internal/render/   overlay.inject.js + bridge (cursor, HUD, spotlight)
internal/agent/    command dispatcher
internal/command/  shared parser (CLI, script, MCP)
internal/daemon/   unix socket server
internal/mcpsrv/   MCP server (stdio, hand-rolled JSON-RPC)
internal/installer/ Chrome for Testing download
```

## Known limitations

- **Cross-origin iframe (OOPIF) is not read**: `snap` shows the frame as a single
  line (`- Iframe`, no content), because that accessibility tree lives in the
  other site's process — reaching it requires a separate CDP session per frame
  (see [`docs/BACKLOG.md`](docs/BACKLOG.md)). A **same-origin** iframe is read in
  full, and
  the ref from inside works: the trees of both frames are merged in the snapshot.
- Native dialogs are always dismissed (`dismiss`), configurable later.
- `bootstrap` assumes the Chromium target model; alternative engines need their
  own path.
- **Reading and reaching are not the same set.** `snap` comes from the
  accessibility tree; aiming by `text=` walks the DOM. The two disagree in two
  measured cases:
  - **CSS-generated content** (`content: attr(...)`, typical of a tooltip) exists
    in the snapshot and **does not exist in the DOM** — and it enters the
    **name** of the surrounding element: a button went from `"Hover me"` to
    `"Hover me Tooltip loaded on hover"` after the hover. A target name is not
    stable; for logic that depends on it, use `ref`.
  - **Field label** is the reverse: `text=` now sees `placeholder` and an
    associated label to agree with what the snapshot shows, but a field **with no
    name at all** (no `aria-label`, label or placeholder) remains unreachable by
    identity — only by `ref` or `pos=`.
- `hover` enters from the outside on purpose: moving the pointer to where it
  already is does not fire `pointerenter`, and the action would answer `ok`
  without the page seeing anything.
- **`wait` and `text=` match by partial text — and the page usually repeats the
  word.** The response says where it found it (`in p.desc`), which is how you
  notice, and the way out is to wait for text that only exists on the target
  (`waitgone "Not yet"`).
- **A background tab freezes anything that depends on a frame.** With the
  document hidden (`document.hidden`), `IntersectionObserver` does not fire: a
  page that loads content that way — infinite feed, lazy image — does not advance
  no matter how much you scroll. `scroll` warns when it reaches the end in that
  condition, and the way out is `axscope tab <n> --focus` (bring the tab to the
  front) or `eval` calling the page's own function.
- `upload` through `<input type=file>` sends the **path**, which the browser
  reads — valid for browser and daemon on the same machine (the extension case).
  In a dropzone the content travels as bytes, so the path does not matter.
- Linux-first: the daemon uses a unix socket and the banner workaround is a
  `.desktop` override. macOS and Windows are not verified yet.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Security issues: see
[`SECURITY.md`](SECURITY.md).

## License

[MIT](LICENSE).
