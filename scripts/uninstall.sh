#!/bin/bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: uninstall.sh [--help] [-y|--yes]

Remove the hhx hook entries from the Claude Code and Codex settings (hhx uninstall),
then remove the binary installed at ~/.local/bin/hhx.
Configuration, caches, and settings backups are kept.

Use --yes when running without an interactive terminal.
EOF
}

fail() {
  echo "hhx uninstall: $*" >&2
  exit 1
}

print_retained_data() {
  local home=$1
  echo ''
  echo 'hhx will keep these files:'
  printf '  %q  (configuration)\n' "$home/.config/hhx/"
  printf '  %q  (caches)\n' "$home/.cache/hhx/"
  printf '  %q  (backups of agent settings that hhx rewrote)\n' "$home/.local/state/hhx/"
}

# 削除そのものは hhx uninstall に委ね、このスクリプトは順序だけを持つ。
# 登録を消す前にバイナリを消すと、agent が存在しないコマンドを hook として呼び続ける。
# curl | bash の途中切断では削除を始めないよう、全体を読み込んでから最後に呼ぶ。
main() {
  local assume_yes=false
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --help)
        usage
        return 0
        ;;
      -y | --yes) assume_yes=true ;;
      *) fail "unknown option $1; the only options are --help and --yes" ;;
    esac
    shift
  done
  [[ "${HOME:-}" = /* ]] || fail "HOME must be an absolute path"

  local home=$HOME installed="$HOME/.local/bin/hhx" hhx
  if [ -d "$installed" ] && [ ! -L "$installed" ]; then
    fail "$installed is a directory; it was not changed"
  fi
  if [ -x "$installed" ] && [ ! -d "$installed" ]; then
    hhx=$installed
  else
    hhx=$(command -v hhx 2>/dev/null || true)
    [ -n "$hhx" ] && [ -x "$hhx" ] ||
      fail "hhx was not found on PATH or at $installed; remove the hhx hook entries by hand"
  fi
  local agents=false
  if [ -d "$home/.claude" ] || [ -d "$home/.codex" ]; then
    agents=true
  fi

  echo 'hhx uninstall will:'
  if [ "$agents" = true ]; then
    printf '  remove the hhx hook entries with %q uninstall\n' "$hhx"
  else
    echo '  skip the hook entries because neither ~/.claude nor ~/.codex exists'
  fi
  if [ -L "$installed" ]; then
    printf '  remove the symlink %q (its target will be kept)\n' "$installed"
  elif [ -f "$installed" ] && [ -x "$installed" ]; then
    printf '  remove the installed binary %q\n' "$installed"
  elif [ -e "$installed" ]; then
    printf '  leave the non-executable file %q in place\n' "$installed"
  else
    echo '  leave the binary in place because it is not at the standard path'
  fi
  print_retained_data "$home"

  if [ "$assume_yes" != true ]; then
    local answer=''
    echo ''
    printf 'Continue? [y/N] '
    [ -r /dev/tty ] || fail 'no terminal to confirm on; rerun with --yes to skip the question'
    read -r answer < /dev/tty || answer=''
    case "$answer" in
      y | Y | yes | YES) ;;
      *) fail 'cancelled; nothing was changed' ;;
    esac
  fi

  if [ "$agents" = true ]; then
    "$hhx" uninstall || fail 'hhx uninstall failed; the binary was left in place; fix the error above and rerun this script'
  fi
  if [ -L "$installed" ] || { [ -f "$installed" ] && [ -x "$installed" ]; }; then
    rm -f "$installed" || fail "could not remove $installed"
    printf 'Removed %q\n' "$installed"
  fi
  if [ "$hhx" != "$installed" ]; then
    printf 'Kept %q because it is outside the standard installation path\n' "$hhx"
  fi

  echo ''
  echo 'To remove the retained files as well, run:'
  printf '  rm -rf %q %q %q\n' "$home/.config/hhx/" "$home/.cache/hhx/" "$home/.local/state/hhx/"
  echo 'Repositories where hhx took snapshots before discarding changes may still hold refs under refs/hhx/.'
  echo 'To list and then delete them, run these from inside such a repository:'
  echo '  git for-each-ref refs/hhx/'
  echo "  git for-each-ref --format='delete %(refname)' refs/hhx/ | git update-ref --stdin"
}

main "$@"
