package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
)

const testBinary = "/opt/hhx/bin/hhx"

// existingClaude は wx の readiness hook と利用者の hook が同居する、実機に近い設定である。
// hhx はこの中のどのグループにも触れてはいけない。
const existingClaude = `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$HOME/.local/bin/wx\" hook pre-tool-use"
          }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/my-guard.sh",
            "timeout": 5
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "\"$HOME/.local/bin/wx\" hook session-start"
          }
        ]
      }
    ]
  },
  "permissions": {
    "allow": [
      "Bash(ls:*)"
    ]
  }
}
`

// compactClaude は整形の規則と違う書式の設定である。hhx のエントリに変化が無ければ、書式も含めて触らない。
const compactClaude = `{"hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "wx hook pre-tool-use"}]}]}, "allow": ["a", "b"]}
`

func noop(*hookrt.Context) error { return nil }

func testDefinitions() []hookrt.Definition {
	return []hookrt.Definition{
		{Name: "alpha-guard", Run: noop, Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PreToolUse", Matcher: "Bash"},
			{Agent: hookrt.Codex, Event: "PreToolUse", Matcher: "Bash"},
		}},
		{Name: "beta-context", Run: noop, Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PostToolUse", Matcher: "Bash", Timeout: 15, StatusMessage: "Checking"},
			{Agent: hookrt.Codex, Event: "PostToolUse", Matcher: "Bash", Timeout: 15, AdditionalContextLimit: 4096},
		}},
		{Name: "gamma-guard", Run: noop, Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PreToolUse", Matcher: "Bash"},
			{Agent: hookrt.Codex, Event: "SessionStart"},
		}},
	}
}

func testOptions(t *testing.T) Options {
	t.Helper()
	home := t.TempDir()
	return Options{Home: home, Binary: testBinary, BackupDir: filepath.Join(home, "backups")}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustInstall(t *testing.T, options Options, agent hookrt.Agent, definitions []hookrt.Definition) Result {
	t.Helper()
	result, err := Install(options, agent, definitions)
	if err != nil {
		t.Fatalf("Install(%s): %v", agent, err)
	}
	return result
}

func mustUninstall(t *testing.T, options Options, agent hookrt.Agent) Result {
	t.Helper()
	result, err := Uninstall(options, agent)
	if err != nil {
		t.Fatalf("Uninstall(%s): %v", agent, err)
	}
	return result
}

type group struct {
	Matcher *string          `json:"matcher"`
	Hooks   []map[string]any `json:"hooks"`
}

func parseHooks(t *testing.T, content string) map[string][]group {
	t.Helper()
	var document struct {
		Hooks map[string][]group `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, content)
	}
	return document.Hooks
}

func commands(g group) []string {
	var out []string
	for _, hook := range g.Hooks {
		out = append(out, hook["command"].(string))
	}
	return out
}

func TestEmptyRegistryIsIdempotent(t *testing.T) {
	for _, agent := range hookrt.Agents() {
		t.Run(string(agent)+" without file", func(t *testing.T) {
			options := testOptions(t)
			path, _ := TargetPath(options.Home, agent)
			for range 2 {
				if result := mustInstall(t, options, agent, nil); result.Changed {
					t.Fatal("installing an empty registry must not change anything")
				}
				if result := mustUninstall(t, options, agent); result.Changed {
					t.Fatal("uninstalling without hhx entries must not change anything")
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("an empty registry must not create %s", path)
			}
		})
		for name, content := range map[string]string{"foreign groups": existingClaude, "compact layout": compactClaude} {
			t.Run(string(agent)+" with "+name, func(t *testing.T) {
				options := testOptions(t)
				path, _ := TargetPath(options.Home, agent)
				writeFile(t, path, content)
				for range 2 {
					if mustInstall(t, options, agent, nil).Changed || mustUninstall(t, options, agent).Changed {
						t.Fatal("nothing to change, but the file was rewritten")
					}
				}
				if got := readFile(t, path); got != content {
					t.Fatalf("foreign content changed:\n%s", got)
				}
			})
		}
	}
}

func TestInstallKeepsForeignGroupsAndIsIdempotent(t *testing.T) {
	options := testOptions(t)
	path, _ := TargetPath(options.Home, hookrt.Claude)
	writeFile(t, path, existingClaude)

	first := mustInstall(t, options, hookrt.Claude, testDefinitions())
	if !first.Changed || first.Backup == "" {
		t.Fatalf("first install must write and back up: %+v", first)
	}
	if backup := readFile(t, first.Backup); backup != existingClaude {
		t.Fatal("the backup must hold the content before the install")
	}
	installed := readFile(t, path)
	if second := mustInstall(t, options, hookrt.Claude, testDefinitions()); second.Changed {
		t.Fatal("reinstalling the same registry must not touch the file")
	}

	hooks := parseHooks(t, installed)
	pre := hooks["PreToolUse"]
	if len(pre) != 3 {
		t.Fatalf("PreToolUse must keep both foreign groups and add one hhx group, got %d", len(pre))
	}
	if got := commands(pre[0]); len(got) != 1 || got[0] != `"$HOME/.local/bin/wx" hook pre-tool-use` {
		t.Fatalf("the wx group moved or changed: %v", got)
	}
	if got := commands(pre[1]); len(got) != 1 || got[0] != "/usr/local/bin/my-guard.sh" {
		t.Fatalf("the user group changed: %v", got)
	}
	want := []string{testBinary + " hook alpha-guard", testBinary + " hook gamma-guard"}
	if got := commands(pre[2]); strings.Join(got, "|") != strings.Join(want, "|") || pre[2].Matcher == nil || *pre[2].Matcher != "Bash" {
		t.Fatalf("the hhx group must be a separate Bash group in registry order, got %v", got)
	}
	post := hooks["PostToolUse"]
	if len(post) != 1 || post[0].Hooks[0]["timeout"] != float64(15) || post[0].Hooks[0]["statusMessage"] != "Checking" {
		t.Fatalf("optional fields were not written: %+v", post)
	}
	if _, ok := post[0].Hooks[0]["additionalContextLimit"]; ok {
		t.Fatal("zero-valued optional fields must be omitted")
	}
	if strings.Contains(installed, "null") {
		t.Fatal("hhx must never write null")
	}

	mustUninstall(t, options, hookrt.Claude)
	if got := readFile(t, path); got != existingClaude {
		t.Fatalf("uninstall must restore the original bytes:\n%s", got)
	}
	if again := mustUninstall(t, options, hookrt.Claude); again.Changed {
		t.Fatal("uninstall must be idempotent")
	}
}

func TestInstallWritesCodexFields(t *testing.T) {
	options := testOptions(t)
	mustInstall(t, options, hookrt.Codex, testDefinitions())
	path, _ := TargetPath(options.Home, hookrt.Codex)
	hooks := parseHooks(t, readFile(t, path))
	post := hooks["PostToolUse"][0].Hooks[0]
	if post["additionalContextLimit"] != float64(4096) {
		t.Fatalf("additionalContextLimit missing: %v", post)
	}
	start := hooks["SessionStart"]
	if len(start) != 1 || start[0].Matcher != nil {
		t.Fatalf("an empty matcher must omit the key: %+v", start)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("a new file must be created with 0600: %v", err)
	}
	mustUninstall(t, options, hookrt.Codex)
	if got := readFile(t, path); got != "{}\n" {
		t.Fatalf("uninstall must fold the events hhx emptied, got %q", got)
	}
}

// TestInstallKeepsGroupPositions は Codex が位置で記録する信頼状態を守るための不変条件を確かめる。
func TestInstallKeepsGroupPositions(t *testing.T) {
	options := testOptions(t)
	path, _ := TargetPath(options.Home, hookrt.Codex)
	writeFile(t, path, `{
  "hooks": {
    "PreToolUse": [
      {"hooks": [{"type": "command", "command": "wx hook pre-tool-use"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/old/hhx hook retired-guard"}]},
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "mine.sh"}, {"type": "command", "command": "/old/hhx hook alpha-guard"}]}
    ]
  }
}
`)
	mustInstall(t, options, hookrt.Codex, testDefinitions())
	pre := parseHooks(t, readFile(t, path))["PreToolUse"]
	if len(pre) != 3 {
		t.Fatalf("want 3 groups, got %d", len(pre))
	}
	if got := commands(pre[1]); len(got) != 1 || got[0] != testBinary+" hook alpha-guard" {
		t.Fatalf("the hhx Bash group must stay at index 1, got %v", got)
	}
	if got := commands(pre[2]); len(got) != 1 || got[0] != "mine.sh" {
		t.Fatalf("hhx entries must be removed from a foreign group without touching the rest, got %v", got)
	}
}

func TestHookCommandQuotesPaths(t *testing.T) {
	tests := []struct {
		binary string
		want   string
		ok     bool
	}{
		{binary: "/Users/me/.local/bin/hhx", want: "/Users/me/.local/bin/hhx hook x", ok: true},
		{binary: "/Users/me/My Tools/hhx", want: `"/Users/me/My Tools/hhx" hook x`, ok: true},
		{binary: "/tmp/it's/hhx", want: `"/tmp/it's/hhx" hook x`, ok: true},
		{binary: "relative/hhx"},
		{binary: "/tmp/$HOME/hhx"},
		{binary: "/tmp/a\"b/hhx"},
	}
	for _, test := range tests {
		got, err := HookCommand(test.binary, "x")
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("HookCommand(%q)=(%q, %v), want %q ok=%v", test.binary, got, err, test.want, test.ok)
		}
	}
}

func TestIsHHXHookCommand(t *testing.T) {
	for command, want := range map[string]bool{
		"/Users/me/.local/bin/hhx hook pr-merge-guard":   true,
		`"/Users/me/My Tools/hhx" hook pr-merge-guard`:   true,
		`"$HOME/.local/bin/hhx" hook pr-merge-guard`:     true,
		"hhx hook anything":                              true,
		`"$HOME/.local/bin/wx" hook pre-tool-use`:        false,
		"/Users/me/.local/bin/hhx install":               false,
		"/Users/me/hhx-tools/guard.sh hook x":            false,
		"hhx hook x; rm -rf /":                           false,
		`"$HOME/.codex/hooks/pr-merge-guard.py"`:         false,
		"/Users/me/.local/bin/hhx hook x\nrm -rf /":      false,
		"/Users/me/.local/bin/not-hhx hook pr-merge-foo": false,
	} {
		if got := isHHXHookCommand(command); got != want {
			t.Errorf("isHHXHookCommand(%q)=%v, want %v", command, got, want)
		}
	}
}

func TestClaudeIgnoresLocalSettings(t *testing.T) {
	options := testOptions(t)
	local := filepath.Join(options.Home, ".claude", "settings.local.json")
	shared := filepath.Join(options.Home, ".claude", "settings.json")
	writeFile(t, shared, "{}\n")
	writeFile(t, local, "{}\n")
	result := mustInstall(t, options, hookrt.Claude, testDefinitions())
	if result.Path != shared {
		t.Fatalf("Claude must write settings.json even when settings.local.json exists, wrote %s", result.Path)
	}
	if got := readFile(t, local); got != "{}\n" {
		t.Fatal("settings.local.json must stay untouched")
	}
}

func TestInstallWritesThroughSymlink(t *testing.T) {
	options := testOptions(t)
	real := filepath.Join(options.Home, "dotfiles", "settings.json")
	writeFile(t, real, "{\n    \"model\": \"opus\"\n}\n")
	link := filepath.Join(options.Home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	result := mustInstall(t, options, hookrt.Claude, testDefinitions())
	if resolved, _ := filepath.EvalSymlinks(real); result.Resolved != resolved {
		t.Fatalf("Resolved=%s, want %s", result.Resolved, resolved)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink must survive the install")
	}
	if !strings.HasPrefix(readFile(t, real), "{\n    \"model\": \"opus\",\n    \"hooks\"") {
		t.Fatalf("the original indentation must be kept:\n%s", readFile(t, real))
	}
}

func TestInstallRejectsUnsafeTargets(t *testing.T) {
	tests := map[string]string{
		"empty file":        "",
		"invalid JSON":      "{",
		"top level array":   "[]\n",
		"hooks not object":  `{"hooks": []}`,
		"event not array":   `{"hooks": {"PreToolUse": {}}}`,
		"duplicate key":     `{"hooks": {}, "hooks": {}}`,
		"multiple values":   "{}\n{}\n",
		"duplicate in hook": `{"hooks": {"PreToolUse": [], "PreToolUse": []}}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			options := testOptions(t)
			path, _ := TargetPath(options.Home, hookrt.Claude)
			writeFile(t, path, content)
			if _, err := Install(options, hookrt.Claude, testDefinitions()); err == nil {
				t.Fatal("Install must refuse the file")
			}
			if got := readFile(t, path); got != content {
				t.Fatal("a refused file must stay untouched")
			}
		})
	}
}

func TestInstallRequiresBinary(t *testing.T) {
	options := testOptions(t)
	options.Binary = ""
	if _, err := Install(options, hookrt.Claude, testDefinitions()); err == nil {
		t.Fatal("Install must fail without the hhx path")
	}
}
