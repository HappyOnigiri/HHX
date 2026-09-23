# idle-wait-guard

[English](idle-wait-guard.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code と Codex で有効。

`sleep 600; echo done` や `echo ok` のように、待つか決まった文を出すだけのコマンドを止めます。作業が進まないままエージェントのターンを埋めるのを防ぎ、理由文で実際の処理を待つ方法を案内します。

コマンドの一部でも有効な作業をしうる場合は通します。文字列で判定し、シェルの構文を完全に解釈したり実行したりはしません。
