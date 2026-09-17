#!/usr/bin/env bash
# Turns the "hide debugging banner" mode on/off in Chromium-based browsers
# (Brave, Chrome, Chromium).
#
# Why it exists: the extension uses chrome.debugger, and Chromium shows a banner
# "«Extension» started debugging this browser" on every tab. The flag
# --silent-debugger-extension-api hides it; it must be in the browser's launch
# command.
#
# On Linux we do this with USER overrides in ~/.local/share/applications, which
# take precedence over the system .desktop — no sudo and easy to revert. This is
# the only platform the script handles: macOS and Windows have no .desktop, so
# the flag has to be added to the launch command yourself (see the README).
#
# Usage: scripts/hide-debug-banner.sh [install|remove|status] [brave|chrome|chromium ...]
#   With no browser named, every one found is handled.
#
# AXSCOPE_DESKTOP_DIRS overrides where the system .desktop files are searched
# (colon-separated), for a browser installed outside the usual prefix.

set -euo pipefail

FLAG="--silent-debugger-extension-api"
APP_DIR="$HOME/.local/share/applications"
DESKTOP_DIRS="${AXSCOPE_DESKTOP_DIRS:-/usr/share/applications:/var/lib/flatpak/exports/share/applications}"

usage() { echo "usage: $0 install|remove|status [brave|chrome|chromium ...]" >&2; }

# names echoes the .desktop basenames that identify a browser family.
names() {
  case "$1" in
    brave)    echo brave-browser.desktop brave.desktop com.brave.Browser.desktop ;;
    chrome)   echo google-chrome.desktop google-chrome-stable.desktop com.google.Chrome.desktop ;;
    chromium) echo chromium.desktop chromium-browser.desktop org.chromium.Chromium.desktop ;;
  esac
}

# resolve echoes the first existing .desktop of a family.
resolve() {
  local name dir
  for name in $(names "$1"); do
    for dir in ${DESKTOP_DIRS//:/ }; do
      if [ -f "$dir/$name" ]; then
        echo "$dir/$name"
        return 0
      fi
    done
  done
  return 1
}

# dest_for is where the user override for a basename lives. The override is
# keyed by the basename only, which is why callers iterate `names`, not paths.
dest_for() { echo "$APP_DIR/$1"; }

# add_flag appends FLAG to every Exec line that does not have it yet. Appending
# (instead of inserting after the executable) keeps `Exec=env VAR=... browser`
# wrappers, used by snap packages, correct.
add_flag() {
  sed -E "/^Exec=/{/$FLAG/b; s#[[:space:]]*\$# $FLAG#}"
}

do_install() {
  local family src dest
  for family in "${FAMILIES[@]}"; do
    if ! src="$(resolve "$family")"; then
      echo "$family: no .desktop found, skipped"
      continue
    fi
    dest="$(dest_for "$(basename "$src")")"
    mkdir -p "$APP_DIR"
    # Temp + move: a sed failure must not leave a half-written override in place.
    add_flag < "$src" > "$dest.tmp"
    mv "$dest.tmp" "$dest"
    echo "$family: override created ($dest)"
    grep -E '^Exec=' "$dest" | head -3 | sed 's/^/  /'
  done
  echo
  echo "Fully close the browser and open it again. After that the debugging banner"
  echo "no longer appears."
}

do_remove() {
  local family name dest
  for family in "${FAMILIES[@]}"; do
    for name in $(names "$family"); do
      dest="$(dest_for "$name")"
      if [ -f "$dest" ]; then
        rm -f "$dest"
        echo "$family: override removed ($dest)"
      fi
    done
  done
}

do_status() {
  local family name dest shown
  for family in "${FAMILIES[@]}"; do
    shown=0
    for name in $(names "$family"); do
      dest="$(dest_for "$name")"
      if [ -f "$dest" ]; then
        echo "$family: installed ($dest)"
        grep -E '^Exec=' "$dest" | head -3 | sed 's/^/  /'
        shown=1
      fi
    done
    [ "$shown" -eq 0 ] && echo "$family: not installed"
  done
  return 0
}

cmd="${1:-status}"
if [ "$#" -gt 0 ]; then shift; fi
case "$cmd" in
  install|remove|status) ;;
  *) usage; exit 2 ;;
esac

FAMILIES=()
for arg in "$@"; do
  case "$arg" in
    brave|chrome|chromium) FAMILIES+=("$arg") ;;
    *) echo "unknown browser: $arg (use brave, chrome or chromium)" >&2; exit 2 ;;
  esac
done
if [ "${#FAMILIES[@]}" -eq 0 ]; then
  FAMILIES=(brave chrome chromium)
fi

case "$cmd" in
  install) do_install ;;
  remove)  do_remove ;;
  status)  do_status ;;
esac
