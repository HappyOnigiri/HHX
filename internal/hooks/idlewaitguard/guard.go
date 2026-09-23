// Package idlewaitguard は、待機のつもりで打つ「何も起きないコマンド」を PreToolUse で拒否する hook（idle-wait-guard）である。
//
// 非同期処理の完了を待つとき、エージェントが `sleep 600; echo done` のバックグラウンド実行と `echo ok` のような
// 無効果のコマンドを交互に打ち続け、空ループに入ることがある。どちらも実行しても何も起きないと静的に判定できるので、
// ターンを浪費する前に止め、正しい待ち方を理由文で返す。
//
// 方針:
//   - コマンド全体が無効果のときだけ拒否する。1 セグメントでも効果のあるコマンドが混ざれば通す。
//   - 前景・バックグラウンドのどちらでも拒否する。run_in_background（Claude Code だけが渡す）は理由文を変えるためだけに見る。
//   - 変数展開・コマンド置換・リダイレクト・前置の代入を含むセグメントは「効果あり」とみなす。
//
// 判定の穴（意図的）: 一次ゲートを語で張っているので、`:` だけのコマンドは素通りする。
// echo や sleep と組み合わさった時点で捕まるので許容する。
package idlewaitguard

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
	py "github.com/HappyOnigiri/hhx/internal/pycompat"
)

// Name は hook の名前である。
const Name = "idle-wait-guard"

// Definition は idle-wait-guard の定義を返す。
func Definition() hookrt.Definition {
	return hookrt.Definition{
		Name:           Name,
		DefaultEnabled: true,
		Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PreToolUse", Matcher: "Bash"},
			{Agent: hookrt.Codex, Event: "PreToolUse", Matcher: "Bash"},
		},
		Gate: gate,
		Run:  run,
	}
}

// gateKeywords は一次ゲートの語である。無効果のコマンドの語を 1 つも含まない入力は判定しない。
var gateKeywords = []string{"sleep", "echo", "printf", "true", "false", "wait"}

func gate(input []byte) bool {
	raw := string(input)
	for _, keyword := range gateKeywords {
		if strings.Contains(raw, keyword) {
			return true
		}
	}
	return false
}

func run(c *hookrt.Context) error {
	var command string
	background := false
	if c.FromArgs {
		// デバッグ経路では run_in_background を渡せないので、前景として扱う。
		command = string(c.Input)
	} else {
		command, background = commandAndBackground(c.Input)
	}
	if command == "" {
		return nil
	}
	label := classify(command)
	if label == "" {
		return nil
	}
	c.Deny(reason(c.Language(), label, background, py.QuoteJSON(command)))
	return nil
}

// commandAndBackground は payload から command と run_in_background を取り出す。
// run_in_background は JSON の true のときだけ真とする（省略時はキー自体が無い）。
func commandAndBackground(raw []byte) (string, bool) {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", false
	}
	toolInput, ok := payload["tool_input"].(map[string]any)
	if !ok {
		return "", false
	}
	// 文字列でない command は、移植元では判定の途中で例外になり無出力で終わっていた。
	command, _ := toolInput["command"].(string)
	background, _ := toolInput["run_in_background"].(bool)
	return command, background
}

// noopCommands は、実行しても状態が変わらず出力も引数どおりに決まるコマンドである。
var noopCommands = map[string]bool{
	"sleep": true, "echo": true, "printf": true, "true": true, "false": true, "wait": true, ":": true,
}

var (
	// シェルのコマンド区切り。改行も区切りとして扱う。
	separatorRE = regexp.MustCompile(`\|\||&&|[;|&\n]`)
	// 効果を持ちうる構文（$ の変数展開・コマンド置換、バッククォート、<> のリダイレクト）。
	effectSyntaxRE = regexp.MustCompile("[$`<>]")
	groupingRE     = regexp.MustCompile(`[(){}]`)
)

// segmentHead はセグメントで実行するコマンド名を返す。空のセグメントなら ok が偽になる。
// グルーピングの括弧だけを落とし、中身の先頭のトークンを見る。
func segmentHead(segment string) (head string, ok bool) {
	tokens := py.Fields(groupingRE.ReplaceAllString(segment, " "))
	if len(tokens) == 0 {
		return "", false
	}
	return tokens[0], true
}

// segmentIsNoop は 1 セグメントが「実行しても何も起きない」かを返す。
// 空のセグメント（区切りの前後）は真を返し、「全セグメントが無効果」の判定に影響させない。
func segmentIsNoop(segment string) bool {
	if effectSyntaxRE.MatchString(segment) {
		return false
	}
	head, ok := segmentHead(segment)
	if !ok {
		return true
	}
	// VAR=value cmd の形の前置の代入は環境を変える。
	if strings.Contains(head, "=") {
		return false
	}
	return noopCommands[head]
}

// classify はコマンド全体を見て、拒否するなら理由文の系統（WAIT か NOOP）を、通すなら空文字列を返す。
func classify(command string) string {
	segments := separatorRE.Split(command, -1)
	nonEmpty := false
	for _, segment := range segments {
		if py.Strip(segment) != "" {
			nonEmpty = true
		}
		if !segmentIsNoop(segment) {
			return ""
		}
	}
	if !nonEmpty {
		return ""
	}
	for _, segment := range segments {
		if head, ok := segmentHead(segment); ok && head == "sleep" {
			return waitLabel
		}
	}
	return noopLabel
}
