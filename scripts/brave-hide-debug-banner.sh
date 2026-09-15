#!/usr/bin/env bash
# Turns the "hide debugging banner" mode on/off in Brave.
#
# Why it exists: the extension uses chrome.debugger, and Chromium shows a
# banner "«Extension» started debugging this browser" on every tab. The flag
# --silent-debugger-extension-api hides the banner; it must be in the Brave
# launch command.
#
# We do this with a USER override in ~/.local/share/applications, which
# takes precedence over the system .desktop — no sudo and easy to revert.
#
# Usage: scripts/brave-hide-debug-banner.sh install | remove | status

set -euo pipefail

FLAG="--silent-debugger-extension-api"
DEST="$HOME/.local/share/applications/brave-browser.desktop"
SOURCE=""
for candidate in /usr/share/applications/brave-browser.desktop \
                 /usr/share/applications/brave.desktop \
                 /var/lib/flatpak/exports/share/applications/com.brave.Browser.desktop; do
  [ -f "$candidate" ] && SOURCE="$candidate" && break
done

install() {
  if [ -z "$SOURCE" ]; then
    echo "could not find Brave's .desktop; adjust Exec manually and add $FLAG" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$DEST")"
  # Copies the system .desktop, replacing "Exec=brave " with "Exec=brave FLAG ".
  sed -E "s#^(Exec=(/[^ ]*/)?brave(-browser)?) #\1 $FLAG #" "$SOURCE" > "$DEST"
  echo "override created: $DEST"
  grep -E '^Exec=' "$DEST" | head -3
  echo
  echo "Close Brave completely and open it again from the menu. After that the"
  echo "debugging banner no longer appears."
}

remove() {
  rm -f "$DEST"
  echo "override removed: $DEST"
}

status() {
  if [ -f "$DEST" ]; then
    echo "installed ($DEST)"
    grep -E '^Exec=' "$DEST" | head -3
  else
    echo "not installed"
  fi
}

case "${1:-status}" in
  install) install ;;
  remove)  remove ;;
  status)  status ;;
  *) echo "usage: $0 install|remove|status" >&2; exit 2 ;;
esac
