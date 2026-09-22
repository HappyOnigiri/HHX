package prmergeguard

// 理由文は「何を・なぜ・どうする」の 3 点を返す。回避を思いとどまらせるのはこの文面だけなので、
// 「迂回する別コマンドを試さない」ことを対応に書く。
const (
	how = "迂回する別コマンドを試さず、実行したい内容と目的をユーザーに伝えて指示を仰いでください。"

	whyMerge = "PR のマージ判断 (タイミング・マージ方式・CI の確認) はユーザーが手動で行う運用です。" +
		"API 経由・git 経由を含め、マージに至る操作はすべて対象です。"
)

// 発火したルールごとの対象の表記。理由文の「❌ ブロック:」の後ろに出し、テストはこれでどのルールが発火したかを見る。
const (
	targetPRMerge  = "gh pr merge"
	targetAPIMerge = "gh api のマージエンドポイント (pulls/N/merge, repos/O/R/merges)"
	targetGraphQL  = "GraphQL によるマージ mutation"
	targetHTTP     = "HTTP クライアント経由の GitHub API 更新"
	targetFetch    = "git fetch origin pull/.../head"
)

func reason(target string) string {
	return "❌ ブロック: " + target + "\n\n理由: " + whyMerge + "\n\n対応: " + how
}
