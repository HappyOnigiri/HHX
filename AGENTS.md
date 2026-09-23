# AGENTS.md

`hhx` は、Claude Code と Codex の agent hook を 1 つのバイナリで提供する Go 製 CLI である。
hook は 1 本につき 1 エントリ（`hhx hook <name>`）で登録し、中身の差し替えで登録を変えない。

## 不変条件

- `hhx hook <name>` はどの経路でも終了コード 0 で終わる（名前を省いた手入力は usage を出して 2）。未知の名前・壊れた設定・本体のエラーや panic は無出力にする（fail-open）。
- PreToolUse を通すときは何も出力しない。`allow` と `ask` は返さない（`ask` は Codex が解釈しない）。
- `hhx install` / `uninstall` は hhx のエントリ（先頭トークンの basename が `hhx`、2 番目が `hook`）だけを扱う。
  wx や利用者の hook は、同じグループにも入れないし消しもしない。
- install は冪等で、hhx のエントリに変化が無ければファイルに触らない。
  既存の hhx のグループは同じ位置へ置き直す。Codex は hooks.json の中の位置で hook の信頼状態を記録するため、位置が動くと再承認が要る。
- 設定ファイルに任意項目の `null` を書かない。matcher を持たないグループに不正なエントリが 1 つでもあると、グループごと無視される。
- 各 hook のコマンド解析（シェルのトークナイザ）は共通化しない。hook ごとに誤爆の条件が調整されており、共通化すると判定が変わり得る。
  共通化するのは `internal/hookrt` の payload の読み込み・出力スキーマ・実行時の保護だけとする。
- 全 Bash 呼び出しで走るため、一次ゲート（生の入力の部分一致）を設定の読み込みより前に置き、そこまでを軽く保つ。

## 構成

- `cmd/hhx`: サブコマンドの振り分け
- `internal/hookrt`: hook の定義と実行時の共通処理
- `internal/registry`: hook の一覧。install と `hhx hook` の振り分けはここから作る
- `internal/install`: Claude の settings.json と Codex の hooks.json の読み書き
- `internal/config`: `~/.config/hhx/config.yaml` の読み込み
- `internal/update`: GitHub Releases の確認と、Release 添付の `install.sh` による更新（`hhx update`）
- `scripts/`: 配布物のビルド、インストーラーとアンインストーラー、それらのテスト
- `.github/workflows/`: CI とリリース（[docs/release.md](docs/release.md)）、flaky なテストの起票（`report-flaky-tests.yml`）
- `.github/scripts/`: flaky なテストの issue を起票する reporter（`actions/github-script` から呼ぶ）
- `tools/`: 開発用の補助ツール。`tools/citest` は CI のテストで落ちたテストだけを 1 回再実行し、報告を artifact に残す
- `compat/`: 移行期間だけ置く Python の互換スイート（[compat/README.md](compat/README.md)）

## 開発

- CI と同じ検査は `make ci`（lint・race とカバレッジのテスト・配布物・インストーラーの検査を並列に走らせる）。
  互換スイートは Python 実装を参照するので CI では流さず、`make check`（`make ci` と `make compat-test`）で手元でだけ流す。
- CI のジョブは Makefile のターゲットを 1 つずつ走らせる。検査を足すときは Makefile に書き、`ci-checks` と `ci.yml` の matrix の両方へ足す。
- hook を移植したら、そのパッケージを Makefile の `GO_COVERAGE_PACKAGES` に足す。
- CI のテストは citest 経由で走り、落ちたテストを 1 回だけ再実行する。再実行で通ったテストは CI を落とさず、
  CI の完了後に `report-flaky-tests.yml` がテストごとに issue を起票する。再実行でも落ちれば CI は失敗する。
  `ci.yml` の `name:`、アップロードのステップ名、artifact の名前、profile は reporter との契約で、`make reporter-check` が突き合わせる。
- CI のランナーには本物の gh があり、git の利用者設定が無い。gh を使うテストは偽の gh を PATH の先頭に置き、`GH_TOKEN` を渡さない。
  git を使うテストは `GIT_CONFIG_GLOBAL=/dev/null` と `GIT_CONFIG_SYSTEM=/dev/null` で隔離し、作成者を `-c user.name=... -c user.email=...` で渡す。
- hook の実行中にネットワークへ出ない。更新の確認は `hhx update` を明示的に実行したときだけ行う。
- install のテストは一時的な HOME で行い、実機の設定ファイルに触れない。
- コメントは日本語で書き、保守に必要な意図・制約・契約だけを残す。
