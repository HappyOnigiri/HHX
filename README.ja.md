# Happy Hooks

[English](README.md) | 日本語

Happy Hooks は、Claude Code と Codex が長時間の作業を滞りなく進めるための hook 集です。`hhx` という 1 つの Go バイナリで動作します。

## 特長

- **作業を止めない:** 不要な確認プロンプトや、作業を中断させるよくある誤りを防ぎます。
- **効率よく待つ:** ジョブごとに何度も確認せずに CI を待つ方法をエージェントに案内します。
- **必要な情報を渡す:** PR の情報やローカルの指示を、作業中の適切な場面で注入します。
- **未コミットの変更を守る:** 変更を破棄するコマンドの前に Git の snapshot を保存します。

コマンドを検査する hook は、静的な判定でよくある誤りを止めます。セキュリティ境界ではありません。

## インストール

Apple Silicon の macOS と Claude Code または Codex が必要です。

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/install.sh | bash
hhx install
```

インストーラーは `hhx` を `~/.local/bin` に置きます。`hhx install` は設定済みのエージェントに hook を登録します。ソースからビルドする場合は、`hhx install` の前に `make install` を実行します。

## 使い方

インストール後、hook は自動的に動きます。CI の待機には次のコマンドを使えます。

```sh
hhx wait-ci --progress       # 現在の PR を待つ
hhx wait-ci 123 --progress   # 指定した PR を待つ
```

新しい版の確認は `hhx update`、適用は `hhx update --apply` で行います。hook の実行中に更新を確認することはありません。

## 設定

`~/.config/hhx/config.yaml` で表示言語を選び、hook を無効にできます。

```yaml
language: ja  # en（既定）か ja
hooks:
  some-hook:
    enabled: false
```

変更に再インストールは不要です。コマンドとオプションは `hhx --help`、リポジトリごとの禁止語リストは[forbidden-term-guard の設定](docs/forbidden-terms.md)を参照してください。

## 破棄した変更の復元

`discard-guard` は、未コミットの変更を破棄する Git コマンドの前に、追跡中・未追跡のファイルを `refs/hhx/discard-snapshot` に保存します。snapshot を探して復元するには、次を実行します。

```sh
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
```

`N` は対象の作業ツリーの番号に置き換えます。index は変わりません。無視されたファイルと submodule 内の未コミットの変更は保存されません。

## コントリビュート

コントリビュートを歓迎します。バグ報告や提案は [Issues](https://github.com/HappyOnigiri/HappyHooks/issues) に、変更は [プルリクエスト](https://github.com/HappyOnigiri/HappyHooks/pulls) でお寄せください。文書や翻訳の改善も歓迎します。

## アンインストール

```sh
curl -fsSL https://github.com/HappyOnigiri/HappyHooks/releases/latest/download/uninstall.sh | bash
```

hook の登録とバイナリを削除します。設定とキャッシュは残り、その場所をスクリプトが表示します。

## ライセンス

[MIT](LICENSE)
