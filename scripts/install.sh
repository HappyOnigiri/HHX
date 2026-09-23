#!/bin/bash
set -euo pipefail

# Release CI がこの値を埋め込み、インストーラーとバイナリを同じタグへ固定する。
release_version='@HHX_RELEASE_VERSION@'

fail() {
  echo "hhx install: $*" >&2
  exit 1
}

# curl | bash の途中切断では配置を始めないよう、全体を読み込んでから最後に呼ぶ。
# hook の登録（hhx install）はここでは行わない。更新では登録済みの絶対パスがそのまま使えるので何もしない。
main() {
  [[ "$release_version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    fail "use install.sh from a GitHub Release"
  [ "$(uname -s)" = Darwin ] && [ "$(uname -m)" = arm64 ] ||
    fail "macOS arm64 is required; use a native Apple Silicon terminal"
  [[ "${HOME:-}" = /* ]] || fail "HOME must be an absolute path"
  for tool in curl shasum mktemp install mv; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
  done

  local install_dir="$HOME/.local/bin" destination="$HOME/.local/bin/hhx"
  local asset=hhx-darwin-arm64
  local base_url="https://github.com/HappyOnigiri/HHX/releases/download/$release_version"
  local checksum checksum_name actual initial_install=false
  [ ! -d "$destination" ] || fail "$destination is a directory"
  if [ ! -e "$destination" ]; then
    initial_install=true
  fi

  scratch=$(mktemp -d "${TMPDIR:-/tmp}/hhx-install.XXXXXX")
  staged=''
  trap 'rm -rf "$scratch"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  echo "Downloading hhx $release_version..."
  curl --fail --silent --show-error --location "$base_url/$asset" --output "$scratch/$asset" ||
    fail "download failed; the installed binary was not changed"
  curl --fail --silent --show-error --location "$base_url/checksums.txt" --output "$scratch/checksums.txt" ||
    fail "checksum download failed; the installed binary was not changed"
  read -r checksum checksum_name < "$scratch/checksums.txt" || fail "invalid checksums.txt"
  [[ "$checksum" =~ ^[0-9a-fA-F]{64}$ ]] && [ "$checksum_name" = "$asset" ] ||
    fail "invalid checksum entry for $asset"
  # 検証する名前を固定し、checksums.txt の別の行や相対パスを shasum に渡さない。
  (cd "$scratch" && printf '%s  %s\n' "$checksum" "$asset" | shasum -a 256 -c - >/dev/null) ||
    fail "checksum verification failed; the installed binary was not changed"
  chmod 0755 "$scratch/$asset"
  actual=$("$scratch/$asset" version) || fail "the downloaded binary could not run; the installed binary was not changed"
  [ "$actual" = "hhx version $release_version" ] ||
    fail "unexpected binary version: $actual; the installed binary was not changed"

  # 同じファイルシステムの上で組み立ててから置き換え、途中で失敗しても既存のバイナリを壊さない。
  install -d "$install_dir"
  staged=$(mktemp "$install_dir/.hhx-install.XXXXXX")
  install -m 0755 "$scratch/$asset" "$staged"
  mv -f "$staged" "$destination"
  staged=''
  # hhx update --apply はこの行で置き換えを確かめる。文言を変えるときは internal/update も合わせる。
  echo "Installed hhx $release_version to $destination"

  local path_configured=false
  case ":${PATH:-}:" in
    *":$install_dir:"*) path_configured=true ;;
  esac
  if [ "$path_configured" = false ]; then
    echo 'To use hhx in this terminal, run:'
    # 利用者が実行するコマンドを展開せずに表示する。
    # shellcheck disable=SC2016
    echo '  export PATH="$HOME/.local/bin:$PATH"'
    echo 'Add that line to your shell configuration (for example, ~/.zshrc) for new terminals.'
  fi
  if [ "$initial_install" = true ]; then
    echo 'To register the hooks in Claude Code and Codex, run:'
    echo '  hhx install'
  fi
}

main "$@"
