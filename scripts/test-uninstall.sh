#!/bin/bash
# uninstall.sh を一時 HOME と偽の hhx で検査する。実機の agent の設定にも ~/.local/bin にも触れない。
set -euo pipefail

root=$(mktemp -d "${TMPDIR:-/tmp}/hhx-uninstall-test.XXXXXX")
trap 'rm -rf "$root"' EXIT
script_directory=$(cd "$(dirname "$0")" && pwd)
uninstaller="$script_directory/uninstall.sh"

# 偽の hhx は呼ばれた引数を記録し、HHX_UNINSTALL_TEST_FAIL=1 なら失敗する。
fake_hhx="$root/fake-hhx"
cat > "$fake_hhx" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "$HHX_UNINSTALL_TEST_CALLS"
[ "${HHX_UNINSTALL_TEST_FAIL:-0}" != 1 ] || { echo 'hhx uninstall: simulated failure' >&2; exit 1; }
EOF
chmod 0755 "$fake_hhx"

failures=0
check() {
  local name=$1 description=$2
  shift 2
  if ! "$@"; then
    echo "FAIL [$name]: $description" >&2
    failures=$((failures + 1))
  fi
}
contains() { grep -Fq -- "$2" "$1"; }

# setup_home NAME LAYOUT。LAYOUT は binary（通常のファイル）・symlink・none のどれか。
setup_home() {
  local name=$1 layout=$2
  case_home="$root/$name/home"
  case_output="$root/$name/output"
  case_calls="$root/$name/calls"
  mkdir -p "$case_home/.local/bin"
  : > "$case_calls"
  case "$layout" in
    binary) cp "$fake_hhx" "$case_home/.local/bin/hhx" ;;
    symlink)
      mkdir -p "$case_home/opt"
      cp "$fake_hhx" "$case_home/opt/hhx"
      ln -s "$case_home/opt/hhx" "$case_home/.local/bin/hhx"
      ;;
    none) ;;
  esac
}

# run_uninstaller [ENV=VALUE...] -- [ARG...]。PATH には標準のコマンドだけを置き、手元の hhx を拾わない。
run_uninstaller() {
  local environment=()
  while [ "$#" -gt 0 ] && [ "$1" != -- ]; do
    environment+=("$1")
    shift
  done
  [ "$#" -eq 0 ] || shift
  case_status=0
  env HOME="$case_home" PATH=/usr/bin:/bin HHX_UNINSTALL_TEST_CALLS="$case_calls" ${environment[@]+"${environment[@]}"} \
    bash "$uninstaller" "$@" > "$case_output" 2>&1 < /dev/null || case_status=$?
}

succeeded() { [ "$case_status" -eq 0 ]; }
failed() { [ "$case_status" -ne 0 ]; }
called_uninstall() { [ "$(cat "$case_calls")" = uninstall ]; }
not_called() { [ ! -s "$case_calls" ]; }

setup_home standard binary
mkdir -p "$case_home/.claude"
run_uninstaller -- --yes
check standard 'the uninstaller succeeds' succeeded
check standard 'hhx uninstall removes the hook entries' called_uninstall
check standard 'the binary is removed' test ! -e "$case_home/.local/bin/hhx"
check standard 'the configuration is reported as kept' contains "$case_output" "$case_home/.config/hhx/"
check standard 'the cache is reported as kept' contains "$case_output" "$case_home/.cache/hhx/"
check standard 'the snapshot refs are explained' contains "$case_output" 'git for-each-ref refs/hhx/'

setup_home uninstall-failure binary
mkdir -p "$case_home/.codex"
run_uninstaller HHX_UNINSTALL_TEST_FAIL=1 -- --yes
check uninstall-failure 'the uninstaller fails' failed
check uninstall-failure 'the binary is kept so the hooks still resolve' test -x "$case_home/.local/bin/hhx"
check uninstall-failure 'the failure is explained' contains "$case_output" 'the binary was left in place'

setup_home no-agents binary
run_uninstaller -- --yes
check no-agents 'the uninstaller succeeds' succeeded
check no-agents 'hhx uninstall is not run without agent directories' not_called
check no-agents 'the binary is removed' test ! -e "$case_home/.local/bin/hhx"

setup_home symlink symlink
mkdir -p "$case_home/.claude"
run_uninstaller -- --yes
check symlink 'the uninstaller succeeds' succeeded
check symlink 'the symlink is removed' test ! -L "$case_home/.local/bin/hhx"
check symlink 'the symlink target is kept' test -x "$case_home/opt/hhx"

setup_home outside none
mkdir -p "$case_home/.claude" "$case_home/elsewhere"
cp "$fake_hhx" "$case_home/elsewhere/hhx"
run_uninstaller PATH="$case_home/elsewhere:/usr/bin:/bin" -- --yes
check outside 'the uninstaller succeeds' succeeded
check outside 'hhx on PATH removes the hook entries' called_uninstall
check outside 'the binary outside the standard path is kept' test -x "$case_home/elsewhere/hhx"
check outside 'keeping it is reported' contains "$case_output" 'outside the standard installation path'

setup_home missing none
run_uninstaller -- --yes
check missing 'the uninstaller fails without hhx' failed
check missing 'the failure is explained' contains "$case_output" 'hhx was not found'

setup_home bad-option binary
run_uninstaller -- --force
check bad-option 'an unknown option is rejected' failed
check bad-option 'nothing is removed' test -x "$case_home/.local/bin/hhx"
check bad-option 'nothing is run' not_called

setup_home help binary
run_uninstaller -- --help
check help 'help succeeds' succeeded
check help 'help shows the usage' contains "$case_output" 'Usage: uninstall.sh'
check help 'help changes nothing' test -x "$case_home/.local/bin/hhx"

# 端末が無いときは確認できないので、何も変えずに断る。端末のある手元では read が入力を待つので飛ばす。
if ! { : < /dev/tty; } 2>/dev/null; then
  setup_home no-terminal binary
  mkdir -p "$case_home/.claude"
  run_uninstaller --
  check no-terminal 'the uninstaller refuses without confirmation' failed
  check no-terminal 'the refusal suggests --yes' contains "$case_output" 'rerun with --yes'
  check no-terminal 'nothing is removed' test -x "$case_home/.local/bin/hhx"
  check no-terminal 'nothing is run' not_called
fi

if [ "$failures" -ne 0 ]; then
  echo "uninstall-test: $failures check(s) failed" >&2
  exit 1
fi
echo 'uninstall-test: all checks passed'
