package prbodystaleness

import "github.com/HappyOnigiri/hhx/internal/i18n"

const (
	idStale              = "pr-body-staleness.stale"
	idMoreCommits        = "pr-body-staleness.more-commits"
	idDefaultInstruction = "pr-body-staleness.default-update-instruction"
	idSentenceEnd        = "pr-body-staleness.sentence-end"
)

// sentenceEnders は文末とみなす記号である。設定の update-instruction の末尾がこれ以外なら、idSentenceEnd を足す。
// 利用者が書く文はどちらの言語でもありうるので、両方の記号を言語にかかわらず受け付ける。
const sentenceEnders = "。．.！!？?"

var messages = i18n.Register(i18n.Catalog{
	// 本文より後のコミットを並べ、本文と食い違っていないかを確かめさせる。一致していれば更新せず報告だけでよいと伝える。
	// {{.Instruction}} は本文の更新方法の案内（1 文）で、設定で差し替えられる。
	idStale: {
		EN: "The push succeeded. The body of PR #{{.Number}} was written before the following {{.Count}} commit(s).\n" +
			"{{.Commits}}\n" +
			"- Check whether the body disagrees with these commits " +
			"(whether behavior that is not in the body was added, and whether what the body says it will do has changed).\n" +
			"- {{.Instruction}} If the body matches the diff, no update is needed; just report that.\n" +
			"- PR: {{.URL}}",
		JA: "push が成功した。PR #{{.Number}} の本文は、以下 {{.Count}} 件のコミットより前に書かれている。\n{{.Commits}}\n" +
			"- 本文の記述とこれらのコミットの内容が食い違っていないか確認すること" +
			"（本文に無い挙動が増えていないか、本文が「やる」と書いた内容が変わっていないか）。\n" +
			"- {{.Instruction}}差分と一致していれば更新は不要で、その旨だけ報告すればよい。\n" +
			"- PR: {{.URL}}",
	},
	// 載せきれなかったコミットの件数の行。
	idMoreCommits: {EN: "\n  - ({{.Count}} more)", JA: "\n  - (ほか {{.Count}} 件)"},
	// 本文の更新方法の案内の既定値。設定の update-instruction があればそちらを使う（利用者の文は訳さない）。
	idDefaultInstruction: {
		EN: "If they disagree, update the body with the update-pr skill (or its `local-*` version if the project has one).",
		JA: "食い違いがあれば update-pr スキル（プロジェクトに `local-*` 版があればそちら）で本文を更新する。",
	},
	// 文末の記号が無い update-instruction に足す記号。
	idSentenceEnd: {EN: ".", JA: "。"},
})
