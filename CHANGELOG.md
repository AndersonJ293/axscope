# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
