package exitplansubagentguard

import "strings"

// 理由文は移植元と同じ文面にする。待つか、TaskStop で止めてから確定するかの 2 つだけを案内し、
// プランの書き直しでは通らないことを明記して、文面を変えての再試行を思いとどまらせる。
// Claude Code の ExitPlanMode にだけ登録するので、Claude Code のツール名（TaskStop）を書いてよい。
const (
	reasonHead = "バックグラウンドで起動したエージェントの結果がまだ返っていません。ここで確定すると、" +
		"ユーザーの承認後に結果が届き、確定済みのプランを直すことになります。"
	reasonTail = "結果は完了通知として届きます。届いてからプランへ反映して確定してください。\n" +
		"「結論を左右する事実は確認済み」は待たない理由になりません。調査以外のエージェント" +
		"(レビュー・設計の別案出しなど) も、見落としを出すために走っています。\n" +
		"待たないなら TaskStop で停止し、停止したことをユーザーへ伝えてから確定してください" +
		"(停止すれば終了通知が出て、この判定は通ります)。\n" +
		"文面の問題ではないので、プランを書き直して再実行しても変わりません。"
)

// reason は未完了のエージェントの一覧を挟んだ理由文を返す。一覧は `- <id> (<説明>)` で、説明が空なら括弧を付けない。
func reason(pending []pendingAgent) string {
	items := make([]string, len(pending))
	for index, agent := range pending {
		items[index] = "- " + agent.id
		if agent.description != "" {
			items[index] += " (" + agent.description + ")"
		}
	}
	return reasonHead + "\n\n未完了のエージェント:\n" + strings.Join(items, "\n") + "\n\n" + reasonTail
}
