// Package exitplansubagentguard は、バックグラウンドで起動したエージェントの結果を待たずにプランを確定する経路を
// PreToolUse で拒否する hook（exit-plan-subagent-guard）である。
//
// 承認後に結果が届いてプランを直す事故が繰り返し起きたので、「待つ」「明示的に停止する」のどちらかを必ず通す。
// ask では判断がユーザーへ移るだけなので deny にし、逃げ道（TaskStop してから確定する）は理由文で案内する。
// 判定材料は transcript（JSONL）だけで、取れないとき（transcript が無い・読めない、payload が壊れている）は通す（fail-open）。
//
// 判定:
//   - 起動: Agent（旧名 Task）の tool_use に対応する tool_result に launchMarker と "agentId: <id>" があるもの。
//     SendMessage の tool_result にある再開の文面も起動として扱い、その id は行の順に終了扱いから外す。
//     対応する tool_use まで見るのは、Bash の出力やファイルの中身に同じ文面が現れるためである
//     （transcript やこの hook を調べると実際に起き、実在しないエージェントで ExitPlanMode が恒久的に deny された）。
//   - 終了: 行に <task-notification> と終端状態の <status> があれば、その行の <task-id> をすべて終了とする。
//     行単位で見るので、message を持たない queue-operation 行でも成立する。TaskStop の状態は killed で、
//     これを終端に入れないと理由文が案内する逃げ道が塞がる。
//   - 未完了: 起動 − 終了。
//
// 判定の穴（意図的。移植元と同じ）:
//   - 同期実行のエージェントは起動に入らない。待つ対象が無いので正しい。
//   - sidechain の行は除く。subagent が孫を起動しても、親のプラン確定を止める理由にはならない。
//   - marker はどれも実物の文面に依存する。文面が変われば黙って拾わなくなる（fail-open 側）。
//   - 終了通知の生の文面を別経路（ファイルの中身など）で読むと終了扱いになる。fail-open 側。
//   - 起動の tool_use 行が残っていなければ拾えない。
//
// 一次ゲートは payload に ExitPlanMode の語があるかだけを見て、tool_name は見ない（matcher の取り違えに対する保険）。
// 語を含む payload なら、どのツールでも判定する。
// デバッグ経路（`hhx hook exit-plan-subagent-guard <transcript.jsonl>`）の引数は transcript のパスで、ゲートを通さない。
//
// 移植元（Python）の値の扱いに合わせている点: transcript は UTF-8 として読んで不正なバイトを置換し、改行は \n・\r・\r\n の
// どれでも区切る（Python の open の既定）。移植元で例外になって無出力で終わっていた形（真で dict でない message や input、
// 配列やオブジェクトの id）は無出力にする。id の一致は Python の == に合わせ、true と 1 と 1.0 を同じとみなす。
// 再現していない違い: JSON の NaN・Infinity（Python は読めるが、ここでは行ごと読み飛ばす）、対の無いサロゲートの
// エスケープ（Python は理由文の出力で例外になって無出力で通すが、ここでは U+FFFD にして拒否する）、
// Python の再帰の上限を超える深い入れ子。どれも Claude Code の transcript には現れない形か、拒否の側に倒れる形である。
package exitplansubagentguard

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
	py "github.com/HappyOnigiri/hhx/internal/pycompat"
)

// Name は hook の名前である。
const Name = "exit-plan-subagent-guard"

// Definition は exit-plan-subagent-guard の定義を返す。Codex には ExitPlanMode が無いので Claude Code にだけ登録する。
func Definition() hookrt.Definition {
	return hookrt.Definition{
		Name:           Name,
		DefaultEnabled: true,
		Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PreToolUse", Matcher: "ExitPlanMode"},
		},
		Gate:          gate,
		GateStdinOnly: true,
		Run:           run,
	}
}

func gate(input []byte) bool {
	return bytes.Contains(input, []byte("ExitPlanMode"))
}

const (
	launchMarker = "Async agent launched successfully"
	// TaskStop で止めたエージェントを SendMessage で再開すると、launchMarker を伴わずに走り出す。
	resumeMarker       = "resumed from transcript in the background"
	notificationMarker = "<task-notification>"
)

var (
	agentIDRE = regexp.MustCompile(`agentId: ([0-9a-zA-Z]{6,})`)
	// 再開の本文は JSON 文字列の中に JSON が入った形で、デコードした後も id の引用符は \" のまま残ることがある。
	resumedIDRE = regexp.MustCompile(`Agent [\\"']*([0-9a-zA-Z]{6,})[\\"']* had no active task; resumed from transcript`)
	taskIDRE    = regexp.MustCompile(`<task-id>([0-9a-zA-Z]{6,})</task-id>`)
	// 終端状態。running / queued のような継続中の状態は含めない。
	terminalStatusRE = regexp.MustCompile(`<status>(?:completed|stopped|killed|failed|error|cancelled)</status>`)
)

// errPythonException は、移植元では例外になって無出力で終わっていた入力を表す。hookrt はエラーを無出力にする。
var errPythonException = errors.New("the Python implementation raises on this input")

func run(c *hookrt.Context) error {
	var path string
	if c.FromArgs {
		path = string(c.Input)
	} else {
		// 移植元は stdin を UTF-8 として厳密に読むので、不正なバイト列は読み込みの例外で無出力になっていた。
		if !utf8.Valid(c.Input) {
			return nil
		}
		path = transcriptPath(c.Input)
	}
	if path == "" {
		return nil
	}
	pending, err := scanFile(path)
	if err != nil || len(pending) == 0 {
		return err
	}
	c.Deny(reason(c.Language(), pending))
	return nil
}

// transcriptPath は payload の transcript_path を返す。payload が壊れている・オブジェクトでない・文字列でなければ空を返す。
func transcriptPath(raw []byte) string {
	payload, ok := decodeJSON(string(raw)).(map[string]any)
	if !ok {
		return ""
	}
	path, _ := payload["transcript_path"].(string)
	return path
}

// decodeJSON は text 全体を 1 つの JSON の値として読む。壊れていれば nil を返す（JSON の null も nil になるが、どちらも dict ではない）。
// 数値は json.Number のまま持ち、Python と同じく桁の大きい整数でも失敗させない。
func decodeJSON(text string) any {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	// 値の後ろに空白以外が残っていれば、Python の json.loads と同じく壊れているとみなす。
	if _, err := decoder.Token(); err != io.EOF {
		return nil
	}
	return value
}

// pendingAgent は結果が返っていないエージェントである。
type pendingAgent struct {
	id, description string
}

// scanFile は path の transcript を読んで未完了のエージェントを返す。読めなければ（判定を諦めて通すので）空を返す。
// 行の長さに上限を設けずに読む。bufio.Scanner の既定の上限（64KB）で止まると、後ろの終了通知を読まずに誤爆する。
func scanFile(path string) ([]pendingAgent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	state := newScanState()
	for {
		chunk, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, nil
		}
		// \n はどの多バイト文字の一部にもならないので、\n ごとに置換しても、ファイル全体を置換したときと同じ文字列になる。
		for _, line := range splitUniversalNewlines(py.DecodeUTF8Replace(chunk)) {
			if err := state.line(line); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF {
			return state.pending(), nil
		}
	}
}

// splitUniversalNewlines は Python の open（newline=None）の readlines と同じく、\n・\r・\r\n のどれでも行を区切り、
// 区切りを \n に置き換えて各行の末尾に残す。最後の行に区切りが無ければそのまま返す。
func splitUniversalNewlines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '\n':
			lines = append(lines, text[start:index+1])
			start = index + 1
		case '\r':
			lines = append(lines, text[start:index]+"\n")
			if index+1 < len(text) && text[index+1] == '\n' {
				index++
			}
			start = index + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// scanState は transcript を 1 回走査して「起動 − 終了」を求める途中の状態である。
// 起動と終了は同じ transcript にあり、tool_use は対応する tool_result より前の行に来るので 1 回の走査で差分が取れる。
type scanState struct {
	// launched は起動したエージェントの id を検出した順に持つ（Python の dict の挿入順）。説明は後の起動で上書きする。
	launched     []string
	descriptions map[string]string
	// described・agentCalls・messageCalls の鍵は tool_use の id を hashKey で表したものである。
	described    map[string]string
	agentCalls   map[string]bool
	messageCalls map[string]bool
	finished     map[string]bool
}

func newScanState() *scanState {
	return &scanState{
		descriptions: map[string]string{},
		described:    map[string]string{},
		agentCalls:   map[string]bool{},
		messageCalls: map[string]bool{},
		finished:     map[string]bool{},
	}
}

func (s *scanState) launch(id, description string) {
	if _, ok := s.descriptions[id]; !ok {
		s.launched = append(s.launched, id)
	}
	s.descriptions[id] = description
}

// line は transcript の 1 行を読む。JSON のパースは marker を含む行だけに絞る（transcript は数 MB になり得る）。
// 部分一致と終了通知の判定は生の行に対して行い、起動と再開の id はパースした本文から取る。
func (s *scanState) line(line string) error {
	hasLaunch := strings.Contains(line, launchMarker) || strings.Contains(line, resumeMarker)
	hasNotification := strings.Contains(line, notificationMarker) && terminalStatusRE.MatchString(line)
	hasAgentCall := strings.Contains(line, `"tool_use"`) && (strings.Contains(line, `"Agent"`) ||
		strings.Contains(line, `"Task"`) || strings.Contains(line, `"SendMessage"`))
	if !hasLaunch && !hasNotification && !hasAgentCall {
		return nil
	}
	entry, ok := decodeJSON(line).(map[string]any)
	if !ok {
		// 壊れた行は読み飛ばし、他の行の判定を続ける。
		return nil
	}
	if sidechain, _ := entry["isSidechain"].(bool); sidechain {
		return nil
	}
	if hasNotification {
		for _, match := range taskIDRE.FindAllStringSubmatch(line, -1) {
			s.finished[match[1]] = true
		}
	}
	message, err := orEmptyDict(entry["message"])
	if err != nil {
		return err
	}
	blocks, _ := message["content"].([]any)
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if isString(block["type"], "tool_use") {
			if err := s.toolUse(block); err != nil {
				return err
			}
			continue
		}
		if !isString(block["type"], "tool_result") {
			continue
		}
		if err := s.toolResult(block); err != nil {
			return err
		}
	}
	return nil
}

func (s *scanState) toolUse(block map[string]any) error {
	isMessage := isString(block["name"], "SendMessage")
	if !isMessage && !isString(block["name"], "Agent") && !isString(block["name"], "Task") {
		return nil
	}
	key, ok := hashKey(block["id"])
	if !ok {
		// 移植元は id を set に入れるので、配列やオブジェクトの id は TypeError になる。
		return errPythonException
	}
	if isMessage {
		s.messageCalls[key] = true
		return nil
	}
	s.agentCalls[key] = true
	input, err := orEmptyDict(block["input"])
	if err != nil {
		return err
	}
	if description, _ := input["description"].(string); description != "" {
		s.described[key] = description
	}
	return nil
}

func (s *scanState) toolResult(block map[string]any) error {
	origin, ok := hashKey(block["tool_use_id"])
	if !ok {
		return errPythonException
	}
	fromAgent, fromMessage := s.agentCalls[origin], s.messageCalls[origin]
	if !fromAgent && !fromMessage {
		return nil
	}
	text := resultText(block)
	if fromAgent && strings.Contains(text, launchMarker) {
		for _, match := range agentIDRE.FindAllStringSubmatch(text, -1) {
			s.launch(match[1], s.described[origin])
		}
	}
	if !fromMessage {
		return nil
	}
	// 再開は行の順に潰す。前回の終了通知より後に走り出しているので、待つ対象へ戻す。
	for _, match := range resumedIDRE.FindAllStringSubmatch(text, -1) {
		if _, ok := s.descriptions[match[1]]; !ok {
			s.launch(match[1], "")
		}
		delete(s.finished, match[1])
	}
	return nil
}

// pending は未完了のエージェントを、説明で安定ソートして返す（Python の sorted と同じ順）。
func (s *scanState) pending() []pendingAgent {
	var pending []pendingAgent
	for _, id := range s.launched {
		if !s.finished[id] {
			pending = append(pending, pendingAgent{id: id, description: s.descriptions[id]})
		}
	}
	// Python の str の比較はコードポイント順で、UTF-8 のバイト順と同じである。
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].description < pending[j].description })
	return pending
}

// resultText は tool_result の本文を返す。content は文字列とブロックの配列の両方があり、配列なら text を連結する。
func resultText(block map[string]any) string {
	switch content := block["content"].(type) {
	case string:
		return content
	case []any:
		var text strings.Builder
		for _, item := range content {
			if part, ok := item.(map[string]any); ok {
				if value, ok := part["text"].(string); ok {
					text.WriteString(value)
				}
			}
		}
		return text.String()
	default:
		return ""
	}
}

func isString(value any, want string) bool {
	text, ok := value.(string)
	return ok && text == want
}

// orEmptyDict は Python の (value or {}).get(...) の前半を再現する。偽の値は空の dict として扱い、
// 真で dict でない値は、移植元では .get の AttributeError になっていたのでエラーを返す。
func orEmptyDict(value any) (map[string]any, error) {
	if !truthy(value) {
		return nil, nil
	}
	dict, ok := value.(map[string]any)
	if !ok {
		return nil, errPythonException
	}
	return dict, nil
}

// truthy は JSON の値の Python での真偽を返す。
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case json.Number:
		key, _ := numberKey(v)
		return key != "n:0"
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

// hashKey は JSON の値を、Python の set・dict の鍵としての同一性を保つ文字列にする。
// Python では true == 1 == 1.0、false == 0 == -0.0 で、整数と浮動小数点数は値そのもので比べる。
// 配列とオブジェクトは鍵にできない（移植元では TypeError）ので ok が偽になる。
func hashKey(value any) (key string, ok bool) {
	switch v := value.(type) {
	case nil:
		return "none", true
	case bool:
		if v {
			return "n:1", true
		}
		return "n:0", true
	case json.Number:
		return numberKey(v)
	case string:
		return "s:" + v, true
	default:
		return "", false
	}
}

// numberKey は JSON の数値を、Python が読んだ値（小数点も指数も無ければ int、あれば float）の正確な値で表す。
func numberKey(number json.Number) (string, bool) {
	text := string(number)
	if !strings.ContainsAny(text, ".eE") {
		integer, ok := new(big.Int).SetString(text, 10)
		if !ok {
			return "", false
		}
		return "n:" + integer.String(), true
	}
	// 範囲外の値は Python と同じく ±inf（または 0）になる。構文は json のデコーダが確かめ済みなので、エラーは範囲外だけである。
	value, _ := strconv.ParseFloat(text, 64)
	if math.IsInf(value, 0) {
		return "inf:" + strconv.FormatBool(value > 0), true
	}
	rational := new(big.Rat).SetFloat64(value)
	if rational.IsInt() {
		return "n:" + rational.Num().String(), true
	}
	return "n:" + rational.String(), true
}
