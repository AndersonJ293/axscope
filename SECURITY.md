# Security Policy

## Reporting a vulnerability

Please **do not** report security vulnerabilities through public issues.

Use GitHub's private vulnerability reporting: go to the **Security** tab of the
repository and choose **Report a vulnerability**. If you cannot use that channel,
open a minimal issue asking a maintainer to contact you privately, without
disclosing the details.

Please include:

- a description of the issue and its impact,
- the mode in use (`ext`, `--chrome`, `--headless`, attach),
- steps to reproduce, if possible,
- any suggested fix.

We will acknowledge receipt as soon as we can and keep you updated on the fix.

## Threat model

axscope drives a real browser on your machine. That gives it a security-relevant
surface; this section describes what is in scope.

### Local attack surface

- **The daemon socket** lives in the user runtime directory
  (`$XDG_RUNTIME_DIR/axscope/<session>.sock`, or the system temp dir as a
  fallback) and speaks a local protocol. Anyone who can write to that socket can
  drive the browser session. It is meant to be reachable only by the same user.
- **The extension bridge** listens on `127.0.0.1` on ports in the
  `8787–8802` range and exchanges CDP with the extension over WebSocket. It is
  bound to loopback.
- **`chrome.debugger`** gives the extension full CDP access to the tabs in its
  group: page content, cookies sent with requests, screenshots, input. Only tabs
  inside the agent's tab group are attached, and only on demand.

### Untrusted page content and prompt injection

Page content — text, HTML, accessibility names, console output, network bodies —
is **untrusted data**. It is read and handed back to the agent that consumes
axscope's output. A malicious page can embed text crafted to manipulate that
agent (prompt injection).

axscope does not interpret page content as instructions, but it cannot protect a
consumer that does. When you point an LLM agent at untrusted pages, treat every
byte that comes back from `snap`, `read`, `console` and `net` as attacker-
controlled input.

### Credentials and profiles

Profiles are persistent and may contain logged-in sessions. `axscope clean`
preserves profiles unless you pass `--all`. Never commit or share a profile
directory.

### Out of scope

- Vulnerabilities in Chromium, Brave or Chrome itself.
- Vulnerabilities in a third-party agent/LLM that consumes axscope's output.
- Attacks that already require code execution as the same local user.
