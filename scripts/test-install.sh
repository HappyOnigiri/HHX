#!/bin/bash
# install.sh を一時 HOME と偽の curl・uname で検査する。ネットワークにも実機の ~/.local/bin にも触れない。
set -euo pipefail

root=$(mktemp -d "${TMPDIR:-/tmp}/hhx-install-test.XXXXXX")
trap 'rm -rf "$root"' EXIT
script_directory=$(cd "$(dirname "$0")" && pwd)

# 配布するバイナリの代わり。`version` に答えるだけの shell script で、名乗る版は環境変数で変える。
new_asset="$root/new-hhx"
cat > "$new_asset" <<'EOF'
#!/bin/bash
[ "${1-}" = version ] || exit 1
echo "hhx version ${HHX_INSTALL_TEST_REPORTED_VERSION:-v0.0.0}"
EOF
chmod 0755 "$new_asset"
old_binary_content='#!/bin/bash
echo "hhx version v0.0.0-old"'

tools="$root/tools"
mkdir -p "$tools"
cat > "$tools/uname" <<'EOF'
#!/bin/bash
case "${1-}" in
  -s) echo "${HHX_INSTALL_TEST_OS:-Darwin}" ;;
  -m) echo arm64 ;;
  *) exit 1 ;;
esac
EOF
# 偽の curl は URL の末尾で資産を選び、HHX_INSTALL_TEST_MODE で失敗の種類を作る。
cat > "$tools/curl" <<'EOF'
#!/bin/bash
set -euo pipefail
output='' url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
printf '%s\n' "$url" >> "$HHX_INSTALL_TEST_URLS"
case "$url" in
  https://github.com/HappyOnigiri/HHX/releases/download/v0.0.0/hhx-darwin-arm64)
    [ "${HHX_INSTALL_TEST_MODE:-}" != download-failure ] || exit 22
    cp "$HHX_INSTALL_TEST_ASSET" "$output"
    ;;
  https://github.com/HappyOnigiri/HHX/releases/download/v0.0.0/checksums.txt)
    if [ "${HHX_INSTALL_TEST_MODE:-}" = bad-checksum ]; then
      printf '%064d  hhx-darwin-arm64\n' 0 > "$output"
    else
      checksum=$(shasum -a 256 "$HHX_INSTALL_TEST_ASSET")
      printf '%s  hhx-darwin-arm64\n' "${checksum%% *}" > "$output"
    fi
    ;;
  *) exit 22 ;;
esac
EOF
chmod 0755 "$tools/uname" "$tools/curl"

installer="$root/install.sh"
sed "s/@HHX_RELEASE_VERSION@/v0.0.0/g" "$script_directory/install.sh" > "$installer"

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
lacks() { ! grep -Fq -- "$2" "$1"; }

# run_case NAME EXISTING(true|false) PATH_CONFIGURED(true|false) [ENV=VALUE...]
# 結果を $case_home・$case_output・$case_status に残す。
run_case() {
  local name=$1 existing=$2 path_configured=$3
  shift 3
  case_home="$root/$name/home"
  case_output="$root/$name/output"
  mkdir -p "$case_home"
  if [ "$existing" = true ]; then
    mkdir -p "$case_home/.local/bin"
    printf '%s\n' "$old_binary_content" > "$case_home/.local/bin/hhx"
    chmod 0755 "$case_home/.local/bin/hhx"
  fi
  local test_path="$tools:$PATH"
  if [ "$path_configured" = true ]; then
    test_path="$case_home/.local/bin:$test_path"
  fi
  : > "$root/$name/urls"
  case_status=0
  env HOME="$case_home" PATH="$test_path" TMPDIR="$root/$name" \
    HHX_INSTALL_TEST_ASSET="$new_asset" HHX_INSTALL_TEST_URLS="$root/$name/urls" "$@" \
    bash "$installer" > "$case_output" 2>&1 || case_status=$?
}

installed_is_new() { cmp -s "$new_asset" "$case_home/.local/bin/hhx"; }
installed_is_old() { [ "$(cat "$case_home/.local/bin/hhx")" = "$old_binary_content" ]; }
no_staged_files() { [ -z "$(find "$case_home/.local/bin" -name '.hhx-install.*' 2>/dev/null)" ]; }
succeeded() { [ "$case_status" -eq 0 ]; }
failed() { [ "$case_status" -ne 0 ]; }

run_case initial false false
check initial 'the installer succeeds' succeeded
check initial 'the binary is installed' installed_is_new
check initial 'the replacement is reported' contains "$case_output" "Installed hhx v0.0.0 to $case_home/.local/bin/hhx"
# shellcheck disable=SC2016 # 表示される文字列そのものと比べる。
check initial 'the PATH hint is shown' contains "$case_output" 'export PATH="$HOME/.local/bin:$PATH"'
check initial 'hook registration is left to hhx install' contains "$case_output" '  hhx install'
check initial 'the assets of the embedded tag are fetched' contains "$root/initial/urls" '/download/v0.0.0/hhx-darwin-arm64'

run_case update true true
check update 'the installer succeeds' succeeded
check update 'the binary is replaced' installed_is_new
check update 'no PATH hint when PATH is configured' lacks "$case_output" 'export PATH='
check update 'no registration hint on update' lacks "$case_output" 'hhx install'
check update 'no staged file is left' no_staged_files

run_case bad-checksum true false HHX_INSTALL_TEST_MODE=bad-checksum
check bad-checksum 'the installer fails' failed
check bad-checksum 'the existing binary is kept' installed_is_old
check bad-checksum 'the failure is explained' contains "$case_output" 'checksum verification failed'

run_case wrong-version true false HHX_INSTALL_TEST_REPORTED_VERSION=v9.9.9
check wrong-version 'the installer fails' failed
check wrong-version 'the existing binary is kept' installed_is_old
check wrong-version 'the failure is explained' contains "$case_output" 'unexpected binary version'

run_case download-failure true false HHX_INSTALL_TEST_MODE=download-failure
check download-failure 'the installer fails' failed
check download-failure 'the existing binary is kept' installed_is_old
check download-failure 'no staged file is left' no_staged_files

run_case not-macos false false HHX_INSTALL_TEST_OS=Linux
check not-macos 'the installer fails' failed
check not-macos 'nothing is installed' test ! -e "$case_home/.local/bin/hhx"
check not-macos 'nothing is downloaded' test ! -s "$root/not-macos/urls"

# タグを埋め込んでいない（リポジトリの）install.sh は、何も取得せずに断る。
unrendered_home="$root/unrendered/home"
mkdir -p "$unrendered_home"
unrendered_status=0
HOME="$unrendered_home" PATH="$tools:$PATH" HHX_INSTALL_TEST_URLS="$root/unrendered-urls" \
  bash "$script_directory/install.sh" > "$root/unrendered-output" 2>&1 || unrendered_status=$?
check unrendered 'the source installer refuses to run' test "$unrendered_status" -ne 0
check unrendered 'the refusal is explained' contains "$root/unrendered-output" 'use install.sh from a GitHub Release'

if [ "$failures" -ne 0 ]; then
  echo "install-test: $failures check(s) failed" >&2
  exit 1
fi
echo 'install-test: all checks passed'
