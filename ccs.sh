# ccs - Claude Code Sessions
# Source this file in your .zshrc / .bashrc:
#   source ~/dev/ccs/ccs.sh

ccs() {
  local bin="${CCS_BIN:-$(dirname "${BASH_SOURCE[0]:-${(%):-%x}}")/ccs}"

  if [[ "$*" == *"-l"* || "$*" == *"--list"* || "$*" == *"-h"* || "$*" == *"--help"* || "$*" == *"-v"* || "$*" == *"--version"* ]]; then
    "$bin" "$@"
    return
  fi

  # Styled output goes to stderr (user sees it).
  # Raw "path\nsessionID" comes back on stdout for us to parse.
  local output
  output=$("$bin" "$@") || return

  local dir session_id
  dir=$(echo "$output" | head -1)
  session_id=$(echo "$output" | tail -1)

  if [[ -z "$dir" || -z "$session_id" ]]; then
    return
  fi

  cd "$dir" || return
}
