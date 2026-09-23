package prbodystaleness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HappyOnigiri/hhx/internal/config"
	"github.com/HappyOnigiri/hhx/internal/hookrt"
	"github.com/HappyOnigiri/hhx/internal/hooktest"
	"github.com/HappyOnigiri/hhx/internal/i18n"
)

func TestMain(m *testing.M) {
	os.Exit(hooktest.Main(m))
}

const (
	written = "2026-09-10T07:10:57Z"
	before  = "2026-09-10T07:06:57Z"
	after   = "2026-09-10T07:36:15Z"
)

func commit(date, headline string, parents int) map[string]any {
	if headline == "" {
		headline = "feat: 交換のメモを後から入力できるようにする"
	}
	return map[string]any{"commit": map[string]any{"committedDate": date, "messageHeadline": headline,
		"parents": map[string]any{"totalCount": parents}}}
}

func pullRequest(over map[string]any) map[string]any {
	data := map[string]any{
		"number":       10088,
		"url":          "https://github.com/o/r/pull/10088",
		"state":        "OPEN",
		"lastEditedAt": written,
		"createdAt":    before,
		"commits":      map[string]any{"nodes": []any{commit(before, "", 1), commit(after, "", 1)}},
	}
	for key, value := range over {
		data[key] = value
	}
	return data
}

func graphql(nodes ...any) map[string]any {
	return map[string]any{"data": map[string]any{"repository": map[string]any{"object": map[string]any{
		"associatedPullRequests": map[string]any{"nodes": nodes}}}}}
}

type fixture struct {
	gh                      *hooktest.FakeGH
	github, other, notARepo string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := hooktest.TempDir(t)
	f := &fixture{
		gh:       hooktest.NewFakeGH(t),
		github:   hooktest.GitRepo(t, filepath.Join(root, "github"), "https://github.com/o/r.git", true),
		other:    hooktest.GitRepo(t, filepath.Join(root, "gitlab"), "git@gitlab.com:owner/repo.git", true),
		notARepo: filepath.Join(root, "plain"),
	}
	if err := os.Mkdir(f.notARepo, 0o755); err != nil {
		t.Fatal(err)
	}
	f.gh.Write(t, "graphql.json", graphql(pullRequest(nil)))
	return f
}

type call struct {
	command  string
	response any
	cwd      string
	event    string
	config   string
}

func (f *fixture) run(t *testing.T, options call) (string, bool) {
	t.Helper()
	if options.command == "" {
		options.command = "git push origin HEAD"
	}
	if options.cwd == "" {
		options.cwd = f.github
	}
	event := options.event
	if event == "" {
		event = "PostToolUse"
	}
	data := map[string]any{
		"session_id":      "test-session",
		"transcript_path": "/dev/null",
		"hook_event_name": event,
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": options.command},
		"cwd":             options.cwd,
	}
	if event == "PostToolUse" {
		data["tool_response"] = map[string]any{}
		if options.response != nil {
			data["tool_response"] = options.response
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return runRaw(t, string(raw), event, options.config)
}

func runRaw(t *testing.T, raw, event, config string) (string, bool) {
	t.Helper()
	injection := hooktest.ParseInjection(t, hooktest.Output(t, Definition(), raw, hooktest.Options{Config: config}))
	if injection.SystemMessage != "" {
		t.Fatalf("unexpected systemMessage: %q", injection.SystemMessage)
	}
	if injection.Event == "" {
		return "", false
	}
	if injection.Event != event {
		t.Fatalf("hookEventName=%q, want %q", injection.Event, event)
	}
	return injection.Context, true
}

func TestCommitAfterTheBodyInjectsAWarning(t *testing.T) {
	f := newFixture(t)
	context, ok := f.run(t, call{})
	if !ok {
		t.Fatal("a commit after the body must inject")
	}
	want := messages.Text(hooktest.Language, idStale, map[string]any{
		"Number": "10088", "Count": 1, "Commits": "  - feat: 交換のメモを後から入力できるようにする",
		"Instruction": messages.T(hooktest.Language, idDefaultInstruction), "URL": "https://github.com/o/r/pull/10088",
	})
	if context != want {
		t.Fatalf("context=\n%s\nwant\n%s", context, want)
	}
	calls := f.gh.Calls(t)
	if len(calls) != 1 {
		t.Fatalf("gh calls: %+v", calls)
	}
	argv := strings.Join(calls[0].Argv, "\x00")
	head := hooktest.Git(t, f.github, "rev-parse", "HEAD")
	for _, part := range []string{"api\x00graphql\x00-f\x00query=", "-F\x00owner=o\x00-F\x00repo=r\x00-F\x00sha=" + head, "commits(last:50)"} {
		if !strings.Contains(argv, part) {
			t.Errorf("gh argv %q is missing %q", calls[0].Argv, part)
		}
	}
	if calls[0].Cwd != f.github {
		t.Errorf("gh ran in %q, want %q", calls[0].Cwd, f.github)
	}
}

func TestUpdateInstructionCanBeReplaced(t *testing.T) {
	f := newFixture(t)
	config := "hooks:\n  pr-body-staleness:\n    update-instruction: \"食い違いがあれば `gh pr edit` で本文を直す。\"\n"
	context, _ := f.run(t, call{config: config})
	if !strings.Contains(context, "\n- 食い違いがあれば `gh pr edit` で本文を直す。"+afterInstruction()) || strings.Contains(context, "update-pr") {
		t.Fatalf("the instruction was not replaced:\n%s", context)
	}
	for value, want := range map[string]string{
		// 文末の記号が無ければ、表示言語の句点を足す。
		"gh pr edit で本文を直す":                "\n- gh pr edit で本文を直す" + messages.T(hooktest.Language, idSentenceEnd) + afterInstruction(),
		"  gh pr edit で本文を直す！  ":           "\n- gh pr edit で本文を直す！" + afterInstruction(),
		"Update the body with gh pr edit.": "\n- Update the body with gh pr edit." + afterInstruction(),
	} {
		context, _ := f.run(t, call{config: "hooks:\n  pr-body-staleness:\n    update-instruction: \"" + value + "\"\n"})
		if !strings.Contains(context, want) {
			t.Errorf("update-instruction %q must end the sentence before the next one:\n%s", value, context)
		}
	}
	for _, config := range []string{
		"hooks:\n  pr-body-staleness:\n    update-instruction: \"  \"\n",
		"hooks:\n  pr-body-staleness:\n    update-instruction: [a]\n",
		"hooks:\n  pr-body-staleness:\n    enabled: true\n",
	} {
		context, _ := f.run(t, call{config: config})
		if !strings.Contains(context, messages.T(hooktest.Language, idDefaultInstruction)) {
			t.Errorf("config %q must fall back to the default instruction:\n%s", config, context)
		}
	}
	var settings Settings
	if err := decodeSettings(t, "update-instruction: x\nenabled: false\n", &settings); err != nil || settings.UpdateInstruction != "x" {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
}

func decodeSettings(t *testing.T, content string, settings *Settings) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	indented := "hooks:\n  pr-body-staleness:\n"
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		indented += "    " + line + "\n"
	}
	if err := os.WriteFile(path, []byte(indented), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	return cfg.Decode(Name, settings)
}

func TestBodyNeverEditedFallsBackToCreatedAt(t *testing.T) {
	f := newFixture(t)
	f.gh.Write(t, "graphql.json", graphql(pullRequest(map[string]any{"lastEditedAt": nil, "createdAt": before})))
	if _, ok := f.run(t, call{}); !ok {
		t.Fatal("createdAt must be used when the body was never edited")
	}
}

func TestOnlyTheNewerCommitsAreListed(t *testing.T) {
	f := newFixture(t)
	f.gh.Write(t, "graphql.json", graphql(pullRequest(map[string]any{"commits": map[string]any{"nodes": []any{
		commit(before, "fix: 本文に書いてある修正", 1), commit(after, "feat: 本文より後のコミット", 1),
	}}})))
	context, _ := f.run(t, call{})
	if !strings.Contains(context, "本文より後のコミット") || strings.Contains(context, "本文に書いてある修正") {
		t.Fatalf("context:\n%s", context)
	}
}

func TestManyCommitsAreSummarized(t *testing.T) {
	f := newFixture(t)
	var nodes []any
	for index := range 7 {
		nodes = append(nodes, commit(after, "c"+string(rune('0'+index)), 1))
	}
	f.gh.Write(t, "graphql.json", graphql(pullRequest(map[string]any{"commits": map[string]any{"nodes": nodes}})))
	context, _ := f.run(t, call{})
	more := messages.Text(hooktest.Language, idMoreCommits, map[string]any{"Count": 2})
	if !strings.Contains(context, countText(7)) || !strings.Contains(context, "  - c6\n  - c5\n  - c4\n  - c3\n  - c2"+more+"\n") {
		t.Fatalf("context:\n%s", context)
	}
}

func TestSilentCases(t *testing.T) {
	for name, testCase := range map[string]struct {
		graphql any
		options call
		mode    string
		noGH    bool
	}{
		"body newer than every commit": {graphql: graphql(pullRequest(map[string]any{"commits": map[string]any{"nodes": []any{commit(before, "", 1)}}}))},
		"merge commit alone": {graphql: graphql(pullRequest(map[string]any{"commits": map[string]any{"nodes": []any{
			commit(before, "", 1), commit(after, "Merge remote-tracking branch 'origin/main'", 2)}}}))},
		"no associated pull request": {graphql: graphql()},
		"closed pull request":        {graphql: graphql(pullRequest(map[string]any{"state": "MERGED"}))},
		"failed push":                {options: call{response: map[string]any{"stderr": "! [rejected]        main -> main (non-fast-forward)"}}, noGH: true},
		"up-to-date push":            {options: call{response: map[string]any{"stderr": "Everything up-to-date"}}, noGH: true},
		"pr create":                  {options: call{command: "gh pr create --fill"}, noGH: true},
		"ls":                         {options: call{command: "ls -la"}, noGH: true},
		"npm script":                 {options: call{command: "npm run push-notifications"}, noGH: true},
		"git status":                 {options: call{command: "git status"}, noGH: true},
		"dry run":                    {options: call{command: "git push --dry-run"}, noGH: true},
		"delete":                     {options: call{command: "git push origin :feature"}, noGH: true},
		"non-GitHub remote":          {options: call{cwd: "other"}, noGH: true},
		"outside a repository":       {options: call{cwd: "plain"}, noGH: true},
		"gh failure":                 {mode: "fail"},
		"gh garbage":                 {mode: "garbage"},
		"gh empty":                   {mode: "empty"},
		"PreToolUse":                 {options: call{event: "PreToolUse"}, noGH: true},
		"nodes not a list":           {graphql: map[string]any{"data": map[string]any{"repository": map[string]any{"object": map[string]any{"associatedPullRequests": map[string]any{"nodes": map[string]any{"a": 1}}}}}}},
		"missing object":             {graphql: map[string]any{"data": map[string]any{"repository": map[string]any{"object": nil}}}},
		"unreadable body time":       {graphql: graphql(pullRequest(map[string]any{"lastEditedAt": "", "createdAt": ""}))},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if testCase.graphql != nil {
				f.gh.Write(t, "graphql.json", testCase.graphql)
			}
			if testCase.mode != "" {
				f.gh.SetMode(t, testCase.mode)
			}
			switch testCase.options.cwd {
			case "other":
				testCase.options.cwd = f.other
			case "plain":
				testCase.options.cwd = f.notARepo
			}
			if context, ok := f.run(t, testCase.options); ok {
				t.Fatalf("must be silent, injected:\n%s", context)
			}
			if calls := f.gh.Calls(t); testCase.noGH && len(calls) != 0 {
				t.Fatalf("gh must not be called: %+v", calls)
			}
		})
	}
}

func TestGHHangDoesNotBlockForever(t *testing.T) {
	f := newFixture(t)
	f.gh.SetMode(t, "hang")
	t.Setenv("FAKE_GH_SLEEP", "60")
	started := time.Now()
	if _, ok := f.run(t, call{}); ok {
		t.Fatal("a hanging gh must be silent")
	}
	if elapsed := time.Since(started); elapsed > 12*time.Second {
		t.Fatalf("the hook waited %s, beyond its own timeout", elapsed)
	}
}

func TestStrangePayloadsAreSilent(t *testing.T) {
	f := newFixture(t)
	for _, raw := range []string{
		"not json but mentions push", "", "null", "[]", `"git push"`, "{}",
		`{"tool_input": "git push"}`,
		`{"tool_input": {"command": ["git", "push"]}}`,
		`{"tool_input": {"command": "git push"}, "cwd": 5}`,
		`{"tool_input": {"command": "git push"}, "hook_event_name": ["PostToolUse"], "cwd": "` + f.github + `"}`,
		`{"tool_input": {"command": "git push"}, "cwd": "` + f.github + `"} trailing`,
		"{\"tool_input\": {\"command\": \"git push\"}, \"cwd\": \"" + f.github + "\", \"x\": \"\xff\"}",
	} {
		if context, ok := runRaw(t, raw, "PostToolUse", ""); ok {
			t.Errorf("payload %q must be silent, injected:\n%s", raw, context)
		}
	}
	// hook_event_name が無ければ PostToolUse として扱う。
	if _, ok := runRaw(t, `{"tool_input": {"command": "git push"}, "cwd": "`+f.github+`"}`, "PostToolUse", ""); !ok {
		t.Error("a payload without the event name must be treated as PostToolUse")
	}
}

func TestStrangeGitHubResponsesAreSilent(t *testing.T) {
	for name, pr := range map[string]map[string]any{
		"string number":       {"number": "x"},
		"list headline":       {"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after, "messageHeadline": []any{"x"}}}}}},
		"string parent count": {"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after, "parents": map[string]any{"totalCount": "1"}}}}}},
		"string commit":       {"commits": map[string]any{"nodes": []any{map[string]any{"commit": "x"}}}},
		"string node":         {"commits": map[string]any{"nodes": []any{"x"}}},
		"string nodes":        {"commits": map[string]any{"nodes": "x"}},
		"list commits":        {"commits": []any{1}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.gh.Write(t, "graphql.json", graphql(pullRequest(pr)))
			if context, ok := f.run(t, call{}); ok {
				t.Fatalf("must be silent, injected:\n%s", context)
			}
		})
	}
}

func TestLooseGitHubResponsesFollowPython(t *testing.T) {
	for name, testCase := range map[string]struct {
		pr   map[string]any
		want string
	}{
		"float number":        {map[string]any{"number": 12.9}, "PR #12 "},
		"no number":           {map[string]any{"number": nil}, "PR #0 "},
		"no url":              {map[string]any{"url": nil}, "- PR: "},
		"missing parents":     {map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after, "messageHeadline": "m"}}}}}, "  - m\n"},
		"missing headline":    {map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after}}}}}, "  - \n"},
		"empty commit":        {map[string]any{"commits": map[string]any{"nodes": []any{nil, map[string]any{}, commit(after, "x", 1)}}}, "  - x\n"},
		"bool parent count":   {map[string]any{"commits": map[string]any{"nodes": []any{commit(after, "b", 1)}}}, "  - b\n"},
		"fractional seconds":  {map[string]any{"lastEditedAt": "2026-09-10T07:10:57.999Z"}, countText(1)},
		"sixth headline type": {map[string]any{"commits": map[string]any{"nodes": sixCommitsWithListFirst()}}, messages.Text(hooktest.Language, idMoreCommits, map[string]any{"Count": 1})},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.gh.Write(t, "graphql.json", graphql(pullRequest(testCase.pr)))
			context, ok := f.run(t, call{})
			if !ok || !strings.Contains(context, testCase.want) {
				t.Fatalf("context must contain %q:\n%s", testCase.want, context)
			}
		})
	}
}

// sixCommitsWithListFirst は、新しい順に並べたとき 6 件目（表示しない位置）に文字列でない見出しが来る commit の並びである。
// 移植元は表示する 5 件だけを連結するので、例外にならない。
func sixCommitsWithListFirst() []any {
	nodes := []any{map[string]any{"commit": map[string]any{"committedDate": after, "messageHeadline": []any{"x"}}}}
	for range 5 {
		nodes = append(nodes, commit(after, "ok", 1))
	}
	return nodes
}

func TestArgvUsesTheProcessDirectory(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.github)
	output := hooktest.Output(t, Definition(), "", hooktest.Options{Args: []string{"git push"}})
	if injection := hooktest.ParseInjection(t, output); !strings.Contains(injection.Context, "#10088") {
		t.Fatalf("argv must use the process directory: %q", output)
	}
	if output := hooktest.Output(t, Definition(), "", hooktest.Options{Args: []string{"git status"}}); output != "" {
		t.Fatalf("argv without a push must be silent: %q", output)
	}
}

func TestDisabledByConfig(t *testing.T) {
	f := newFixture(t)
	if context, ok := f.run(t, call{config: "hooks:\n  pr-body-staleness:\n    enabled: false\n"}); ok {
		t.Fatalf("a disabled hook must be silent:\n%s", context)
	}
	if calls := f.gh.Calls(t); len(calls) != 0 {
		t.Fatalf("a disabled hook must not call gh: %+v", calls)
	}
}

func TestTriggersPush(t *testing.T) {
	for _, command := range []string{
		"git push",
		"git push origin HEAD",
		"git push --force-with-lease origin feature",
		"git push origin HEAD:refs/heads/feature",
		"git -C /other/worktree push origin HEAD",
		"/usr/bin/git push",
		"make build && git push",
		"git commit -m 'x'; git push",
		"GIT PUSH",
		"git push 2>&1",
		"(git push)",
		"git\u3000push",
	} {
		if !triggersPush(command) {
			t.Errorf("%q must be a push", command)
		}
	}
	for _, command := range []string{
		"git push --dry-run origin HEAD",
		"git push -n origin HEAD",
		"git push origin --delete feature",
		"git push origin :feature",
		"gh pr create --fill",
		"git status",
		"echo push",
		"npm run push-image",
		"git status >push",
		"",
	} {
		if triggersPush(command) {
			t.Errorf("%q must not be a push", command)
		}
	}
}

// stale は GitHub の応答と同じく JSON を経由した値で staleCommits を呼ぶ（数値は json.Number になる）。
func stale(t *testing.T, over map[string]any) []any {
	t.Helper()
	data, err := json.Marshal(pullRequest(over))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := decodeJSON(string(data))
	newer, err := staleCommits(value.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	return newer
}

func TestStaleCommits(t *testing.T) {
	newer := stale(t, map[string]any{"commits": map[string]any{"nodes": []any{
		commit(before, "古い", 1), commit("2026-09-10T07:20:00Z", "中間", 1), commit(after, "最新", 1),
	}}})
	if len(newer) != 2 || newer[0] != "最新" || newer[1] != "中間" {
		t.Fatalf("newer=%v", newer)
	}
	if newer := stale(t, map[string]any{"commits": map[string]any{"nodes": []any{commit(after, "マージ", 2)}}}); len(newer) != 0 {
		t.Errorf("merge commits must be ignored: %v", newer)
	}
	if newer := stale(t, map[string]any{"lastEditedAt": "", "createdAt": ""}); len(newer) != 0 {
		t.Errorf("unreadable body times must give nothing: %v", newer)
	}
	if newer := stale(t, map[string]any{"commits": map[string]any{"nodes": []any{commit("", "壊れた日付", 1)}}}); len(newer) != 0 {
		t.Errorf("unreadable commit times must be ignored: %v", newer)
	}
	if newer := stale(t, map[string]any{"commits": nil}); len(newer) != 0 {
		t.Errorf("missing commits must give nothing: %v", newer)
	}
}

func TestParseTime(t *testing.T) {
	for value, want := range map[string]string{
		"2026-09-10T07:10:57Z":     "2026-09-10 07:10:57",
		"2026-09-10T07:10:57.500Z": "2026-09-10 07:10:57",
		"2026-09-10t07:10:57":      "2026-09-10 07:10:57",
		"2026-9-1T7:1:5":           "2026-09-01 07:01:05",
		"2026-09- 1T 7:00:00ZZ":    "2026-09-01 07:00:00",
		"٢٠٢٦-09-10T07:10:57":      "2026-09-10 07:10:57",
		"2024-02-29T00:00:00":      "2024-02-29 00:00:00",
	} {
		got, ok := parseTime(value)
		if !ok || got.Format("2006-01-02 15:04:05") != want {
			t.Errorf("parseTime(%q)=%v %v, want %s", value, got, ok, want)
		}
	}
	for _, value := range []any{nil, "", "yesterday", 17, "2026-02-30T00:00:00", "2026-09-10T07:10:60", "0000-01-01T00:00:00",
		"2026-09-10T07:10:57+09:00", "2026-13-01T00:00:00", "2026-09-10 07:10:57", "2026-09-10T24:00:00"} {
		if got, ok := parseTime(value); ok {
			t.Errorf("parseTime(%v)=%v, want unreadable", value, got)
		}
	}
}

func TestMatchOrigin(t *testing.T) {
	for _, url := range []string{
		"https://github.com/o/r.git", "https://github.com/o/r", "git@github.com:o/r.git",
		"ssh://git@github.com/o/r.git", "https://github.com/o/r/", "https://GitHub.COM/o/r.GIT", "é" + "github.com/o/r",
		"https://github.com/o/r.gıt",
	} {
		owner, repo, ok := matchOrigin(url)
		if !ok || owner != "o" || repo != "r" {
			t.Errorf("matchOrigin(%q)=%q %q %v", url, owner, repo, ok)
		}
	}
	for _, url := range []string{
		"git@gitlab.com:o/r.git", "https://notgithub.com/o/r.git", "/local/path/repo", "-github.com/o/r",
		"\u212agithub.com/o/r", "\u017fgithub.com/o/r", "https://github.com/o", "https://github.com/o/r/x",
	} {
		if owner, repo, ok := matchOrigin(url); ok {
			t.Errorf("matchOrigin(%q)=%q %q, want no match", url, owner, repo)
		}
	}
	// 境界の直後に一致しなくても、後ろの位置で一致すれば拾う（search の走査）。
	if owner, repo, ok := matchOrigin("xgithub.com/a/b github.com/c/d"); !ok || owner != "c" || repo != "d" {
		t.Errorf("a later match must be found: %q %q %v", owner, repo, ok)
	}
}

func TestGate(t *testing.T) {
	if !gate([]byte("git PUSH")) || gate([]byte("git status")) {
		t.Fatal("the gate must look for push case-insensitively")
	}
}

// afterInstruction は注入文のうち、本文の更新方法の案内の直後に続く文の書き出しである。
func afterInstruction() string {
	entry := messages[idStale]
	text := entry.EN
	if hooktest.Language == i18n.Japanese {
		text = entry.JA
	}
	_, after, _ := strings.Cut(text, "{{.Instruction}}")
	line, _, _ := strings.Cut(after, "\n")
	return line[:min(len(line), 10)]
}

// countText は注入文の 1 行目のうち、コミットの件数を含む部分である。
func countText(count int) string {
	line, _, _ := strings.Cut(messages.Text(hooktest.Language, idStale, map[string]any{
		"Number": "1", "Count": count, "Commits": "", "Instruction": "", "URL": "",
	}), "\n")
	_, after, _ := strings.Cut(line, "PR #1")
	return after
}

// 英語は日本語より長くなりやすい。見出しを上限まで並べても、Codex の additionalContextLimit に収まること。
func TestMessageFitsTheCodexContextLimit(t *testing.T) {
	limit := 0
	for _, registration := range Definition().Registrations {
		if registration.Agent == hookrt.Codex {
			limit = registration.AdditionalContextLimit
		}
	}
	if limit == 0 {
		t.Fatal("the Codex registration must set additionalContextLimit")
	}
	// GitHub のコミットの見出しは 1 行で、表示は長くても 100 文字ほどで切られる。見出しを長めに取って上限まで並べる。
	var headlines []any
	for range maxHeadlines + 3 {
		headlines = append(headlines, strings.Repeat("h", 120))
	}
	pr := map[string]any{"number": json.Number("123456"), "url": "https://github.com/" + strings.Repeat("o", 39) + "/" + strings.Repeat("r", 100) + "/pull/123456"}
	for _, language := range []i18n.Language{i18n.English, i18n.Japanese} {
		text, err := message(language, pr, headlines, messages.T(language, idDefaultInstruction))
		if err != nil {
			t.Fatal(err)
		}
		if len(text) > limit {
			t.Errorf("%s: %d bytes, over the limit %d", language, len(text), limit)
		}
	}
}
