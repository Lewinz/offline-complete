#!/usr/bin/env bash
# Remove the system installer payload. User data (~/.oc and Application Support)
# is deliberately preserved.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "uninstall-pkg: run with sudo" >&2
  exit 1
fi

console_user="$(/usr/bin/stat -f %Su /dev/console 2>/dev/null || true)"
if [ -n "$console_user" ] && [ "$console_user" != "root" ] && [ -x /usr/local/bin/oc-manage ]; then
  console_home="$(/usr/bin/dscl . -read "/Users/$console_user" NFSHomeDirectory 2>/dev/null | /usr/bin/awk '{print $2}')"
  if [ -n "$console_home" ]; then
    /usr/bin/su -l "$console_user" -c "/usr/local/bin/oc-manage shell uninstall --home \"$console_home\"" >/dev/null 2>&1 || true
  fi
fi

for tool in oc oc-app oc-manage oc-window oc-client; do
  path="/usr/local/bin/$tool"
  if [ -f "$path" ] && grep -q 'offline-complete launcher' "$path"; then
    rm -f "$path"
    echo "removed: $path"
  fi
done

if [ -d "/Applications/Offline Complete.app" ]; then
  rm -rf "/Applications/Offline Complete.app"
  echo "removed: /Applications/Offline Complete.app"
fi

/usr/sbin/pkgutil --forget dev.offlinecomplete.pkg >/dev/null 2>&1 || true
echo "User data was not removed: ~/.oc and ~/Library/Application Support/offline-complete"
