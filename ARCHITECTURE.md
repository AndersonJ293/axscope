# axscope Architecture

This document is a **contract**: it describes the layers, who may import whom,
and the phases of the refactoring that takes the project out of its current
state. Whoever executes follows this; whoever reviews checks against this.

## Diagnosis (measured, not guesswork)

| Problem | Evidence |
|---|---|
| `internal/browser` accumulates 6 responsibilities | 2910 lines = 46% of the code |
| ...and depends on the **presentation** | 26 calls to `overlay.` in `actions.go`/`session.go` |
| `internal/agent` is a God file | 1007 lines: lifecycle + router + 25 handlers + refs + parser |
| Router is a `switch` parallel to the command table | a new command requires touching spec, switch and handler |
| `eval` **triplicated** | `page.Eval*`, `agent.evalString`, `browser.evalObject/evalString` |
| `page` exists for a single consumer | only `overlay` uses it |
| `cli` doesn't say what it is | it is the daemon client, used by `cmd/axscope` and `mcpsrv` |

## Layers and dependency direction

The dependency always points **downward**. Never upward, never in a cycle.

```
   cmd/*                  entrypoints (CLI, probe)
     |
   agent                  orchestration: lifecycle + command handlers
     |
   browser                DOMAIN: tabs, targets, actions, snapshot
     |        \
   dom         render     (render is plugged in via interface, see below)
     |
   cdp                    protocol client, raw
     |
   protocol  paths  command  installer     leaves (no internal deps)
```

Rules:

1. **`browser` does not know `render`.** The domain declares a port
   (`browser.Presenter`) and `agent` injects the implementation. Today the
   domain calls `overlay.Spotlight/…` directly — it is a dependency inversion.
2. **`agent` does not speak CDP directly.** It uses `browser` and `dom`.
   Conscious exceptions become a comment explaining why.
3. **One responsibility per file, ~400 lines as ceiling.** Past that, it is a
   sign that two things live together.
4. **A new command registers in one place only.** A registry `name → handler`
   replaces the `switch`. The `command.Specs` table remains the source of truth
   for what exists (CLI, help and MCP derive from it).
5. **A single `eval`/DOM helper** (`internal/dom`). No third copy.
6. **`cdp` is dumb**: it sends a command, waits for a response, delivers an
   event. It does not know what a tab, a target or a click is.

## Target structure

```
cmd/axscope/                 entry: global flags, install/engines/clean/mcp/serve/stop
cmd/cdpprobe/           diagnostic entry

internal/
  protocol/             request/response (leaf)
  command/              spec table + parser (leaf, uses protocol)
  paths/                on-disk paths (leaf)
  installer/            engine download (leaf)
  cdp/                  raw CDP client
  dom/                  DOM/runtime access: Eval, EvalAwait, EvalString,
                        EvalObject, BoxOf, ScrollTo — one place only
  browser/              DOMAIN
    session.go            tabs, attach, navigation, convergence
    launcher.go           launch/attach browser, engines
    observe.go            console, network, dialogs
    target.go             target resolution: ref/css/text/pos
    input.go              click, drag, keyboard, scroll
    snapshot.go           accessibility tree → text
    presenter.go          presentation PORT (interface)
  render/               implements browser.Presenter: cursor, HUD, ripple
  agent/                ORCHESTRATION
    agent.go              lifecycle (ensure, Close, setAgent)
    dispatch.go           registry name → handler
    refs.go               refs and snapshot generation
    script.go             script parser
    cmd_navigate.go       open, back, forward, reload, wait, waitgone
    cmd_interact.go       click, hover, drag, fill, type, press, select, check, scroll
    cmd_inspect.go        snap, read, console, net, eval, status
    cmd_tabs.go           tabs, tab, newtab, closetab
    cmd_capture.go        shot, script
  daemon/               socket server
  bridge/               in-browser bridge (extension)
  daemonclient/         daemon client (was `cli`)
  mcpsrv/               MCP server (stdio)
```

## Refactoring invariants

- **No behavior change.** It is refactoring: same messages, same results, same
  flags. If a behavior needs to change, that is another commit, with a
  justification.
- **Build and vet green at the end of each phase** (`go build ./... && go
  vet ./...`).
- **No hacks**: no aliases/re-exports to "not break", no empty `catch`, no dead
  file. Whoever leaves, leaves for real.
- **A comment says why**, not what. It is the project convention.
- **No new dependency** without justifying it (the binary is static and
  lightweight on purpose).
- **Smoke test at the end**: `tabs`, `snap`, `hover`, `drag`, `shot` in the lab.

## Phases

Each phase is independent and verifiable. Do not start the next one before the
previous one is green.

### Phase 0 — safety net (before touching anything)

There is no test at all today. Refactoring without a net is a gamble.

- Extract from `snapshot.go` the **pure** part (`buildText(nodes) string`) and
  test it with an accessibility-tree fixture. That is where all the noise
  trimming lives — the most fragile logic of the project.
- Test `command.Parse` (input table → expected request).
- Test `refGen` and `splitTokens`.

Criterion: `go test ./...` green, with the tests covering the snapshot trimming
(footer, skip-links, name echo, identical siblings, anonymous wrapper).

### Phase 1 — invert the presentation

- Declare `browser.Presenter` (cursor, press, spotlight, HUD).
- `browser` stops importing `overlay`; it receives the port in the
  session/agent.
- `overlay` becomes `render` and implements the interface.

Criterion: `grep -r "internal/overlay" internal/browser` empty; build green.

### Phase 2 — a single `dom`

- `internal/dom` with `Eval`, `EvalAwait`, `EvalString`, `EvalObject`, `BoxOf`.
- `page` is absorbed; `agent.evalString`, `browser.evalObject`,
  `browser.evalString` and `boxOf` go away.

Criterion: a single implementation of each helper; `internal/page` no longer
exists.

### Phase 3 — split the `agent`

- `dispatch.go` with a `map[string]handler` registry; the `switch` goes away.
- Handlers in the `cmd_*.go` files by area.
- `refs.go`, `script.go`.

Criterion: no file in `internal/agent` above ~400 lines; adding a command
requires touching **two** places (spec + handler) and not three.

### Phase 4 — split `browser/actions.go`

- `target.go` (resolution) and `input.go` (actions).

Criterion: no file in `internal/browser` above ~450 lines.

### Phase 5 — honest names

- `overlay` → `render` (if Phase 1 did not do it), `cli` → `daemonclient`.
- `cmd/cdpprobe` reuses what already exists instead of duplicating the engine.

Criterion: build green and smoke test passing.

## What does NOT go in here

- Behavior change, snapshot optimization, new feature.
- Real-browser integration test suite (left for later; Phase 0 covers what is
  pure).
