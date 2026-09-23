# Happy Hooks

[English](README.md) | 日本語

Happy Hooks は、Claude Code と Codex が長時間の作業を滞りなく進めるための hook 集です。`hhx` という 1 つの Go バイナリで動作します。

- 不要な確認プロンプトによって、エージェントがユーザー待ちで止まるのを防ぎます。
- CI の完了待ちなどで適切な待機方法を案内し、ツールの繰り返し呼び出しを減らします。
- PR や作業環境に関する情報を必要な場面で渡し、エージェントの判断を助けます。

コマンドを検査する hook は、静的な判定でよくある誤りを止めます。セキュリティ境界ではありません。

## インストール

Happy Hooks は Apple Silicon の macOS 向けに配布しています。

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/install.sh | bash
hhx install         # Happy Hooks の hook を ~/.claude/settings.json と ~/.codex/hooks.json に登録する
```

インストーラーはチェックサムを確かめてからバイナリを `~/.local/bin/hhx` に置きます。hook の登録はしません。
ソースからビルドするときは `make install` を実行します（`bin/hhx` をビルドして `~/.local/bin/hhx` にコピーします）。

`hhx install` は Happy Hooks 自身の hook のグループだけを書き、他の hook には触れません。
冪等なので、変更が無ければもう一度実行してもファイルを変えません。
既定では、設定ディレクトリ（`~/.claude`、`~/.codex`）のある agent をそれぞれ設定します。選ぶときは `--agent claude` か `--agent codex` を渡します。
`~/.claude/settings.local.json` があっても、Happy Hooks は常に Claude のユーザー設定（`~/.claude/settings.json`）に書きます。

`hhx uninstall` は Happy Hooks が書いたエントリだけを外します。

## 更新

```sh
hhx update          # GitHub Releases で新しい版があるかを確かめる
hhx update --apply  # その版のインストーラーで入れる
```

更新を確かめるのは `hhx update` を実行したときだけです。hook がそのためにネットワークへ出ることはありません。
開発ビルド（`make install`）は自分では更新しません。

## アンインストール

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/uninstall.sh | bash
```

`hhx uninstall` を実行してから `~/.local/bin/hhx` を消します。
設定とキャッシュは残し、その場所を表示します。

## 設定

`~/.config/hhx/config.yaml`（`HHX_CONFIG` で差し替えられます）で、表示言語を選び、hook の有効・無効と hook 固有の設定を持たせます。

```yaml
language: ja   # en（既定）か ja
hooks:
  some-hook:
    enabled: false
```

`language` は、Happy Hooks が書くものすべての言語を選びます。deny の理由文、注入するコンテキスト、`hhx` のコマンドの出力が対象です。
指定が無いとき、または `en`・`ja` 以外の値のときは英語になり、不正な値は `hhx install` が報告します。
機械が読む部分はどの言語でも同じです。JSON のキー、注入するコンテキストのタグと項目名、
`hhx wait-ci` の接頭辞 `wait-ci:`・最終行・終了コードは変わりません。

既知の hook はすべて `hhx install` が登録し、動かすかどうかは実行時にこのファイルから決めます。ファイルを変えても入れ直す必要はありません。
ファイルが無い・壊れているときも hook は止まらず、既定の設定で動きます。
設定の誤りは `hhx install` が報告します。

## hook

| hook | 既定 | 拒否するもの |
| --- | --- | --- |
| `pr-merge-guard` | 有効 | PR のマージ。`gh pr merge`、マージの REST エンドポイント、GraphQL のマージ mutation、`curl` / `wget` による GitHub API の更新、`git fetch ... pull/N/head` |
| `discard-guard` | 有効 | それ自体では何も拒否しない。未コミットの変更を破棄する Git のコマンドの前に、作業ツリーの snapshot を保存する（下記）。対象のリポジトリを特定できないときと、snapshot を保存できないときだけ拒否する |
| `idle-wait-guard` | 有効 | 待つかリテラルを出すだけのコマンド（`sleep 600`、`echo ok`）。agent が待ちのあいだターンを埋めるのに使う |
| `forbidden-term-guard` | 有効 | リポジトリのリストにある語を含む PR や issue の本文の送信（[docs/forbidden-terms.md](docs/forbidden-terms.md)） |
| `irreversible-guard` | 有効 | 誰にも元に戻せない操作。資格情報の失効・削除、パッケージのレジストリへの公開（`--dry-run` を除く）、リモートのリソースの削除、Git のオブジェクトの破壊、ディスクやバックアップの消去、どのツールからでも秘密ファイル（`.env*`、`~/.ssh`、`*.pem`）への書き込み、`hhx` バイナリ・`~/.config/hhx`・hook を登録するファイルの削除や移動 |
| `dangerous-rm-guard` | 有効（Claude Code だけ） | Claude Code の組み込みの検査が、どの権限設定でも飛ばせない確認で止める `rm` / `rmdir` の形。空になりうる変数で始まるパス、静的に解決できない対象、重要なディレクトリ、作業ディレクトリとその祖先 |
| `exit-plan-subagent-guard` | 有効（Claude Code だけ） | バックグラウンドで起動した agent の結果が返る前の、プランモードの終了（`ExitPlanMode`）。agent が終わるか止めれば通す |
| `git-hookspath-guard` | 無効 | `core.hooksPath` の変更と、`.git/config` などの Git の設定ファイルの直接の編集 |

コンテキストの hook は、agent のコンテキストに文を足すだけで、何も拒否しません。

| hook | 既定 | イベント | 足すもの |
| --- | --- | --- | --- |
| `pr-context` | 有効 | `UserPromptSubmit` | プロンプト中の GitHub の PR の URL（3 件まで、`#discussion_r` のアンカーはそれぞれ 2 件まで）ごとに、状態・head と base・規模・ローカルの clone に head の commit があるか。ローカルのファイルは PR ではないことも念押しする |
| `push-ci-context` | 有効 | `PostToolUse`（Bash） | `git push`・`gh pr create`・`gh workflow run` の成功の後に、ターンの残りの作業を終えてから `hhx wait-ci --progress`（dispatch した workflow なら `gh run watch`）で CI を待つ方法 |
| `pr-body-staleness` | 有効 | `PostToolUse`（Bash） | `git push` の成功の後に、PR の本文を最後に編集した後の commit。本文が古くなっていないかを agent に確かめさせる |
| `agents-local-context` | 有効（Codex だけ） | `PreToolUse`、`SessionStart`、`SubagentStart` | ツールが触るパスに当てはまる `AGENTS.local.md` を、Git のルートから順に、セッションごとに 1 回。compact の後と subagent には入れ直す |

`git-hookspath-guard` は、Git のグローバルな hook からリポジトリの hook へ委ねる構成向けです。次で有効にします。

```yaml
hooks:
  git-hookspath-guard:
    enabled: true
```

`pr-body-staleness` は、本文を `update-pr` スキルで更新するよう agent に伝えます。
この 1 文は、自分の PR の本文の更新方法に差し替えられます。
どの言語でも書いたとおりに使い、文末の記号が無ければ `.`（`language: ja` なら `。`）を足します。

```yaml
hooks:
  pr-body-staleness:
    update-instruction: "食い違いがあれば `gh pr edit --body-file` で本文を更新する。"
```

`pr-context` と `pr-body-staleness` は `gh` を呼びます（`PATH` にあってサインイン済みであること）。失敗したときや 8 秒を超えたときは黙って諦めます。
`pr-context` は取得した内容を 90 秒キャッシュし、セッションごとに注入した内容を 24 時間記録します。
`agents-local-context` はセッションごとに注入したルールを 30 日記録します。
どちらも `$XDG_CACHE_HOME/hhx/`（既定は `~/.cache/hhx/`）に置き、消しても hook がもう一度注入するだけです。

`forbidden-term-guard` は、リポジトリの共通の Git ディレクトリに `forbidden-terms.txt` が無ければ何もしません。

`dangerous-rm-guard` は Claude Code v2.1.239 の組み込みの検査の規則に従い、それらのコマンドを先に拒否します。
ユーザーを止める確認の代わりに、agent が次の手を決められる理由を返すためです。
新しい Claude Code では組み込みの検査が変わっているかもしれません。

### 破棄の snapshot

`discard-guard` が見るのは、`git reset --hard`、`git checkout` の破棄の形（`-- <path>`、`.`、`-f`、`-B`）、
`git switch -f` / `-C` / `--discard-changes`、`git restore <path>`（`--staged` だけのものを除く）、`git clean -f`、
`git apply -R` / `--3way` / `--reject` です。
そのコマンドが走る前に、作業ツリー（追跡中のファイルも未追跡のファイルも）を `refs/hhx/discard-snapshot` の reflog へ commit します。
作業ツリーと index には触れず、作業ツリーが `HEAD` と同じなら何も保存しません。
リテラルの `cd` と `git -C` を辿ってリポジトリを探し、コマンドが破棄するリポジトリをすべて保存します。
`( ... )` や `$( ... )` の中の `cd` の後は、括弧の外のディレクトリと `cd` の先の両方を保存します。
括弧の数え方が、クォートの中や `case` のパターンを区別しないためです。
1 つの `git` の複数の `-C` は、Git と同じく順に適用します。

ref は共通の Git ディレクトリにあるので、リポジトリの worktree はすべて同じ ref を使います。
各エントリのメッセージは `wt-snapshot: <規則> @ <worktree>` です。発火した規則（`git-reset-hard / git-clean` など。どの言語でも同じ）と、
保存した worktree の最上位のディレクトリが入ります。復元するには次のようにします。

```sh
# 1. 自分の worktree のエントリを探す
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
# 2. そのファイルを作業ツリーへ戻す（index はそのまま）
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
# 1 ファイルだけ取り出すなら
git show 'refs/hhx/discard-snapshot@{N}:path/to/file'
```

snapshot へは reflog からしか辿れないので、古いエントリは他の reflog のエントリと同じく `git gc` で期限切れになります。

### 1 セッションだけマージを許す

agent の CLI を `AGENT_ALLOW_PR_MERGE=1` を付けて起動すると、そのセッションでは `pr-merge-guard` がすべて通します。
hook は agent の CLI のプロセスの環境変数を受け継ぐので、これを設定できるのはセッションを起動した人だけです。
agent がシェルで `export AGENT_ALLOW_PR_MERGE=1` を実行しても、hook の見る環境変数は変わりません。
`1` 以外の値ではガードは有効のままです。

### 限界

ガードはコマンドの文字列を読むだけで、実行もシェルの再現もしません。
よくある誤りを止めるもので、セキュリティ境界ではありません。
意図してコマンドを隠す agent は通り抜けます。たとえば変数の展開（`$CMD`）、`eval`、
別のシェル（`sh -c '...'`）、スクリプトのファイル、別の言語（`python3 -c ...`）です。
クォートの中の文字列はコマンドとして扱わないので、`echo 'gh pr merge'` は意図して通します。
agent にガードの迂回を思いとどまらせるのは拒否の理由文で、
別のコマンドを試さずユーザーに報告するよう伝えています。

`discard-guard` は保存する側に倒します。

- 無視対象の未追跡ファイルは保存しないので、`git clean -fdx` ではそれらを失います。`.gitignore` に当たる追跡中のファイルは保存します。
- `ssh` 越しなど、別のマシンで破棄するものは保存できません。
- `=` ではなく空白で書いた `--git-dir <path>` を見落とします。
- `--` を付けずに書いた `git checkout <path>` と `git checkout <commit> <path>`、`git checkout --ours <path>`、`git rm -f` を見落とします。
- 一覧に無いグローバルオプションの後に書いた破棄のコマンドを見落とします。`git -P reset --hard` や `git --no-optional-locks reset --hard` などです。
  一覧にあるのは `-c`、`-C`、`-p`、`--paginate`、`--no-pager`、`--git-dir=`、`--work-tree=`、`--exec-path=`、`--literal-pathspecs` です。
- submodule の中の未コミットの変更は保存しないので、`git reset --hard --recurse-submodules` ではそれらを失います。
  未追跡の入れ子のリポジトリは commit の ID としてしか保存しないので、`git clean -ffd` ではそのファイルと履歴を失います。
- heredoc など、破棄のコマンドに言及するだけの文字列でも snapshot を作ります。
- 対象を辿れないコマンドは拒否します。変数・glob・クォートを含むパス、`pushd` / `popd`、`sh -c`、
  `GIT_DIR` / `--git-dir` / `--work-tree`、まだ存在しないディレクトリ（同じコマンドの `mkdir` や `git clone` で作るもの）です。
  ディレクトリを作る手順は、別のコマンドで先に実行してください。
- Codex はコマンドごとの作業ディレクトリを hook に渡さず、セッションを始めたディレクトリだけを渡すので、それを基準に対象を決めます。
- パイプラインやバックグラウンドのコマンドの中の `cd`（`cd dir | ...`、`cd dir & ...`）も、
  シェルがサブシェルで実行することがあるにもかかわらず、コマンドの残りのディレクトリを変えるものとして扱います。

コンテキストの hook も文字列で照合します。

- `pr-context` は区切りの後に `github.com` が続けば URL として拾うので、`evil.com/github.com/o/r/pull/1` にも反応します。
- `pr-body-staleness` は commit の日時で比べます。rebase・amend・cherry-pick は日時を付け直すので、
  正しいままの本文を確かめさせることがあります。後から取り込んだ古い commit は見落とします。
- `push-ci-context` は、push の先が GitHub かをコマンドの出力から判断し、分からなければ作業ディレクトリの origin から判断します。
  Codex はセッションを始めたディレクトリしか渡さないので、判断できないときは案内を注入します。
- `agents-local-context` がコマンドからパスを探すのはベストエフォートです。クォートの中の文字列・heredoc・変数は、パスを隠したりパスに見えたりします。

`exit-plan-subagent-guard` はセッションの transcript だけを読みます。

- Claude Code がバックグラウンドの agent の起動・再開・終了のときに書く文面そのものに依存します。
  その文面が変わると、黙って拒否しなくなります。
- ファイルから読んだものなど、終了の通知に見える文字列は、agent が終わったものとして数えます。
- 追うのはメインのセッションが起動したバックグラウンドの agent だけで、前景で動かした agent や subagent が起動した agent は追いません。
- transcript が無いときや読めないときは通します。

## CI を待つ

`hhx wait-ci` は PR の check がすべて終わるまで待ち、結果を 1 回だけ報告します。
agent は push の後、ジョブごとの更新を読み続けずに CI を待てます。

```sh
hhx wait-ci --progress         # 現在のブランチの PR（detached なら HEAD の PR）
hhx wait-ci 123 --all-checks   # PR の番号・ブランチ・URL。成功した check も並べる
```

出力の 1 行目が結論、最終行が `wait-ci: exit=<code> failed=<n> total=<n>` です。
出力が切れても、どちらかの端から結果が分かります。`--progress` の行が stderr に出るほかは、すべて stdout に出ます。
既定では失敗した check だけを並べます。

| 終了コード | 意味 |
| --- | --- |
| 0 | すべての check が成功した、ブランチに PR が無い、またはリポジトリに CI が無い |
| 1 | 失敗した check がある |
| 2 | 引数が不正 |
| 3 | タイムアウト（push した commit が PR の head にならなかった、check が現れなかった、check が終わらなかった） |
| 4 | `gh` が失敗し続けた |
| 5 | PR が base とコンフリクトしていて、check が起動しない |

`gh` と `git` を呼ぶので、どちらも `PATH` にあり、`gh` がサインイン済みである必要があります。
reference を省くと、まず `HEAD` が PR の head になるまで待つので、前の push の check を報告しません。
他より遅れて始まる workflow を拾うため、終わった check の集合が `--settle` 秒変わらないことも待ちます。
時間の上限は `hhx wait-ci --help` で確かめてください。

## ライセンス

MIT
