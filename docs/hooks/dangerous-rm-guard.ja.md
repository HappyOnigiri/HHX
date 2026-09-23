# dangerous-rm-guard

[English](dangerous-rm-guard.md) | 日本語 · [hook 一覧](../../README.ja.md#hook-一覧)

**既定:** Claude Code のみ有効。

Claude Code の組み込み検査が確認プロンプトを出す `rm`・`rmdir` の形を先に拒否します。空になりうる変数で始まるパス、静的に解決できない対象、重要なディレクトリ、作業ディレクトリやその祖先などが対象です。

エージェントはユーザー待ちになる代わりに、対処できる理由文を受け取ります。判定は Claude Code v2.1.239 の規則に合わせており、新しい版では異なる可能性があります。
