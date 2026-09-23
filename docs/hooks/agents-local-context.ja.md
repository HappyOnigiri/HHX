# agents-local-context

[English](agents-local-context.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Codex のみ有効。

ツールの対象パスに適用される `AGENTS.local.md` を Git のルートから順に注入します。セッション開始時とツール実行前に調べ、compact の後と subagent には記録済みのルールを入れ直します。

同じセッションで内容が変わらないルールは、通常 1 回だけ注入します。ツール入力からパスを見つけるのはベストエフォートで、変数・クォート・heredoc はパスを隠したりパスに見えたりします。コンテキストを足すだけで、ツールは止めません。
