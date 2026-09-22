#!/bin/bash
set -euo pipefail

# 開発ビルドの VERSION の推定を使わず、CI がリリースブランチから決めたタグだけを受け取る。
release_version=${RELEASE_VERSION:-}
[[ "$release_version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  echo 'release: RELEASE_VERSION must be vX.Y.Z' >&2
  exit 1
}
go_command=${GO:-go}
release_dir=${RELEASE_DIR:-artifacts/release}
script_directory=$(cd "$(dirname "$0")" && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/hhx-release.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 配布用ビルドは BuildMeta を空にし、`hhx version` に -dev を付けない。
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$go_command" build -trimpath \
  -ldflags "-s -w -X github.com/HappyOnigiri/hhx/internal/version.Version=$release_version -X github.com/HappyOnigiri/hhx/internal/version.BuildMeta=" \
  -o "$scratch/hhx-darwin-arm64" ./cmd/hhx
sed "s/@HHX_RELEASE_VERSION@/$release_version/g" "$script_directory/install.sh" > "$scratch/install.sh"
# uninstall.sh は何もダウンロードしないので、タグを埋め込まずにそのまま配る。
cp "$script_directory/uninstall.sh" "$scratch/uninstall.sh"
(cd "$scratch" && shasum -a 256 hhx-darwin-arm64 > checksums.txt)
mkdir -p "$release_dir"
# publish-release.yml はこの 4 つのパスを固定で添付する。名前を変えると添付が黙って壊れる。
cp "$scratch/hhx-darwin-arm64" "$scratch/install.sh" "$scratch/uninstall.sh" "$scratch/checksums.txt" "$release_dir/"
