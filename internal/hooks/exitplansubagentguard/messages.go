package exitplansubagentguard

import (
	"strings"

	"github.com/HappyOnigiri/hhx/internal/i18n"
)

// 理由文は待つか、TaskStop で止めてから確定するかの 2 つだけを案内し、
// プランの書き直しでは通らないことを明記して、文面を変えての再試行を思いとどまらせる。
// Claude Code の ExitPlanMode にだけ登録するので、Claude Code のツール名（TaskStop）を書いてよい。
const (
	idReason = "exit-plan-subagent-guard.reason"
	idHead   = "exit-plan-subagent-guard.head"
	idTail   = "exit-plan-subagent-guard.tail"
)

var messages = i18n.Register(i18n.Catalog{
	// 見出し・未完了のエージェントの一覧・案内の順に並べる。
	idReason: {
		EN: "{{.Head}}\n\nAgents still running:\n{{.Agents}}\n\n{{.Tail}}",
		JA: "{{.Head}}\n\n未完了のエージェント:\n{{.Agents}}\n\n{{.Tail}}",
	},
	// なぜ今確定してはいけないか（承認の後に結果が届き、確定済みのプランを直すことになる）。
	idHead: {
		EN: "An agent started in the background has not returned its result yet. If you finalize the plan now, " +
			"the result will arrive after the user approves it, and you will have to revise a plan that is already final.",
		JA: "バックグラウンドで起動したエージェントの結果がまだ返っていません。ここで確定すると、" +
			"ユーザーの承認後に結果が届き、確定済みのプランを直すことになります。",
	},
	// 待つ・TaskStop で止めて伝える、の 2 択だけを示す。「確認済み」を待たない理由にさせず、書き直しでは通らないと明記する。
	idTail: {
		EN: "The result arrives as a completion notice. Wait for it, reflect it in the plan, and then finalize.\n" +
			"\"The facts that decide the conclusion are already confirmed\" is not a reason to skip waiting. Agents " +
			"other than research (reviews, alternative designs, and so on) are also running to catch what you missed.\n" +
			"If you will not wait, stop the agent with TaskStop, tell the user you stopped it, and then finalize " +
			"(once it is stopped, a completion notice is sent and this check passes).\n" +
			"This is not about the wording, so rewriting the plan and trying again will not change the outcome.",
		JA: "結果は完了通知として届きます。届いてからプランへ反映して確定してください。\n" +
			"「結論を左右する事実は確認済み」は待たない理由になりません。調査以外のエージェント" +
			"(レビュー・設計の別案出しなど) も、見落としを出すために走っています。\n" +
			"待たないなら TaskStop で停止し、停止したことをユーザーへ伝えてから確定してください" +
			"(停止すれば終了通知が出て、この判定は通ります)。\n" +
			"文面の問題ではないので、プランを書き直して再実行しても変わりません。",
	},
})

// reason は未完了のエージェントの一覧を挟んだ理由文を返す。一覧は `- <id> (<説明>)` で、説明が空なら括弧を付けない。
func reason(language i18n.Language, pending []pendingAgent) string {
	items := make([]string, len(pending))
	for index, agent := range pending {
		items[index] = "- " + agent.id
		if agent.description != "" {
			items[index] += " (" + agent.description + ")"
		}
	}
	return messages.Text(language, idReason, map[string]any{
		"Head":   messages.T(language, idHead),
		"Agents": strings.Join(items, "\n"),
		"Tail":   messages.T(language, idTail),
	})
}
