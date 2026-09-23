# pr-body-staleness

[English](pr-body-staleness.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code と Codex で有効。

`git push` の成功後、PR 本文の最終編集日時と commit の日時を比べます。本文より新しい commit があれば、本文が現在の変更と合っているかをエージェントに確認させます。新しい commit があるだけで本文を古いと断定しません。

既定の案内は `update-pr` スキルを参照します。変更するには `~/.config/hhx/config.yaml` に `hooks.pr-body-staleness.update-instruction` を書きます。

```yaml
hooks:
  pr-body-staleness:
    update-instruction: "本文が古ければ gh pr edit で更新する。"
```

認証済みの `gh` を呼び、PR を取得できなければ黙って終了します。rebase や cherry-pick で commit の日時が変わると、本文が正しくても確認を促すことがあります。
