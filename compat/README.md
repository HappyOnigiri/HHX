# 互換スイート

Python 実装の agent hook のテストを複製し、
同じ L1（契約）と L3（結合）のテストを `hhx hook <name>` に向けて流すための仕組み。
移行期間だけ置き、Python 実装からの切り替えが済んだら削除する。恒久的なテストは Go で書く。

## 実行

```sh
make compat-test
# Python 実装に向けて流す（複製が元の挙動を保っているかの確認）
HHX_COMPAT_TARGET=python HHX_COMPAT_PYTHON_HOOKS=<Python 本体のディレクトリ> \
  python3 -m unittest discover -s compat -t compat
```

| 環境変数 | 意味 |
| --- | --- |
| `HHX_COMPAT_TARGET` | `hhx`（既定）か `python` |
| `HHX_BIN` | 対象の hhx。既定は `bin/hhx` |
| `HHX_COMPAT_PYTHON_HOOKS` | `python` のときに起動する Python 本体のディレクトリ |

対象が `hhx` のとき、`helpers.py` は `HHX_CONFIG` を存在しないパスへ固定し、手元の `~/.config/hhx/config.yaml` を読ませない。

## 切り替えの仕組み

- テストがフックを起動する経路は `helpers.hook_command(script)` に集めてある。
  hhx では `hhx hook <name>` を、python では Python 本体を起動する。
- hook 名は Python のファイル名から拡張子を除いたもの。違うものは `helpers.HOOK_NAMES` に書く
  （`worktree-guard.py` のうち hhx へ移すのは snapshot の部分だけなので `discard-guard`）。
- hhx では、`helpers.PORTED_HOOKS` に無い hook のテストを skip する。
  L2（`load_hook` で Python 本体を import する単体テスト）は、本体に触れた時点で skip する。
- `test_compat_harness.py` は、hook が 1 本も無い状態でも hhx を L1 の経路で起動できることを確かめる。

## hook を移植したとき

1. `helpers.PORTED_HOOKS` に hook 名を足す。
2. `make compat-test` で、その hook の L1 と L3 が hhx に向けて全件通ることを確かめる。
3. 仕様を意図して変えたテスト（ref 名や保護対象の置き換えなど）は、ここで書き換える。

## 複製元からの変更

- ファイル先頭の配布元の注記を外し、実行方法の案内を `make compat-test` に置き換えた。
- 個人環境のパスを、架空のユーザー（`/Users/alice`）と `~/dotfiles` に置き換えた。
- `HOOKS_DIR / SCRIPT` を直接起動していた箇所を `hook_command(SCRIPT)` に、
  `fake_gh.py` のパスを `helpers.FAKE_GH` に置き換えた。
- `test_worktree_guard.py` の並行実行のテストで、スレッドに入る前に `hook_command` を呼ぶ
  （スレッドの中で起きた skip はテストに届かないため）。
- hhx に入れない hook のテスト（agent-worktree-guard、worktree-context）は複製していない。
