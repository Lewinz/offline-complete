package editstate

// ZLEHooks is an opt-in zsh integration fragment. A trusted caller must define
// _offline_complete_zle_event before enabling it. The callback receives an
// event (begin, update, end), buffer, character cursor, multibyte flag and
// current working directory; begin and end carry an empty buffer and cursor. It must return promptly, must not write
// terminal input, and must not persist or log the buffer. The fragment neither
// connects to IPC nor installs itself in the user's shell configuration.
//
// ZLE hooks execute synchronously: a blocking callback blocks the shell. The
// owner must arrange a bounded, nonblocking transport and invalidate state on
// transport failure before using these hooks in a real session. The update hook
// suppresses zsh xtrace locally while passing the buffer; the callback and other
// shell facilities remain responsible for not exposing it.
const ZLEHooks = `
function _offline_complete_zle_begin {
  typeset -g _offline_complete_zle_active=1
  local mb=0
  [[ -o multibyte ]] && mb=1
  _offline_complete_zle_event begin '' '' "$mb" "$PWD" || :
  return 0
}
function _offline_complete_zle_update {
  setopt localoptions noxtrace
  [[ ${_offline_complete_zle_active:-0} == 1 ]] || return 0
  local mb=0
  [[ -o multibyte ]] && mb=1
  _offline_complete_zle_event update "$BUFFER" "$CURSOR" "$mb" "$PWD" || :
  return 0
}
function _offline_complete_zle_end {
  [[ ${_offline_complete_zle_active:-0} == 1 ]] || return 0
  typeset -g _offline_complete_zle_active=0
  local mb=0
  [[ -o multibyte ]] && mb=1
  _offline_complete_zle_event end '' '' "$mb" "$PWD" || :
  return 0
}
function _offline_complete_zle_enable {
  (( $+functions[_offline_complete_zle_event] )) || return 1
  zmodload zsh/zle || return 1
  autoload -Uz add-zle-hook-widget
  add-zle-hook-widget line-init _offline_complete_zle_begin || return 1
  if ! add-zle-hook-widget line-pre-redraw _offline_complete_zle_update; then
    add-zle-hook-widget -d line-init _offline_complete_zle_begin
    return 1
  fi
  if ! add-zle-hook-widget line-finish _offline_complete_zle_end; then
    add-zle-hook-widget -d line-pre-redraw _offline_complete_zle_update
    add-zle-hook-widget -d line-init _offline_complete_zle_begin
    return 1
  fi
}
function _offline_complete_zle_disable {
  if [[ ${_offline_complete_zle_active:-0} == 1 ]]; then
    _offline_complete_zle_end
  fi
  zmodload zsh/zle || return 1
  autoload -Uz add-zle-hook-widget
  add-zle-hook-widget -d line-init _offline_complete_zle_begin
  add-zle-hook-widget -d line-pre-redraw _offline_complete_zle_update
  add-zle-hook-widget -d line-finish _offline_complete_zle_end
}
`
