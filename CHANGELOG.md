# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `AXSCOPE_MCP_TIMEOUT_MINUTES` bounds a single MCP tool call (default `10`,
  `0` = no bound), so a client that never cancels cannot hold a call forever.
- `click --dom`, for pages that refuse the real pointer (`pointer-events: none`,
  a handler that only trusts a programmatic click).
- `open --force`, which accepts a `beforeunload` so an explicit navigation can
  leave a page with unsaved changes.
- `open` and `newtab` name the tab they landed on (`tab: [index] targetId`), no
  `tabs` round trip needed.

### Fixed

- An MCP tool call no longer freezes the whole server: calls run concurrently, so
  a slow `wait` blocks neither `ping` nor the next call, and a client
  `notifications/cancelled` reaches the command already in flight.
- The daemon stops a command when its client goes away (a cancellation, a lost
  connection), releasing the run lock instead of queueing every later request
  behind a stuck command.
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
