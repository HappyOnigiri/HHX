# pr-context

[English](pr-context.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code と Codex で有効。

ユーザーのプロンプトに GitHub の PR URL があると、PR の状態、head と base、変更規模、ローカルの clone に head の commit があるかを注入します。ローカルのファイルは PR の内容とは限らないことも伝えます。PR の本文は注入しません。

対象は 1 プロンプトにつき PR URL 3 件まで、各 PR の discussion コメント 2 件までです。同じセッションへの重複した注入は抑えます。認証済みの `gh` が必要ですが、失敗してもプロンプトは止めません。
