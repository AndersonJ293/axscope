# axscope Architecture

A factual map of how the code is organized today: the layers, who may import
whom, and the invariants that keep it that way. It is written for whoever is
reading the tree for the first time.

## Overview

`axscope` is one Go binary with four roles (`client`, `serve`, `mcp`, `install`).
All four converge on a single `agent.Agent` that owns the browser session and runs
commands. The same command table (`internal/command`) is parsed by the CLI, by the
script runner and by the MCP server, so the surface cannot drift between them.

The flow of a request, at a high level:

```
CLI / script / MCP  ->  protocol.Request  ->  agent.Run  ->  handler  ->  browser.Session  ->  cdp
```

The `agent` translates a request into domain calls; the `browser` domain speaks
CDP through `internal/cdp` and evaluates DOM through `internal/dom`; `render`
draws the cursor and HUD through the `browser.Presenter` port.

## Layers and dependency direction

Dependencies always point **downward**. Never upward, never in a cycle.

```
entrypoints      cmd/axscope · cmd/cdpprobe
                      |
transports       daemon · daemonclient · mcpsrv · bridge
                      |
orchestration    agent              lifecycle + dispatch registry + handlers
                      |
domain           browser            tabs, targets, input, snapshot, observe
                  ^   |
                  |   v
presentation     render             implements browser.Presenter
                      |
primitives       dom · cdp          DOM/eval helper · raw protocol client
                      |
leaves           protocol · command · paths · installer
```

Two arrows deserve a note:

- `render` imports `browser` (it implements the port); `browser` never imports
  `render`. That is the inversion described below.
- `agent` imports both and wires them together.

The exact rules:

1. A package may import only packages below it in the diagram — the single
   exception is `render`, which implements the port declared above it.
2. No import cycles, not even a short one.
3. `cmd/*` is the only place that assembles transports and entrypoints.
4. Leaves (`protocol`, `command`, `paths`, `installer`) depend on nothing inside
   `internal/` except downward.
5. `bridge` is the transport that hands the agent a ready `*cdp.Client` from the
   browser extension; nothing else imports it.

## Package responsibilities

| Package | Responsibility |
|---|---|
| `cmd/axscope` | entrypoint: global flags, `serve`/`mcp`/`install`/`clean`/`stop`, client path |
| `cmd/cdpprobe` | diagnostic entry: probes a CDP endpoint (`internal/cdp` + `internal/dom`) |
| `internal/agent` | orchestration: session lifecycle (`agent.go`), command registry (`dispatch.go`), `cmd_*.go` handlers, refs and script |
| `internal/browser` | domain: session, launcher, target resolution, input, snapshot, observe, tabs, upload |
| `internal/render` | presentation: cursor, HUD, ripple and spotlight; implements `browser.Presenter` |
| `internal/dom` | the single DOM/eval helper: `Eval`, `EvalAwait`, `EvalString`, `EvalObject`, `BoxOf`, `ScrollTo` |
| `internal/cdp` | raw CDP client over WebSocket: send a command, wait for a response, deliver an event |
| `internal/protocol` | `Request`/`Response` types (leaf) |
| `internal/command` | canonical `Specs` table and parser shared by CLI, script and MCP (leaf) |
| `internal/paths` | on-disk paths: data dir, profiles, logs, active tab (leaf) |
| `internal/installer` | engine download (leaf) |
| `internal/daemon` | unix-socket server that hosts the agent |
| `internal/daemonclient` | client that talks to the daemon socket |
| `internal/mcpsrv` | MCP server over stdio, delegating to the daemon client |
| `internal/bridge` | in-browser bridge: the extension transport |

`cdp` is deliberately dumb: it does not know what a tab, a target or a click is.

## Domain/presentation inversion

The domain must not know how actions are drawn. `internal/browser` declares the
port:

```go
// internal/browser/presenter.go
type Presenter interface {
    Install(ctx context.Context, client *cdp.Client, session string) error
    MoveCursor(ctx context.Context, client *cdp.Client, session string, x, y float64) error
    Press(ctx context.Context, client *cdp.Client, session string, x, y float64, kind string) error
    Spotlight(ctx context.Context, client *cdp.Client, session string, rect *dom.Rect) error
    SetHUD(ctx context.Context, client *cdp.Client, session, tabs, label string) error
}
```

`internal/render` implements it (`render.Presenter`). `agent` injects the
implementation when it builds the session:

```go
// internal/agent/agent.go
sess, err := browser.NewSession(ctx, handle.Client, false, render.Presenter{})
```

So `browser` never imports `render`, and a different presentation (or a no-op
one) can be plugged in without touching the domain.

## Command surface as a single source of truth

`internal/command.Specs` is the canonical table of commands, positionals and
flags. From it derive:

- the CLI help and the parser (`command.Parse`, `command.Help`),
- the script runner,
- the MCP tool list.

Adding a command means two changes, not three: a `Spec` entry and a handler
registration in `agent.routes()` (`internal/agent/dispatch.go`). The dispatch is a
`map[string]route`, not a `switch` parallel to the table.

Keep the help text in `command.go` and the README in sync when the surface
changes; they document the same thing.

## Invariants that hold today

- **One responsibility per file, ~400 lines as a ceiling.** Past that, two things
  likely live together.
- **No new dependency without justification.** The binary is static and light on
  purpose.
- **Comments are few and high-value.** State a non-obvious *why* in at most 2–3
  lines. No history, no first person, no restating the code; prefer readable code
  over explanatory comments.
- **No dead code and no empty `catch` blocks.** If something leaves, it leaves
  for real.
- **No behavior change hidden in a refactor.** A behavior change is its own
  commit, with a rationale.

## What lives in the tests

`go test ./...` covers the pure pieces that are cheap to pin down: the parser
(`command_test.go`), the snapshot tree rendering and its regressions
(`snapshot_test.go`), target resolution (`target_test.go`), field semantics
(`field_test.go`), clickables, iframes, the agent handlers, the protocol and the
MCP surface. The presentation overlay itself is JavaScript shipped as
`render.inject.js` and has no Go test.
