package idlewaitguard

import (
	"strings"
	"testing"

	"github.com/HappyOnigiri/hhx/internal/hooktest"
)

const backgroundSentencePrefix = "バックグラウンド実行は即座に戻る"

func runBash(t *testing.T, command string, background any) hooktest.Result {
	t.Helper()
	var extra map[string]any
	if background != nil {
		extra = map[string]any{"run_in_background": background}
	}
	return hooktest.Stdin(t, Definition(), hooktest.BashPayload(command, "/tmp", extra))
}

func check(t *testing.T, want string, cases []hooktest.Case) {
	t.Helper()
	hooktest.CheckTable(t, Definition(), want, "/tmp", cases)
}

// 実測された空ループの前半（run_in_background の sleep）。
func TestBackgroundSleepLoop(t *testing.T) {
	for _, command := range []string{"sleep 600; echo done", "sleep 600", "sleep 60; echo done", "(sleep 600; echo done)"} {
		got := runBash(t, command, true)
		if got.Decision != hooktest.Deny || !strings.Contains(got.Reason, "WAIT") {
			t.Errorf("%q: %+v", command, got)
		}
	}
}

// 前景でも同じく拒否する（待機になっていないのは同じ）。
func TestForegroundSleep(t *testing.T) {
	check(t, hooktest.Deny, []hooktest.Case{
		{Command: "sleep 5", Label: "WAIT"},
		{Command: "sleep 600; echo done", Label: "WAIT"},
		{Command: "sleep 1 && sleep 2", Label: "WAIT"},
		{Command: "sleep 5\n", Label: "WAIT"},
		// Python の str.split() と同じく、Unicode の空白でもトークンを分ける。
		{Command: "sleep　5", Label: "WAIT"},
	})
}

// 理由文はバックグラウンドかどうかで言い回しを変える。
func TestReasonMentionsBackgroundOnlyWhenBackground(t *testing.T) {
	background := runBash(t, "sleep 600", true).Reason
	foreground := runBash(t, "sleep 600", false).Reason
	if !strings.Contains(background, backgroundSentencePrefix) {
		t.Errorf("background reason: %q", background)
	}
	if strings.Contains(foreground, backgroundSentencePrefix) || !strings.Contains(foreground, waitForegroundSentence) {
		t.Errorf("foreground reason: %q", foreground)
	}
	for _, reason := range []string{background, foreground} {
		if !strings.Contains(reason, "ターンを終えて") {
			t.Errorf("reason must offer to end the turn: %q", reason)
		}
	}
	// run_in_background は JSON の true のときだけバックグラウンドとみなす。
	if got := runBash(t, "sleep 600", "true").Reason; strings.Contains(got, backgroundSentencePrefix) {
		t.Errorf("a string run_in_background must be treated as foreground: %q", got)
	}
}

// run_in_background は省略時にキー自体が無い。落ちずに前景として扱う。
func TestOmittedBackgroundKeyBehavesAsForeground(t *testing.T) {
	got := runBash(t, "sleep 600", nil)
	if got.Decision != hooktest.Deny || !strings.Contains(got.Reason, "WAIT") || strings.Contains(got.Reason, backgroundSentencePrefix) {
		t.Fatalf("%+v", got)
	}
}

// 実測された空ループの後半（ターン繋ぎ）。
func TestTurnFiller(t *testing.T) {
	check(t, hooktest.Deny, []hooktest.Case{
		{Command: "echo ok", Label: "NOOP"},
		{Command: "echo waiting", Label: "NOOP"},
		{Command: "echo idle", Label: "NOOP"},
		{Command: "true", Label: "NOOP"},
		{Command: "echo ok; true", Label: "NOOP"},
		{Command: `printf 'ok\n'`, Label: "NOOP"},
		{Command: "echo ok\n", Label: "NOOP"},
		{Command: "{ echo ok; }", Label: "NOOP"},
	})
}

func TestBackgroundNoopIsDeniedToo(t *testing.T) {
	got := runBash(t, "echo ok", true)
	if got.Decision != hooktest.Deny || !strings.Contains(got.Reason, "NOOP") {
		t.Fatalf("%+v", got)
	}
}

func TestReasonOffersAlternatives(t *testing.T) {
	got := runBash(t, "echo ok", nil).Reason
	for _, part := range []string{"ターンを終えて", "書き換えて再実行しないでください"} {
		if !strings.Contains(got, part) {
			t.Errorf("reason %q does not contain %q", got, part)
		}
	}
}

// UI で理由が省略されても、先頭だけで拒否の対象を特定できる。コマンドは Python の json.dumps と同じ形で埋め込む。
func TestReasonStartsWithRejectedCommand(t *testing.T) {
	cases := map[string]string{
		"echo ok; true":            `NOOP: 拒否対象コマンド: "echo ok; true"` + "\n",
		"echo first\necho second":  `NOOP: 拒否対象コマンド: "echo first\necho second"` + "\n",
		"echo a && echo b":         `NOOP: 拒否対象コマンド: "echo a && echo b"` + "\n",
		"echo 'x' \"y\" \\ \t\x01": `NOOP: 拒否対象コマンド: "echo 'x' \"y\" \\ \t\u0001"` + "\n",
		"sleep 1  ":                "WAIT: 拒否対象コマンド: \"sleep 1  \"\n",
	}
	for command, prefix := range cases {
		if got := runBash(t, command, nil).Reason; !strings.HasPrefix(got, prefix) {
			t.Errorf("%q: reason %q does not start with %q", command, got, prefix)
		}
	}
}

// 複合コマンドの途中の echo は許容する（実測ログの正当な使い方）。
func TestEchoInCompoundCommand(t *testing.T) {
	check(t, "", hooktest.Commands(
		"mkdir -p tmp/scratch && echo created",
		"go build -o bin/x ./cmd/x && echo built",
		"git fetch origin && echo fetched",
		"echo start && make test",
		"npm ci; echo done; npm test",
	))
}

// 変数展開・コマンド置換を伴う echo は情報を取るコマンドなので通す。
func TestEchoCarryingInformation(t *testing.T) {
	check(t, "", hooktest.Commands(
		`echo "installPath=$P"`,
		"echo $PATH",
		"echo $(git rev-parse HEAD)",
		"echo `date`",
	))
}

// リダイレクト・パイプ・代入を伴うものは状態を変える。
func TestEchoWithSideEffect(t *testing.T) {
	check(t, "", hooktest.Commands(
		"echo hello > out.txt",
		"echo hello >> out.txt",
		"echo hello | pbcopy",
		"FOO=bar echo hello",
	))
}

func TestOrdinaryCommands(t *testing.T) {
	check(t, "", hooktest.Commands(
		"ls -la",
		"git status",
		"gh pr view 123 --json body",
		"node companion.mjs task --json",
		"sleep_timer --help",
	))
}

// 待機を伴う正当なポーリングは、実処理と連結されていれば通す。
func TestSleepCombinedWithRealWork(t *testing.T) {
	check(t, "", hooktest.Commands(
		"sleep 2 && curl -s https://example.test/health",
		"until gh run view --json status; do sleep 30; done",
	))
}

// `:` だけのコマンドは一次ゲートを通らず素通りする（意図的な穴）。挙動が変わったらここで気付く。
func TestKnownGapBareColon(t *testing.T) {
	if got := runBash(t, ":", nil); got.Decision != "" {
		t.Fatalf("%+v", got)
	}
	// ゲートを通れば `:` も無効果のコマンドとして扱う。
	if label, _ := classify(":"); label != noopLabel {
		t.Fatalf("classify(:)=%q", label)
	}
}

func TestClassifyIgnoresEmptySegments(t *testing.T) {
	for _, command := range []string{"", ";", " ; && ", "\n\n", "　"} {
		if label, _ := classify(command); label != "" {
			t.Errorf("classify(%q)=%q, want pass", command, label)
		}
	}
	if label, _ := classify("; echo ok ;"); label != noopLabel {
		t.Errorf("empty segments must not affect the decision, got %q", label)
	}
}

func TestSegmentIsNoop(t *testing.T) {
	cases := map[string]bool{
		"":            true,
		"   ":         true,
		"echo ok":     true,
		"(sleep 1":    true,
		"FOO=1 true":  false,
		"echo $X":     false,
		"echo > f":    false,
		"ls":          false,
		"echo=1":      false,
		"{ printf x}": true,
	}
	for segment, want := range cases {
		if got := segmentIsNoop(segment); got != want {
			t.Errorf("segmentIsNoop(%q)=%v, want %v", segment, got, want)
		}
	}
}

// デバッグ経路（argv）でも同じ判定になる。
func TestArgvPath(t *testing.T) {
	for command, want := range map[string]string{
		"sleep 600; echo done":       hooktest.Deny,
		"echo ok":                    hooktest.Deny,
		"mkdir -p x && echo created": "",
	} {
		if got := hooktest.Argv(t, Definition(), command); got.Decision != want {
			t.Errorf("argv %q: %+v, want %q", command, got, want)
		}
	}
	// argv では run_in_background を渡せないので前景の言い回しになる。
	if got := hooktest.Argv(t, Definition(), "sleep 1").Reason; strings.Contains(got, backgroundSentencePrefix) {
		t.Errorf("argv reason: %q", got)
	}
}

func TestOddInputsDoNotCrash(t *testing.T) {
	for _, raw := range []string{
		"", "   ", "echo ok", "[]", "null", "0",
		`{"tool_input": {"command": null}}`,
		`{"tool_input": {"command": 123, "x": "echo"}}`,
		`{"tool_input": "echo ok"}`,
		`{"cwd": "/tmp", "x": "sleep"}`,
		`{"tool_input": {"command": ["echo ok"]}}`,
	} {
		if got := hooktest.Stdin(t, Definition(), raw); got.Decision != "" {
			t.Errorf("odd input %q: %+v", raw, got)
		}
	}
}

func TestDisabledByConfig(t *testing.T) {
	got := hooktest.Run(t, Definition(), hooktest.BashPayload("echo ok", "/tmp", nil), hooktest.Options{
		Config: "hooks:\n  idle-wait-guard:\n    enabled: false\n",
	})
	if got.Decision != "" {
		t.Fatalf("disabled hook must write nothing: %+v", got)
	}
}
