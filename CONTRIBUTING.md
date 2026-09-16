# Contributing to axscope

Thanks for taking the time to contribute. This document describes how to build,
test and propose changes.

## Development setup

Requirements:

- Go 1.27+
- A Chromium-based browser (Brave or Chrome) for manual testing
- Linux is the primary target today

```bash
git clone https://github.com/AndersonJ293/axscope
cd axscope
make build          # builds ./axscope
make vet            # go vet ./...
go test ./...       # unit tests
```

To install locally while developing:

```bash
make install        # → ~/.local/bin/axscope
```

## Before you open a pull request

- `gofmt -w` your files (the CI checks formatting).
- `go build ./...`, `go vet ./...` and `go test ./...` must pass.
- Keep the diff focused: one concern per pull request.
- If you change a command, a flag or the MCP surface, update the help text in
  `internal/command/command.go` and the README together — they are the source of
  truth for both the CLI and MCP.

## Design conventions

- **Comments are few and high-value.** State a non-obvious *why* in at most 2–3
  lines. No history, no first person, no restating the code; prefer readable code
  over explanatory comments.
- **One responsibility per file**, around 400 lines as a ceiling.
- **No new dependencies without justification.** The binary is static and light
  on purpose.
- **No behavior change hidden in a refactor.** If behavior must change, that is
  its own commit, with a rationale.
- **No dead code and no empty `catch` blocks.**

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
`fix:`, `docs:`, `refactor:`, `test:`, `chore:`. Write the summary in the
imperative mood, in English.

Example:

```
fix: refuse a click that does not reach the target

elementFromPoint sees layers but does not know where the browser re-delivers
the event (shadow host, iframe); the capture listener does.
```

## Reporting bugs and requesting features

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`. For a bug,
include:

- what you ran (`axscope ...`), the mode (`ext`, `--chrome`, `--headless`),
- what you expected and what happened,
- the output of `axscope snap` and, if relevant, `axscope console`.

## Security

Do **not** open a public issue for a security problem. See
[`SECURITY.md`](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the MIT
License (see [`LICENSE`](LICENSE)).
