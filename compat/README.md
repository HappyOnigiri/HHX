# 互換スイート

Python 実装の agent hook のテストを複製し、
同じ L1（契約）と L3（結合）のテストを `hhx hook <name>` に向けて流すための仕組み。
`hhx wait-ci` も、Python 実装の `wait-ci` と同じシナリオで比べる（下の「wait-ci」）。
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
| `HHX_COMPAT_PYTHON_WAIT_CI` | Python 実装の `wait-ci`。渡すと wait-ci の差分テストが流れる。`python` では、渡さなければ wait-ci のテストを skip する |

対象が `hhx` で `HHX_COMPAT_PYTHON_HOOKS` を渡したときは、差分テスト（`test_differential.py`）も流れる。
注入系の hook（push-ci-context・pr-body-staleness・pr-context・agents-local-context）は、判定の代わりに注入の内容を比べる。
push-ci-context は待機コマンドの文面（`wait-ci` → `hhx wait-ci`）を置き換え、workflow run を絞る時刻を伏せてから比べる。
pr-body-staleness は origin を変形して、注入の内容と gh の呼び出し（owner・repo）を比べる（後読みを手書きの走査にしたため）。
agents-local-context は 1 件ごとに別の session_id で起動し、警告は種類（`:` の前）だけを比べる。
同じ入力を Python 本体（プロセス内で `main` を呼ぶ）と `hhx hook <name>` の両方に通し、判定と理由文
（発火したルールのラベルと、抽出したトークン・解決後のパス）が一致するかを比べる。
入力は既存のテストがフックに渡している入力と、それに境界の断片（区切り文字・クォート・`>`・`.pub`・`.env.<x>`・
Unicode の空白や語の文字など）を差し込んだ変形である。`HHX_DIFF_SEED` で乱数の種を、`HHX_DIFF_CASES` で変形の数を変えられる。
意図して仕様を変えた入力（irreversible-guard の G 類の旧保護対象と hhx 自身のパス）は比べない。
discard-guard は、Python 本体（worktree-guard.py）のブランチ attach の判定を無効にして比べ、理由文で意図して変えた 1 文は置き換えてから比べる。
snapshot の対象の特定で意図して変えた 2 点（`( ... )` の中の cd を閉じ括弧で取り消す、1 つの git の複数の `-C` を順に適用する）に
当たりうる入力も比べない。前者は、閉じ括弧で取り消した cwd に加え、括弧を無視して辿った cwd も保存する（どちらかが解決できなければ deny）。
Python 本体は別のリポジトリを保存して通すので、データを守ることを優先して直した（AGENTS.md の「hook の移植の型」）。
snapshot は両方が本物の git でサンドボックスのリポジトリに作るので、同じ一時 index の lock で競合しないよう hhx も 1 件ずつ起動する。

```sh
HHX_COMPAT_PYTHON_HOOKS=<Python 本体のディレクトリ> make compat-test
```

### wait-ci

`test_wait_ci.py` は、偽の gh（`fake_gh_scenario.py`）と本物の git で組んだシナリオを `hhx wait-ci` に流し、
終了コードと出力の形（1 行目の結論・最終行の `wait-ci: exit=...`・`--progress` が無ければ stderr が空）を確かめる。
`HHX_COMPAT_PYTHON_WAIT_CI` に Python 実装の `wait-ci` を渡すと、同じシナリオを両方に流して、終了コード・stdout・gh の呼び出しを比べる。
poll の回数が実時間で揺れないシナリオでは、進捗（stderr）も比べる。
移植元のテストはモジュールを import して内部関数を差し替える形なので、hhx に向けてそのまま流せず、この差分テストで代える
（ケースは Go のテストへ移した）。

```sh
HHX_COMPAT_PYTHON_WAIT_CI=<Python 実装の wait-ci> make compat-test
```

比べる前に次を正規化する。

- 結論行と `PR head:` 行の秒数。シナリオは `--interval 1` などの短い値で実時間を待つので揺れる。
- gh の出力が JSON として読めないときの理由。Python の json と Go の encoding/json でエラーの文言が違う。
- 引数の解析の失敗は stderr を比べない。argparse と pflag で文言が違う。どちらも stdout は空で終了コードは 2。

対象が `hhx` のとき、`helpers.py` は `HHX_CONFIG` を一時的な設定ファイルへ固定し、手元の `~/.config/hhx/config.yaml` を読ませない。
その設定ファイルは、既定で無効な hook（`helpers.DEFAULT_OFF_HOOKS`）だけを有効にする。

## 切り替えの仕組み

- テストがフックを起動する経路は `helpers.hook_command(script)` に集めてある。
  hhx では `hhx hook <name>` を、python では Python 本体を起動する。
- hook 名は Python のファイル名から拡張子を除いたもの。違うものは `helpers.HOOK_NAMES` に書く
  （`worktree-guard.py` のうち hhx へ移すのは snapshot の部分だけなので `discard-guard`）。
- hhx では、`helpers.PORTED_HOOKS` に無い hook のテストを skip する。
  L2（`load_hook` で Python 本体を import する単体テスト）は、本体に触れた時点で skip する。
- `test_compat_harness.py` は、hook が 1 本も無い状態でも hhx を L1 の経路で起動できることを確かめる。

## hook を移植したとき

1. `helpers.PORTED_HOOKS` に hook 名を足す。hhx で既定では無効な hook は `helpers.DEFAULT_OFF_HOOKS` にも足す
   （互換スイートが使う一時的な設定ファイルで有効にする。Python 実装は常に有効なため）。
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
- `test_irreversible_guard.py` の G 類（ガードファイル）は、保護対象を hhx の実行ファイル・`~/.config/hhx`・hook の登録ファイルに
  置き換えた。旧配布先（`~/.claude/hooks`・`~/.codex/hooks`）と dotfiles の正本は、hhx では通過を期待し、Python では元の deny のまま流す。
  `WT_AGENT_WORKTREE_POLICY` を前提にした `OnDemandFileToolPassThroughTest` は、hhx では skip する。
- 状態とキャッシュの置き場所を hhx の置き場所（`$XDG_CACHE_HOME/hhx`、既定は `~/.cache/hhx`）へ移したので、
  `test_pr_context.py` はキャッシュのパスを `helpers.PR_CONTEXT_CACHE` で、`test_agents_local_context.py` は隔離を
  `CODEX_HOME` の代わりに `XDG_CACHE_HOME` で行う。`helpers.py` は、hhx を対象にするとき開発者の `XDG_CACHE_HOME` を外す。
  agents-local-context の状態を使えないときの警告は、SQLite をやめたので「状態DB」を「状態ファイル」にした。
- `test_push_ci_context.py` は、hhx では待機コマンドが `hhx wait-ci --progress` であることを `helpers.WAIT_CI_COMMAND` で確かめる。
- `test_worktree_guard.py` は、hhx では snapshot の ref・作成者・一時 index の名前を新しい値（`refs/hhx/discard-snapshot`、
  `hhx <hhx@localhost>`、`hhx-discard-snapshot.index`）で確かめる。`helpers.snapshot_ref_exists` の既定の ref も同じく切り替える。
  ブランチ attach の deny（`DetachedWorktreePolicyTest`）は wx へ移るので、hhx では skip する。
  `PassThroughTest` の「ルールが発火したか」は、hhx では存在しない cwd で起動したときの理由文のラベルから読む。
  `MainWorkspacePassThroughTest` は hhx でもそのまま流す（`WT_AGENT_WORKTREE_POLICY` は hhx では意味を持たない）。
