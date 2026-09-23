# discard-guard

[English](discard-guard.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code と Codex で有効。

未コミットの変更を破棄する Git コマンドの前に、追跡中・未追跡のファイルを `refs/hhx/discard-snapshot` の reflog に保存します。
`git reset --hard`、強制的な checkout・switch、`git restore`、`git clean -f` などが対象です。
保存できればコマンドを通し、対象を特定できない場合や保存に失敗した場合は止めます。
snapshot 自体は作業ツリーと index を変更しません。

## snapshot の復元

作業ツリーのパスが付いたエントリを探し、その番号を `N` に入れます。

```sh
git reflog show --format='%gd %gs' refs/hhx/discard-snapshot
git restore --source='refs/hhx/discard-snapshot@{N}' --worktree -- .
```

復元するのは作業ツリーで、index は変更しません。無視されたファイルと submodule 内の未コミットの変更は保存されません。snapshot は reflog とともに期限切れになります。
