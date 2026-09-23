package prmergeguard

import "github.com/HappyOnigiri/hhx/internal/i18n"

// 理由文は「何を・なぜ・どうする」の 3 点を返す。回避を思いとどまらせるのはこの文面だけなので、
// 「迂回する別コマンドを試さない」ことを対応に書く。
// 発火したルールごとの対象の表記（target）は「❌ ブロック:」の後ろに出し、テストはこれでどのルールが発火したかを見る。
const (
	idReason        = "pr-merge-guard.reason"
	idWhy           = "pr-merge-guard.why"
	idHow           = "pr-merge-guard.how"
	idTargetPRMerge = "pr-merge-guard.target.pr-merge"
	idTargetAPI     = "pr-merge-guard.target.api-merge"
	idTargetGraphQL = "pr-merge-guard.target.graphql"
	idTargetHTTP    = "pr-merge-guard.target.http"
	idTargetFetch   = "pr-merge-guard.target.fetch-head"
)

var messages = i18n.Register(i18n.Catalog{
	// 理由文の骨組み。対象・理由・対応の 3 段を、この順で出す。
	idReason: {
		EN: "❌ Blocked: {{.Target}}\n\nReason: {{.Why}}\n\nAction: {{.How}}",
		JA: "❌ ブロック: {{.Target}}\n\n理由: {{.Why}}\n\n対応: {{.How}}",
	},
	// マージはユーザーの手作業であり、どの経路でも止めることを伝える。
	idWhy: {
		EN: "Merging a PR (its timing, merge method, and CI check) is done manually by the user. " +
			"Every operation that leads to a merge is covered, including via the API and via git.",
		JA: "PR のマージ判断 (タイミング・マージ方式・CI の確認) はユーザーが手動で行う運用です。" +
			"API 経由・git 経由を含め、マージに至る操作はすべて対象です。",
	},
	// 別コマンドでの迂回を禁じ、ユーザーへの報告に誘導する。
	idHow: {
		EN: "Do not retry with a different command to get around this block. " +
			"Tell the user what you want to run and why, and ask for instructions.",
		JA: "迂回する別コマンドを試さず、実行したい内容と目的をユーザーに伝えて指示を仰いでください。",
	},
	// gh pr merge の直接の実行。
	idTargetPRMerge: {EN: "gh pr merge", JA: "gh pr merge"},
	// gh api で REST のマージエンドポイントを叩く形。
	idTargetAPI: {
		EN: "the merge endpoints of gh api (pulls/N/merge, repos/O/R/merges)",
		JA: "gh api のマージエンドポイント (pulls/N/merge, repos/O/R/merges)",
	},
	// GraphQL のマージ mutation。
	idTargetGraphQL: {EN: "a merge mutation via GraphQL", JA: "GraphQL によるマージ mutation"},
	// curl などの HTTP クライアントで GitHub API を更新する形。
	idTargetHTTP: {EN: "a GitHub API update via an HTTP client", JA: "HTTP クライアント経由の GitHub API 更新"},
	// PR の head を取ってきてローカルでマージする準備。
	idTargetFetch: {EN: "git fetch origin pull/.../head", JA: "git fetch origin pull/.../head"},
})

func reason(language i18n.Language, target string) string {
	return messages.Text(language, idReason, map[string]any{
		"Target": messages.T(language, target),
		"Why":    messages.T(language, idWhy),
		"How":    messages.T(language, idHow),
	})
}
