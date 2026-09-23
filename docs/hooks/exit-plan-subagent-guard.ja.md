# exit-plan-subagent-guard

[English](exit-plan-subagent-guard.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code のみ有効。

メインのセッションが起動したバックグラウンドのエージェントから結果が返るまで、`ExitPlanMode` を止めます。そのエージェントが終了するか停止すれば、結果を計画に反映できる状態として通します。

Claude Code のセッション transcript を読みます。読めない場合や通知の形式が変わった場合は、未完了のエージェントを検出できないことがあります。前景のエージェントや subagent が起動したバックグラウンドのエージェントは追いません。
