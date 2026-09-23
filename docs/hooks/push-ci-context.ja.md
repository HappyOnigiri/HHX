# push-ci-context

[English](push-ci-context.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code と Codex で有効。

`git push` または `gh pr create` の成功後、残りの作業を先に終え、それから `hhx wait-ci --progress` で CI の完了まで待つようエージェントに案内します。短い間隔で繰り返し確認することや、CI を見ずにターンを終えることを避けます。

`gh workflow run` の成功後は `gh run watch` で対象の workflow を待つよう案内します。失敗した push や dry-run の push では発火しません。コンテキストを足すだけで、権限の判断は返しません。
