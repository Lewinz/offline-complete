package editstate

// CommandStatusHooks is an opt-in zsh fragment. The caller supplies a prompt,
// nonblocking _offline_complete_command_status callback accepting one exit
// status. It does not transmit command text or install shell configuration.
// These hooks alone do not reliably associate a status with an editing cycle;
// they must not be used as a learning signal without such an association.
const CommandStatusHooks = `
function _offline_complete_status_preexec {
  typeset -g _offline_complete_status_pending=1
  return 0
}
function _offline_complete_status_precmd {
  local rc=$?
  [[ ${_offline_complete_status_pending:-0} == 1 ]] || return 0
  typeset -g _offline_complete_status_pending=0
  _offline_complete_command_status "$rc" || :
  return 0
}
function _offline_complete_status_enable {
  (( $+functions[_offline_complete_command_status] )) || return 1
  autoload -Uz add-zsh-hook
  add-zsh-hook preexec _offline_complete_status_preexec || return 1
  if ! add-zsh-hook precmd _offline_complete_status_precmd; then
    add-zsh-hook -d preexec _offline_complete_status_preexec
    return 1
  fi
}
function _offline_complete_status_disable {
  typeset -g _offline_complete_status_pending=0
  autoload -Uz add-zsh-hook
  add-zsh-hook -d preexec _offline_complete_status_preexec
  add-zsh-hook -d precmd _offline_complete_status_precmd
}
`
