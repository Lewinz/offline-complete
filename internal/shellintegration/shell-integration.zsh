# offline-complete shell integration.
# Installed by oc-manage shell install; edits are overwritten on upgrade.
if [[ -o interactive && -z "$ZSH_EXECUTION_STRING" && $EUID -ne 0 && -z "$SSH_CONNECTION" && -z "$OFFLINE_COMPLETE_ACTIVE" ]]; then
  if [[ -d "$HOME/.local/bin" && ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    export PATH="$HOME/.local/bin:$PATH"
  fi
  oc_app_pid="$(cat "$HOME/Library/Application Support/offline-complete/app.pid" 2>/dev/null)"
  if [[ -n "$oc_app_pid" ]] && kill -0 "$oc_app_pid" 2>/dev/null; then
    if [[ -x @OC@ ]]; then
      export OFFLINE_COMPLETE_QUIET=1
      export OFFLINE_COMPLETE_AUTO=1
      exec @OC@
    fi
  fi
  unset oc_app_pid
fi
