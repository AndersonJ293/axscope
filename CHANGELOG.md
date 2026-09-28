# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `batch`, the fast path for an agent: a list of commands (lines or objects) in
  one call, with each step's position and time, a stop-at-first-error summary
  (`--continue` to go on) and `snap=final` to read the screen at the end. Over
  MCP `steps` is a typed list, and an agent issuing single actions in a row gets
  an occasional tip pointing at it.
- `script` accepts `content=` over MCP, so a script no longer needs a file.
- `snap --delta` prints only the lines that changed since the tab's previous
  reading, with one line of context.
- A `snap` answer has a ceiling (24 KB, `AXSCOPE_SNAP_MAX_BYTES`): past it the
  answer keeps the start and saves the whole reading to a file.

### Changed

- `snap` summarizes a closed `<select>` with more than 10 options into one line
  with the value and the count, counts a list's empty placeholder rows instead
  of listing them, and `depth=` no longer cuts a target whose only content is
  its own label.

- The `initialize` instructions carry the grammar of the `batch` steps, not
  only `batch`'s description: a client may serve a tool schema cached from an
  earlier connection.

- The MCP catalog is 8 tools (`batch`, `snap`, `open`, `click`, `fill`, `wait`,
  `shot`, `help`) instead of 34: every other command runs as a `batch` step,
  and `batch`'s description lists their grammar. `AXSCOPE_MCP_TOOLS=all` keeps
  one tool per command.

- Refs are stable across readings of the same page: an element keeps its
  number, and a new one gets a number above every earlier one.
- `wait --change [within=<target>]` waits for the page or a region to change and
  settle (DOM quiet, network idle, no `aria-busy`), then reads it with refs; the
  region may not exist yet.

- `snap within=<target>` reads one element (a ref, `css=`, `text=`, or a plain
  `<div>` the tree drops), without scrolling the page.
- `snap` reads an open modal dialog alone (`aria-modal` or `showModal()`), and
  the header names it and `--page`, which reads the whole page.
- `snap depth=N` cuts the reading at N levels; each cut container keeps a ref
  and a count of the targets inside, to open with `within=`.
- `snap --viewport` leaves out what is wholly off screen and counts it.
- An action on a `text=`/`css=` target that matched several elements says so
  (`note: 5 elements match … acted on the first`), and `>> nth=N` (Playwright's
  syntax, 0-based) picks another.

- `AXSCOPE_MCP_TIMEOUT_MINUTES` bounds a single MCP tool call (default `10`,
  `0` = no bound), so a client that never cancels cannot hold a call forever.
- `click --dom`, for pages that refuse the real pointer (`pointer-events: none`,
  a handler that only trusts a programmatic click).
- `open --force`, which accepts a `beforeunload` so an explicit navigation can
  leave a page with unsaved changes.
- `open` and `newtab` name the tab they landed on (`tab: [index] targetId`), no
  `tabs` round trip needed.

### Fixed

- Only the page's own requests count as its work in flight: an iframe's loads
  (a cross-origin one reports their end in its own session) no longer make the
  first seconds after a load wait out the settle cap on every action.

- `wait --change` counts a change the previous step already caused (a list
  that opened during the `click`, a filter applied during the `type`) instead of
  answering "nothing changed": the page keeps a short log of its DOM changes.

- A request open for more than 5 s (a cross-origin iframe's document such as
  reCAPTCHA's, an EventSource, a long poll) no longer counts as the page being
  busy, and a new main document forgets the old page's open requests. On a page
  with reCAPTCHA every action used to wait out the 1.5 s settle cap and
  `wait --network-idle` never went idle: a `press` goes from 1.5 s to 3 ms.

- The MCP server exits on SIGTERM (it swallowed the signal while blocked
  reading stdin), when its client process is gone, and when a newer server of
  the same client took over and it sat idle for 10 minutes: a client that
  reconnects without closing the old server's stdin no longer leaves servers
  and browsers behind.

- An MCP tool call no longer freezes the whole server: calls run concurrently, so
  a slow `wait` blocks neither `ping` nor the next call, and a client
  `notifications/cancelled` reaches the command already in flight.
- The daemon stops a command when its client goes away (a cancellation, a lost
  connection), releasing the run lock instead of queueing every later request
  behind a stuck command.
- A tab attaches on the session's own lifetime, not the request's: the command
  that built the session no longer poisons every later `newtab`, `tab` or `open`
  on a fresh target with `failed to acquire lock: context canceled` once the
  client that ran it left (acting on the already-attached tab still worked).
- The daemon client reads a response under the caller's context, so a daemon that
  accepted a request and never answered no longer blocks the client forever.
- A tab the page opens (`target=_blank`, `window.open`) that is born already in
  the session's group is now reported to the daemon: the extension claims it by
  its group on creation, instead of only on a group change (which never comes).
- Input (and `IntersectionObserver`) now reaches a background tab: each tab
  emulates focus (`Emulation.setFocusEmulationEnabled`), so `click`/`scroll` no
  longer need `tab --focus`, which brought the browser forward and stole the tab
  the user was on.
- The extension stopped grouping tabs after the user removed the group (a dead
  `groupId` was kept), and adoption is now keyed on the **session**, not the
  agent name, so two sessions of the same agent get separate groups.
- A plain `ref` (`e12`) now means the current reading; the generation suffix is
  optional, and an explicit older one is still refused.
- The snapshot header reports horizontal scroll when an area scrolls sideways.
- `open` aborted by a `beforeunload` names the cause and the way out instead of a
  raw `net::ERR_ABORTED`.
- `daemon.Run` stats the socket path once instead of twice; the redundant,
  racy check around the stale-socket cleanup is gone.

### Security

- The daemon socket is created with mode `0600` and its runtime directory with
  `0700`. When there is no `XDG_RUNTIME_DIR`, the fallback directory is now
  per-user (`$TMPDIR/axscope-<uid>`), and the daemon refuses a runtime directory
  that is a symlink or owned by another user — closing the local squatting path.

### Removed

- The `dialog` command from the command surface. It was declared in the catalog
  but never implemented; native dialogs are always dismissed (as documented).

### Changed

- The cursor glides from its last point to the target (40–80 ms, by distance)
  and the input lands on arrival, replacing the fixed 160 ms pause: a click
  drops from ~540 ms to ~100 ms. It stays on screen across navigations, tilts on
  click, and has a new look. `AXSCOPE_CURSOR_DELAY` now caps the glide
  (default 80).

- Readability pass: comments reduced to a few high-value lines, with no internal
  history. Documentation moved to `docs/` (`ARCHITECTURE.md`, `BACKLOG.md`).
- Tests now run with the race detector in CI.

## [0.1.0] - 2026-09-15

First public release.

### Added

- Agent-driven browser with a single binary and four roles: `client`, `serve`,
  `mcp` and `install`.
- `snap`: reads the page as text from the accessibility tree, with stable `ref`s,
  scroll state and a list of clickables the tree does not mark.
- Actions by identity (`ref`, `css=`, `text=`, `pos=x,y`) with real refusal
  diagnostics when the target is disabled, covered or clipped.
- Convergence commands (`wait`, `waitgone`) that fail loudly instead of masking
  timing with `sleep`.
- Batch execution through `script`.
- A rendered cursor, ripple, HUD and target spotlight injected into the page.
- Three engines: the browser extension (your own, logged-in browser), Chrome for
  Testing and chrome-headless-shell.
- MCP server over stdio, sharing the same daemon and refs as the CLI.
- Multi-session isolation through per-session tab groups in the extension mode.

[Unreleased]: https://github.com/AndersonJ293/axscope/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/AndersonJ293/axscope/releases/tag/v0.1.0
