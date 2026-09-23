package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HappyOnigiri/hhx/internal/waitci"
)

// scriptedRunner は git と gh の呼び出しを argv で振り分けて答える。gh の応答は argv の接頭辞ごとに順に返す。
type scriptedRunner struct {
	// branch が空なら detached HEAD、repo が偽なら git 管理下でない。
	repo   bool
	branch string
	head   string
	gh     map[string][]waitci.Result
	calls  []string
}

func (s *scriptedRunner) Run(name string, args []string, _ time.Duration) (waitci.Result, error) {
	line := name + " " + strings.Join(args, " ")
	s.calls = append(s.calls, line)
	if name == "git" {
		ok := func(text string) (waitci.Result, error) { return waitci.Result{Stdout: text + "\n"}, nil }
		switch {
		case !s.repo:
			return waitci.Result{Code: 128, Stderr: "fatal: not a git repository"}, nil
		case args[0] == "rev-parse" && args[1] == "--git-dir":
			return ok(".git")
		case args[0] == "rev-parse" && args[1] == "HEAD":
			return ok(s.head)
		case args[0] == "symbolic-ref" && s.branch != "":
			return ok(s.branch)
		}
		return waitci.Result{Code: 1}, nil
	}
	for prefix, responses := range s.gh {
		if strings.HasPrefix(line, prefix) && len(responses) > 0 {
			response := responses[0]
			if len(responses) > 1 {
				s.gh[prefix] = responses[1:]
			}
			return response, nil
		}
	}
	return waitci.Result{Code: 1, Stderr: "unexpected call: " + line}, nil
}

type stoppedClock struct{ slept time.Duration }

func (c *stoppedClock) Now() time.Duration { return c.slept }

func (c *stoppedClock) Sleep(d time.Duration) { c.slept += d }

// fakeWaitCI は wait-ci の外部を差し替える。outcome が nil でなければ監視を行わずにそれを返し、渡された Waiter を記録する。
type fakeWaitCI struct {
	runner  *scriptedRunner
	outcome *waitci.Outcome
	missing string
	clock   *stoppedClock
	waiter  *waitci.Waiter
	watched int
}

func installFakeWaitCI(t *testing.T, fake *fakeWaitCI) {
	t.Helper()
	old := waitCICommand
	t.Cleanup(func() { waitCICommand = old })
	if fake.runner == nil {
		fake.runner = &scriptedRunner{repo: true, branch: "feature", head: strings.Repeat("B", 40)}
	}
	fake.clock = &stoppedClock{}
	waitCICommand = waitCIAdapters{
		lookPath: func(name string) (string, error) {
			if name == fake.missing {
				return "", errors.New("not found")
			}
			return "/bin/" + name, nil
		},
		runner: fake.runner,
		clock:  fake.clock,
		watch: func(waiter *waitci.Waiter) waitci.Outcome {
			fake.waiter = waiter
			fake.watched++
			if fake.outcome != nil {
				return *fake.outcome
			}
			return waiter.Run()
		},
	}
}

func lines(stdout string) []string { return strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") }

func outcome(status waitci.Status, checks ...waitci.Check) *waitci.Outcome {
	return &waitci.Outcome{Status: status, Head: "abc123", Elapsed: 612, Checks: checks}
}

var (
	passed  = waitci.Check{Name: "build", Done: true, Result: "SUCCESS", URL: "https://example.test/build"}
	failed  = waitci.Check{Name: "tests", Done: true, Result: "FAILURE", URL: "https://example.test/tests"}
	running = waitci.Check{Name: "build", Result: "IN_PROGRESS"}
)

// --- MainExitCode ---

func TestWaitCIConflictHasItsOwnExitCode(t *testing.T) {
	// CI 失敗 (1) と混ぜない。1 だと呼び出し側がジョブのログを探しに行く。
	installFakeWaitCI(t, &fakeWaitCI{outcome: &waitci.Outcome{Status: waitci.StatusConflict, Conflicting: true}})
	if code, _, _ := runCommand(t, "", "wait-ci", "213"); code != 5 {
		t.Fatalf("code=%d", code)
	}
}

func TestWaitCIRepositoryWithoutCIIsNotATimeout(t *testing.T) {
	// 3 を返すと呼び出し側が CI の遅延と誤解して push し直す。
	installFakeWaitCI(t, &fakeWaitCI{outcome: &waitci.Outcome{Status: waitci.StatusNoCI, Elapsed: 45}})
	code, stdout, _ := runCommand(t, "", "wait-ci", "213")
	if code != 0 || lines(stdout)[0] != "wait-ci: この repo には CI が無い (check が 0 件のまま 45s 経ち、"+
		"workflow も直近の merged PR の check も見つからない)。監視をスキップした" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

// --- MainDetachedLookup ---

func detachedRunner(gh map[string][]waitci.Result) *scriptedRunner {
	return &scriptedRunner{repo: true, head: strings.Repeat("A", 40), gh: gh}
}

func TestWaitCIResolvesDetachedHeadBeforeStartingTheWaiter(t *testing.T) {
	fake := &fakeWaitCI{
		runner:  detachedRunner(map[string][]waitci.Result{"gh pr list --search": {{Stdout: "204\n"}}}),
		outcome: &waitci.Outcome{Status: waitci.StatusNoPR},
	}
	installFakeWaitCI(t, fake)
	code, stdout, _ := runCommand(t, "", "wait-ci", "--interval", "1")
	if code != 0 || fake.waiter.SHA != strings.Repeat("A", 40) {
		t.Fatalf("code=%d sha=%q", code, fake.waiter.SHA)
	}
	// 検出した PR 番号で監視する。PR が無ければ commit の側の文言で知らせる。
	if lines(stdout)[0] != "wait-ci: この commit に PR が無いので監視をスキップした" {
		t.Fatalf("stdout=%q", stdout)
	}
	fake.outcome = nil
	fake.runner.gh["gh pr view"] = []waitci.Result{{Code: 1, Stderr: "no pull requests found"}}
	fake.waiter.Run()
	if last := fake.runner.calls[len(fake.runner.calls)-1]; last != "gh pr view 204 --json headRefOid,mergeable,statusCheckRollup" {
		t.Fatalf("last call=%q", last)
	}
}

func TestWaitCISkipsDetachedHeadWithoutAnOpenPR(t *testing.T) {
	fake := &fakeWaitCI{runner: detachedRunner(map[string][]waitci.Result{"gh pr list --search": {{Stdout: ""}}})}
	installFakeWaitCI(t, fake)
	code, stdout, _ := runCommand(t, "", "wait-ci", "--interval", "1", "--pr-lookup-timeout", "0")
	if code != 0 || fake.watched != 0 {
		t.Fatalf("code=%d watched=%d", code, fake.watched)
	}
	want := []string{
		"wait-ci: commit " + strings.Repeat("A", 40) + " に open PR が無い。監視をスキップした",
		"wait-ci: exit=0 failed=0 total=0",
	}
	if !reflect.DeepEqual(lines(stdout), want) {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestWaitCIRetriesTheLookupBeforeSkipping(t *testing.T) {
	// 検索インデックスへの反映待ちで監視ごと飛ばさない。
	fake := &fakeWaitCI{
		runner:  detachedRunner(map[string][]waitci.Result{"gh pr list --search": {{Stdout: ""}, {Stdout: "213"}}}),
		outcome: &waitci.Outcome{Status: waitci.StatusNoPR},
	}
	installFakeWaitCI(t, fake)
	if code, _, _ := runCommand(t, "", "wait-ci", "--interval", "1"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	lookups := 0
	for _, line := range fake.runner.calls {
		if strings.HasPrefix(line, "gh pr list --search") {
			lookups++
		}
	}
	if lookups != 2 || fake.watched != 1 || fake.clock.slept != time.Second {
		t.Fatalf("lookups=%d watched=%d slept=%v", lookups, fake.watched, fake.clock.slept)
	}
}

func TestWaitCIReportsAFailedDetachedLookup(t *testing.T) {
	fake := &fakeWaitCI{runner: detachedRunner(map[string][]waitci.Result{
		"gh pr list --search": {{Code: 1, Stderr: "could not resolve to a Repository"}},
	})}
	installFakeWaitCI(t, fake)
	code, stdout, _ := runCommand(t, "", "wait-ci")
	if code != 4 || lines(stdout)[0] != "wait-ci: detached HEAD の PR 解決に失敗した: could not resolve to a Repository" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	// HEAD の commit が取れなければ、検索せずに失敗として返す。
	fake = &fakeWaitCI{runner: detachedRunner(nil)}
	fake.runner.head = ""
	installFakeWaitCI(t, fake)
	code, stdout, _ = runCommand(t, "", "wait-ci", "--any-sha")
	if code != 4 || lines(stdout)[0] != "wait-ci: detached HEAD の PR 解決に失敗した: detached HEAD の commit SHA を取得できない" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestWaitCIAnySHAStillFindsThePRByTheCurrentCommit(t *testing.T) {
	// --any-sha でも PR の特定には現在の commit を使い、監視対象 SHA は任せる。
	fake := &fakeWaitCI{
		runner:  detachedRunner(map[string][]waitci.Result{"gh pr list --search " + strings.Repeat("A", 40): {{Stdout: "9"}}}),
		outcome: outcome(waitci.StatusComplete, passed),
	}
	installFakeWaitCI(t, fake)
	if code, _, _ := runCommand(t, "", "wait-ci", "--any-sha"); code != 0 || fake.waiter.SHA != "" {
		t.Fatalf("code=%d sha=%q", code, fake.waiter.SHA)
	}
}

// --- 監視対象の SHA ---

func TestWaitCITargetSHA(t *testing.T) {
	head := strings.Repeat("B", 40)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{}, head},
		{[]string{"213"}, ""},
		{[]string{"213", "--sha", "HEAD"}, head},
		{[]string{"213", "--sha", "cafe"}, "cafe"},
		{[]string{"--sha", "cafe"}, "cafe"},
		{[]string{"--any-sha"}, ""},
		{[]string{"--sha", "cafe", "--any-sha"}, ""},
		// argparse が値として読む、- で始まる引数（= 付き、負の数、単独の -、空白を含むもの）。
		{[]string{"213", "--sha=-x"}, "-x"},
		{[]string{"213", "--sha", "-5"}, "-5"},
		{[]string{"213", "--sha", "-"}, "-"},
		{[]string{"213", "--sha", "-x y"}, "-x y"},
	}
	for _, tc := range cases {
		fake := &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed)}
		installFakeWaitCI(t, fake)
		if code, _, _ := runCommand(t, "", append([]string{"wait-ci"}, tc.args...)...); code != 0 {
			t.Fatalf("%v: code=%d", tc.args, code)
		}
		if fake.waiter.SHA != tc.want {
			t.Errorf("%v: sha=%q, want %q", tc.args, fake.waiter.SHA, tc.want)
		}
	}
}

func TestWaitCIPassesTheOptionsToTheWaiter(t *testing.T) {
	fake := &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed)}
	installFakeWaitCI(t, fake)
	runCommand(t, "", "wait-ci", "213")
	got := [5]int{fake.waiter.Interval, fake.waiter.Timeout, fake.waiter.StartTimeout, fake.waiter.Settle, fake.waiter.NoCITimeout}
	if got != [5]int{20, 1800, 300, 30, 45} || fake.waiter.Progress != nil || fake.waiter.HasCI == nil {
		t.Fatalf("defaults=%v", got)
	}
	runCommand(t, "", "wait-ci", "--interval=2", "--timeout", "3", "--start-timeout", "4", "--settle", "5",
		"--no-ci-timeout", "6", "--pr-lookup-timeout", "7", "213")
	got = [5]int{fake.waiter.Interval, fake.waiter.Timeout, fake.waiter.StartTimeout, fake.waiter.Settle, fake.waiter.NoCITimeout}
	if got != [5]int{2, 3, 4, 5, 6} {
		t.Fatalf("options=%v", got)
	}
}

// --- MainOutput ---

func TestWaitCIFailureVerdictIsTheFirstAndLastLine(t *testing.T) {
	installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed, failed)})
	code, stdout, stderr := runCommand(t, "", "wait-ci", "213")
	want := []string{
		"wait-ci: 1/2 件が失敗",
		"PR head: abc123  (2 checks, 612s)",
		"  FAILURE   tests  0s",
		"            https://example.test/tests",
		"wait-ci: exit=1 failed=1 total=2",
	}
	// 結論を stderr へ分けると、合流時にバッファの差で明細より先に届く。
	if code != 1 || !reflect.DeepEqual(lines(stdout), want) || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestWaitCISuccessDoesNotListThePassingChecks(t *testing.T) {
	installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed)})
	code, stdout, _ := runCommand(t, "", "wait-ci", "213")
	want := []string{"wait-ci: 全 1 件が成功", "PR head: abc123  (1 checks, 612s)", "wait-ci: exit=0 failed=0 total=1"}
	if code != 0 || !reflect.DeepEqual(lines(stdout), want) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestWaitCIAllChecksListsThePassingOnes(t *testing.T) {
	installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed)})
	_, stdout, _ := runCommand(t, "", "wait-ci", "213", "--all-checks")
	if !strings.Contains(stdout, "\n  SUCCESS   build  0s\n") {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestWaitCIConflictIsNotedNextToTheVerdict(t *testing.T) {
	result := outcome(waitci.StatusComplete, passed)
	result.Conflicting = true
	installFakeWaitCI(t, &fakeWaitCI{outcome: result})
	_, stdout, _ := runCommand(t, "", "wait-ci", "213")
	if got := lines(stdout)[:2]; !reflect.DeepEqual(got, []string{
		"wait-ci: 全 1 件が成功", "wait-ci: この PR は base ブランチとコンフリクトしている",
	}) {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestWaitCIEveryExitPathEndsWithTheMachineReadableLine(t *testing.T) {
	cases := []struct {
		status  waitci.Status
		message string
		code    int
		first   string
	}{
		{waitci.StatusNoPR, "", 0, "wait-ci: この branch に PR が無いので監視をスキップした"},
		{waitci.StatusError, "boom", 4, "wait-ci: gh の呼び出しが続けて失敗した: boom"},
		{waitci.StatusHeadTimeout, "cafe", 3, "wait-ci: 612s 待っても head が cafe にならなかった (現在 abc123)"},
		{waitci.StatusConflict, "", 5, "wait-ci: base ブランチとコンフリクトしていて check が 1 件も起動しない。rebase か merge で解消して push し直す"},
		{waitci.StatusNoCI, "", 0, "wait-ci: この repo には CI が無い (check が 0 件のまま 612s 経ち、" +
			"workflow も直近の merged PR の check も見つからない)。監視をスキップした"},
		{waitci.StatusEmptyTimeout, "", 3, "wait-ci: 612s 待っても check が 1 件も登録されなかった"},
	}
	for _, tc := range cases {
		result := outcome(tc.status)
		result.Message = tc.message
		installFakeWaitCI(t, &fakeWaitCI{outcome: result})
		code, stdout, stderr := runCommand(t, "", "wait-ci", "213")
		want := []string{tc.first, "wait-ci: exit=" + string(rune('0'+tc.code)) + " failed=0 total=0"}
		if code != tc.code || !reflect.DeepEqual(lines(stdout), want) || stderr != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", tc.status, code, stdout, stderr)
		}
	}
	result := outcome(waitci.StatusHeadTimeout)
	result.Head = ""
	result.Message = "cafe"
	installFakeWaitCI(t, &fakeWaitCI{outcome: result})
	if _, stdout, _ := runCommand(t, "", "wait-ci", "213"); lines(stdout)[0] != "wait-ci: 612s 待っても head が cafe にならなかった (現在 不明)" {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestWaitCITimeoutKeepsItsOwnExitCodeWithTheCheckCounts(t *testing.T) {
	installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusTimeout, running, failed)})
	code, stdout, _ := runCommand(t, "", "wait-ci", "213")
	got := lines(stdout)
	if code != 3 || got[0] != "wait-ci: 612s 以内に完了しなかった (pending: build)" ||
		got[len(got)-1] != "wait-ci: exit=3 failed=1 total=2" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestWaitCIProgressIsTheOnlyThingOnStderr(t *testing.T) {
	fake := &fakeWaitCI{outcome: outcome(waitci.StatusComplete)}
	installFakeWaitCI(t, fake)
	var stdout, stderr strings.Builder
	code := runWaitCI([]string{"213", "--progress"}, &stdout, &stderr)
	fake.waiter.Progress("0s total=0 pending=0")
	if code != 0 || stderr.String() != "wait-ci: [progress] 0s total=0 pending=0\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusComplete)})
	if _, _, stderr := runCommand(t, "", "wait-ci", "213", "-v"); stderr != "" {
		t.Fatalf("stderr=%q", stderr)
	}
}

// --- 監視の結合（偽の gh を通して Waiter を実際に回す） ---

func TestWaitCIWatchesThroughGH(t *testing.T) {
	view := `{"headRefOid":"` + strings.Repeat("B", 40) + `","mergeable":"MERGEABLE","statusCheckRollup":[` +
		`{"__typename":"CheckRun","workflowName":"CI","name":"test","status":"COMPLETED","conclusion":"SUCCESS",` +
		`"startedAt":"2026-09-09T23:20:01Z","completedAt":"2026-09-09T23:21:31Z"}]}`
	fake := &fakeWaitCI{runner: &scriptedRunner{repo: true, branch: "feature", head: strings.Repeat("B", 40),
		gh: map[string][]waitci.Result{"gh pr view --json": {{Stdout: view}}}}}
	installFakeWaitCI(t, fake)
	code, stdout, stderr := runCommand(t, "", "wait-ci", "--progress", "--all-checks", "--settle", "20", "--interval", "10")
	want := []string{
		"wait-ci: 全 1 件が成功",
		"PR head: " + strings.Repeat("B", 40) + "  (1 checks, 20s)",
		"  SUCCESS   CI / test  90s",
		"wait-ci: exit=0 failed=0 total=1",
	}
	if code != 0 || !reflect.DeepEqual(lines(stdout), want) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if stderr != "wait-ci: [progress] 0s total=1 pending=0 settling\nwait-ci: [progress] 10s total=1 pending=0 settling\n" {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestWaitCIAsksForCIEvidenceThroughGH(t *testing.T) {
	fake := &fakeWaitCI{runner: &scriptedRunner{gh: map[string][]waitci.Result{
		"gh pr view 5 --json":       {{Stdout: `{"headRefOid":"abc","statusCheckRollup":[]}`}},
		"gh api repos/":             {{Stdout: "0\n"}},
		"gh pr list --state merged": {{Stdout: "0\n"}},
	}}}
	installFakeWaitCI(t, fake)
	code, stdout, _ := runCommand(t, "", "wait-ci", "5", "--no-ci-timeout", "0")
	if code != 0 || !strings.Contains(lines(stdout)[0], "この repo には CI が無い (check が 0 件のまま 0s 経ち") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

// --- 引数 ---

func TestWaitCIRejectsAnIntervalBelowOne(t *testing.T) {
	installFakeWaitCI(t, &fakeWaitCI{})
	for _, args := range [][]string{{"--interval", "0"}, {"--interval", "-5"}, {"--interval=-1"}} {
		code, stdout, _ := runCommand(t, "", append([]string{"wait-ci"}, args...)...)
		if code != 2 || stdout != "wait-ci: --interval は 1 以上\nwait-ci: exit=2 failed=0 total=0\n" {
			t.Errorf("%v: code=%d stdout=%q", args, code, stdout)
		}
	}
}

func TestWaitCIArgumentErrorsExitWithTwo(t *testing.T) {
	for _, args := range [][]string{
		{"--interval", "abc"},
		{"--interval", "0x10"},
		{"--interval", "1__0"},
		{"--interval", "_1"},
		{"--interval", "--5"},
		{"--timeout"},
		{"--no-such-option"},
		{"1", "2"},
		{"--s", "x"},
		// argparse と同じく、--sha の直後のオプションに見える引数を値に取らない。
		{"--sha", "--progress"},
		{"--sha", "-v"},
		{"--sha", "--"},
		{"--sh", "--any"},
	} {
		installFakeWaitCI(t, &fakeWaitCI{outcome: outcome(waitci.StatusComplete)})
		code, stdout, stderr := runCommand(t, "", append([]string{"wait-ci"}, args...)...)
		// argparse のエラーと同じく、最終行を出さずに 2 で終える。
		if code != 2 || stdout != "" || !strings.Contains(stderr, "hhx wait-ci: error: ") {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestWaitCIAcceptsPythonStyleIntegersAndAbbreviations(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		interval int
		all      bool
	}{
		{[]string{"213", "--interval", " +1_0 "}, 10, false},
		{[]string{"213", "--int", "3"}, 3, false},
		{[]string{"--interval=4", "213", "--all"}, 4, true},
		{[]string{"--inter=5", "--", "--213"}, 5, false},
	} {
		fake := &fakeWaitCI{outcome: outcome(waitci.StatusComplete, passed)}
		installFakeWaitCI(t, fake)
		code, stdout, stderr := runCommand(t, "", append([]string{"wait-ci"}, tc.args...)...)
		if code != 0 || fake.waiter == nil || fake.waiter.Interval != tc.interval || strings.Contains(stdout, "SUCCESS") != tc.all {
			t.Errorf("%v: code=%d interval=%d stdout=%q stderr=%q", tc.args, code, fake.waiter.Interval, stdout, stderr)
		}
	}
}

func TestWaitCIHelpGoesToStdout(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "--he"} {
		code, stdout, stderr := runCommand(t, "", "wait-ci", flag)
		if code != 0 || !strings.HasPrefix(stdout, "Usage: hhx wait-ci") || !strings.Contains(stdout, "--pr-lookup-timeout") ||
			stderr != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", flag, code, stdout, stderr)
		}
	}
}

func TestWaitCIRequiresGHAndGit(t *testing.T) {
	for _, tool := range []string{"gh", "git"} {
		installFakeWaitCI(t, &fakeWaitCI{missing: tool})
		code, stdout, _ := runCommand(t, "", "wait-ci", "213")
		if code != 4 || stdout != "wait-ci: "+tool+" が必要\nwait-ci: exit=4 failed=0 total=0\n" {
			t.Errorf("%s: code=%d stdout=%q", tool, code, stdout)
		}
	}
}

func TestWaitCIDefaultAdaptersUseTheRealEnvironment(t *testing.T) {
	adapters := defaultWaitCIAdapters()
	if _, ok := adapters.runner.(waitci.ExecRunner); !ok || adapters.lookPath == nil || adapters.clock == nil ||
		adapters.watch == nil {
		t.Fatalf("adapters=%+v", adapters)
	}
	if code, stdout, _ := runCommand(t, "", "help"); code != 0 || !strings.Contains(stdout, "hhx wait-ci") {
		t.Fatalf("usage does not mention wait-ci: %q", stdout)
	}
}
