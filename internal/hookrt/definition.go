// Package hookrt は hook の定義と、全 hook に共通する実行時の処理を持つ。
//
// 共通化するのは payload の読み込み、出力スキーマ、実行時の保護（panic の回復と fail-open、終了コード 0）だけである。
// コマンド文字列の解析は hook ごとに誤爆の条件が違うため、ここには置かない。
package hookrt

// Agent は hook を登録する CLI である。
type Agent string

const (
	Claude Agent = "claude"
	Codex  Agent = "codex"
)

// Agents は install が扱う CLI を、既定で処理する順に返す。
func Agents() []Agent {
	return []Agent{Claude, Codex}
}

// Registration は hook 1 本を 1 つの CLI の 1 つのイベントへ登録する単位である。
// 零値の項目は設定ファイルへ書かない。任意項目に null を書くと、CLI がグループごと無視するためである。
type Registration struct {
	Agent Agent
	Event string
	// Matcher が空ならキーごと省略し、全ツール（またはイベントの全発生）に適用させる。
	Matcher                string
	Timeout                int
	StatusMessage          string
	AdditionalContextLimit int
}

// Definition は hook 1 本の定義である。install と `hhx hook <name>` の振り分けは、同じ定義の一覧から作る。
type Definition struct {
	// Name は `hhx hook <name>` の <name> で、登録するコマンド文字列の一部になる。変えると再登録が要る。
	Name string
	// DefaultEnabled は設定に enabled が無いときの有効・無効である。
	DefaultEnabled bool
	Registrations  []Registration
	// Gate は payload を解析する前に、生の入力の部分一致で大半の呼び出しを抜ける一次ゲートである。
	// 偽を返すと設定も読まずに無出力で終わる。nil なら常に通す。
	Gate func(input []byte) bool
	// GateStdinOnly が真なら、デバッグ経路（引数）の入力には Gate を当てない。
	// 引数が payload でもコマンド文字列でもない hook（exit-plan-subagent-guard は transcript のパスを受け取る）で使う。
	GateStdinOnly bool
	// Run は hook の本体である。エラーを返すか panic すると、出力を捨てて無出力で終わる（fail-open）。
	Run func(*Context) error
	// NewSettings は Context.Settings へ渡す hook 固有の設定の零値を返す。nil なら hook 固有の設定を持たない。
	// install はこれで設定を読み込み、型の誤りを報告する（実行時は無出力で終わるため、気付く機会が install しかない）。
	NewSettings func() any
}
