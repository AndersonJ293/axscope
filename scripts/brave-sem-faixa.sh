#!/usr/bin/env bash
# Liga/desliga o modo "sem faixa de depuração" no Brave.
#
# Por que existe: a extensão usa chrome.debugger, e o Chromium mostra uma faixa
# "«Extensão» started debugging this browser" em todas as abas. A flag
# --silent-debugger-extension-api esconde a faixa; ela precisa estar no comando
# de abertura do Brave.
#
# Fazemos isso com um override de USUÁRIO em ~/.local/share/applications, que
# tem precedência sobre o .desktop do sistema — sem sudo e fácil de reverter.
#
# Uso: scripts/brave-sem-faixa.sh instalar | remover | status

set -euo pipefail

FLAG="--silent-debugger-extension-api"
DEST="$HOME/.local/share/applications/brave-browser.desktop"
SOURCE=""
for candidate in /usr/share/applications/brave-browser.desktop \
                 /usr/share/applications/brave.desktop \
                 /var/lib/flatpak/exports/share/applications/com.brave.Browser.desktop; do
  [ -f "$candidate" ] && SOURCE="$candidate" && break
done

instalar() {
  if [ -z "$SOURCE" ]; then
    echo "não achei o .desktop do Brave; ajuste o Exec manualmente e acrescente $FLAG" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$DEST")"
  # Copia o .desktop do sistema trocando "Exec=brave " por "Exec=brave FLAG ".
  sed -E "s#^(Exec=(/[^ ]*/)?brave(-browser)?) #\1 $FLAG #" "$SOURCE" > "$DEST"
  echo "overrides criado: $DEST"
  grep -E '^Exec=' "$DEST" | head -3
  echo
  echo "Feche o Brave por completo e abra de novo pelo menu. Depois disso a faixa"
  echo "de depuração não aparece mais."
}

remover() {
  rm -f "$DEST"
  echo "override removido: $DEST"
}

status() {
  if [ -f "$DEST" ]; then
    echo "instalado ($DEST)"
    grep -E '^Exec=' "$DEST" | head -3
  else
    echo "não instalado"
  fi
}

case "${1:-status}" in
  instalar|install) instalar ;;
  remover|remove)   remover ;;
  status)           status ;;
  *) echo "uso: $0 instalar|remover|status" >&2; exit 2 ;;
esac
