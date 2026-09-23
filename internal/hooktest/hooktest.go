// Package hooktest は、hook のテストが実運用と同じ経路（hookrt.Run に stdin の payload か argv を渡す）で
// 判定を確かめるための補助である。テストからだけ使う。
package hooktest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HappyOnigiri/hhx/internal/config"
	"github.com/HappyOnigiri/hhx/internal/hookrt"
)

// Deny は Result.Decision の拒否である。通したときの Decision は空文字列になる。
const Deny = "deny"

// Result は hook を 1 回起動した結果である。
type Result struct {
	// Decision は permissionDecision で、無出力なら空文字列である。
	Decision string
	Reason   string
}

// Options は起動の条件である。零値は「設定ファイルが無い」状態で stdin から起動する。
type Options struct {
	// Config は設定ファイルの中身（YAML）である。空なら設定ファイルが無いものとして既定値で動かす。
	Config string
	// Args は `hhx hook <name>` より後ろの引数である。空でなければ stdin を使わない（デバッグ経路）。
	Args []string
}

// Run は definition を stdin の raw で起動し、出力を検査して判定を返す。
// 出力は PreToolUse の hookSpecificOutput だけを持つ JSON であることを確かめる。
func Run(t *testing.T, definition hookrt.Definition, raw string, options Options) Result {
	t.Helper()
	var cfg *config.Config
	if options.Config != "" {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(options.Config), 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		cfg = loaded
	}
	var out bytes.Buffer
	hookrt.Run(&definition, hookrt.Invocation{
		Args:       options.Args,
		Stdin:      strings.NewReader(raw),
		Stdout:     &out,
		LoadConfig: func() (*config.Config, error) { return cfg, nil },
	})
	return parse(t, out.Bytes())
}

// Stdin は definition を stdin の raw で、設定ファイルの無い状態で起動する。
func Stdin(t *testing.T, definition hookrt.Definition, raw string) Result {
	t.Helper()
	return Run(t, definition, raw, Options{})
}

// Argv は definition をデバッグ経路（`hhx hook <name> '<arg>'`）で起動する。
func Argv(t *testing.T, definition hookrt.Definition, arg string) Result {
	t.Helper()
	return Run(t, definition, "", Options{Args: []string{arg}})
}

func parse(t *testing.T, output []byte) Result {
	t.Helper()
	if len(bytes.TrimSpace(output)) == 0 {
		return Result{}
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(output, &data); err != nil {
		t.Fatalf("output is not JSON: %q", output)
	}
	if len(data) != 1 || data["hookSpecificOutput"] == nil {
		t.Fatalf("output must have only hookSpecificOutput: %q", output)
	}
	var specific struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	}
	if err := json.Unmarshal(data["hookSpecificOutput"], &specific); err != nil {
		t.Fatalf("hookSpecificOutput: %v", err)
	}
	if specific.HookEventName != "PreToolUse" {
		t.Fatalf("hookEventName=%q, want PreToolUse", specific.HookEventName)
	}
	return Result{Decision: specific.PermissionDecision, Reason: specific.PermissionDecisionReason}
}

// BashPayload は Claude Code が Bash の PreToolUse で渡す payload を作る（互換スイートの run_hook と同じ形）。
// toolInput に追加する項目（run_in_background など）は extra で渡す。
func BashPayload(command, cwd string, extra map[string]any) string {
	toolInput := map[string]any{"command": command}
	for key, value := range extra {
		toolInput[key] = value
	}
	return ToolPayload("Bash", toolInput, cwd)
}

// ToolPayload は tool_name と tool_input を持つ PreToolUse の payload を作る。
func ToolPayload(toolName string, toolInput map[string]any, cwd string) string {
	data, err := json.Marshal(map[string]any{
		"session_id":      "test-session",
		"transcript_path": "/dev/null",
		"hook_event_name": "PreToolUse",
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"cwd":             cwd,
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}

// Case は表形式のテストの 1 行である。Label が空でなければ、拒否の理由にそれが含まれることも確かめる。
type Case struct {
	Command string
	Label   string
}

// CheckTable は cases の各コマンドを Bash の payload で起動し、判定が want（Deny か空文字列）であることを確かめる。
func CheckTable(t *testing.T, definition hookrt.Definition, want, cwd string, cases []Case) {
	t.Helper()
	for _, testCase := range cases {
		got := Stdin(t, definition, BashPayload(testCase.Command, cwd, nil))
		if got.Decision != want {
			t.Errorf("command %q: decision=%q, want %q (reason: %q)", testCase.Command, got.Decision, want, got.Reason)
			continue
		}
		if testCase.Label != "" && !strings.Contains(got.Reason, testCase.Label) {
			t.Errorf("command %q: reason %q does not contain %q", testCase.Command, got.Reason, testCase.Label)
		}
	}
}

// Commands は、ラベルを確かめない Case の並びを作る。
func Commands(commands ...string) []Case {
	cases := make([]Case, len(commands))
	for index, command := range commands {
		cases[index] = Case{Command: command}
	}
	return cases
}
