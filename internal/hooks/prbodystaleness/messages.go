package prbodystaleness

// defaultUpdateInstruction は本文の更新方法の案内の既定値である。設定の update-instruction で差し替えられる。
const defaultUpdateInstruction = "食い違いがあれば update-pr スキル（プロジェクトに `local-*` 版があればそちら）で本文を更新する。"

// staleMessage は注入する注意である。%[1]s は PR 番号、%[2]d は本文より後の commit の数、%[3]s はその見出しの一覧、
// %[4]s は本文の更新方法の案内、%[5]s は PR の URL である。
const staleMessage = "push が成功した。PR #%[1]s の本文は、以下 %[2]d 件のコミットより前に書かれている。\n%[3]s\n" +
	"- 本文の記述とこれらのコミットの内容が食い違っていないか確認すること" +
	"（本文に無い挙動が増えていないか、本文が「やる」と書いた内容が変わっていないか）。\n" +
	"- %[4]s差分と一致していれば更新は不要で、その旨だけ報告すればよい。\n" +
	"- PR: %[5]s"

// moreCommits は載せきれなかった commit の件数の行である。
const moreCommits = "\n  - (ほか %d 件)"
