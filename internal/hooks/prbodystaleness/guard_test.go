package prbodystaleness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HappyOnigiri/hhx/internal/config"
	"github.com/HappyOnigiri/hhx/internal/hooktest"
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
	want := "push が成功した。PR #10088 の本文は、以下 1 件のコミットより前に書かれている。\n" +
		"  - feat: 交換のメモを後から入力できるようにする\n" +
		"- 本文の記述とこれらのコミットの内容が食い違っていないか確認すること" +
		"（本文に無い挙動が増えていないか、本文が「やる」と書いた内容が変わっていないか）。\n" +
		"- 食い違いがあれば update-pr スキル（プロジェクトに `local-*` 版があればそちら）で本文を更新する。" +
		"差分と一致していれば更新は不要で、その旨だけ報告すればよい。\n" +
		"- PR: https://github.com/o/r/pull/10088"
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
	if !strings.Contains(context, "\n- 食い違いがあれば `gh pr edit` で本文を直す。差分と一致していれば") || strings.Contains(context, "update-pr") {
		t.Fatalf("the instruction was not replaced:\n%s", context)
	}
	for _, config := range []string{
		"hooks:\n  pr-body-staleness:\n    update-instruction: \"  \"\n",
		"hooks:\n  pr-body-staleness:\n    update-instruction: [a]\n",
		"hooks:\n  pr-body-staleness:\n    enabled: true\n",
	} {
		context, _ := f.run(t, call{config: config})
		if !strings.Contains(context, defaultUpdateInstruction) {
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
	if !strings.Contains(context, "以下 7 件の") || !strings.Contains(context, "  - c6\n  - c5\n  - c4\n  - c3\n  - c2\n  - (ほか 2 件)\n") {
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
		"float number":        {map[string]any{"number": 12.9}, "PR #12 の本文"},
		"no number":           {map[string]any{"number": nil}, "PR #0 の本文"},
		"no url":              {map[string]any{"url": nil}, "- PR: "},
		"missing parents":     {map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after, "messageHeadline": "m"}}}}}, "  - m\n"},
		"missing headline":    {map[string]any{"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"committedDate": after}}}}}, "  - \n"},
		"empty commit":        {map[string]any{"commits": map[string]any{"nodes": []any{nil, map[string]any{}, commit(after, "x", 1)}}}, "  - x\n"},
		"bool parent count":   {map[string]any{"commits": map[string]any{"nodes": []any{commit(after, "b", 1)}}}, "  - b\n"},
		"fractional seconds":  {map[string]any{"lastEditedAt": "2026-09-10T07:10:57.999Z"}, "以下 1 件"},
		"sixth headline type": {map[string]any{"commits": map[string]any{"nodes": sixCommitsWithListFirst()}}, "(ほか 1 件)"},
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
