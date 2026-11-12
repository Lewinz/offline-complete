#!/usr/bin/env bash
# Remove the app, launchers and marked zsh auto-start block created by
# scripts/install.sh. Kiro configuration is not touched.
set -euo pipefail

DEST="$HOME/Applications/Offline Complete.app"
LAUNCHER="$HOME/.local/bin/oc"

if [ -x "$HOME/.local/bin/oc-manage" ]; then
  "$HOME/.local/bin/oc-manage" shell uninstall >/dev/null 2>&1 || true
fi

for tool in oc oc-manage oc-window oc-client oc-app; do
  path="$HOME/.local/bin/$tool"
  if [ -f "$path" ] && grep -q 'offline-complete launcher' "$path"; then
    rm -f "$path"
    echo "removed launcher: $path"
  fi
done
if [ -d "$DEST" ]; then
  rm -rf "$DEST"
  echo "removed app: $DEST"
fi
echo "Removed the marked offline-complete zsh block; Kiro configuration was not touched."
