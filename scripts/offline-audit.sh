#!/usr/bin/env bash
# Best-effort offline smoke check. This is not a substitute for a clean VM with
# system-level network observation; see docs/RELEASE.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin"
PROFILE="$(mktemp -t offline-complete-sandbox.XXXXXX)"
trap 'rm -f "$PROFILE"' EXIT

if ! command -v sandbox-exec >/dev/null 2>&1; then
  echo "offline-audit: sandbox-exec unavailable; use a clean VM procedure" >&2
  exit 1
fi
if [ ! -x "$BIN/offline-complete" ] || [ ! -x "$BIN/oc-manage" ]; then
  echo "offline-audit: run 'make bin' first" >&2
  exit 1
fi

cat > "$PROFILE" <<'PROFILE_EOF'
(version 1)
(allow default)
(deny network*)
PROFILE_EOF

echo "== plain proxy under denied network =="
sandbox-exec -f "$PROFILE" "$BIN/offline-complete" /bin/sh -c 'printf ok'
echo

echo "== management status under denied network =="
OFFLINE_COMPLETE_RULES="$ROOT/rules/base.json" \
  sandbox-exec -f "$PROFILE" "$BIN/oc-manage" status

echo
echo "Note: the agent's Unix socket may be rejected by 'deny network*'."
echo "A full agent run needs a profile that allows AF_UNIX but denies IP traffic,"
echo "or a clean-VM network monitor. Document the exact profile used."
