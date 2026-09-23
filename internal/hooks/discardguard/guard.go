// Package discardguard は、未コミットの変更を破棄する git 操作の前に、復元用の snapshot を取る hook（discard-guard）である。
//
// 方針:
//   - 破棄自体は止めない。ただし戻せない状態にはしない。一時 index で作った snapshot のコミットを、
//     reflog つきの refs/hhx/discard-snapshot に積む。追跡ファイルの変更と未追跡ファイルが入る。作業ツリーと本物の index には触らない。
//   - 対象は discardRules。git stash は stash list / stash apply で戻せるので対象外。
//   - 検出は多めに拾い、対象の特定で絞る。保存先のリポジトリは cd を出現順に辿って決める。破棄系の git ごとに
//     「その時点の作業ディレクトリ（-C があればその先）」を候補にし、候補が複数あれば全部保存する（余分に保存しても害はない）。
//     パスは先頭の ~/ だけ展開し、変数入りのパスや pushd / sh -c 経由のように静的に追えない形は deny する。
//   - 保存できたら無出力で抜け、作れなければ deny する。
//
// allow を返さない理由: permissionDecision はコマンド文字列全体への判定なので、複合コマンド（git reset --hard && rm -rf ...）で
// allow を返すと、同居する別のコマンドまで permission のルールを飛ばしてしまう。無出力で抜けて通常の permission の流れに任せる。
// ask を返さない理由: ask を承認しても snapshot は作られず、Codex は ask を解釈しない。
// 理由文には「ガードが働く形への書き直し」だけを書く。cp 退避のような迂回路を案内すると、snapshot なしの破棄へ誘導することになる。
//
// snapshot の寿命: 到達経路は reflog だけなので、git gc の reflog expire で自然に消える。掃除の処理は持たない。
// ref は linked worktree の間で共有される（refs/hhx/* は共通ディレクトリに置かれる）。別の作業ツリーで破棄すると ref の先頭は
// そちらに移るので、どの作業ツリーのものかはメッセージ末尾の "@ <toplevel>" で判別する（復元の手順は README）。
//
// Codex の PreToolUse には exec_command の workdir が渡らず、payload の cwd はセッション開始時のディレクトリのままである。
// この hook はその cwd を基準にする（移植元と同じ限界）。
// デバッグ経路（`hhx hook discard-guard '<コマンド>'`）では cwd が渡らないので、プロセスの作業ディレクトリを基準にする。
package discardguard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
	py "github.com/HappyOnigiri/hhx/internal/pycompat"
)

// Name は hook の名前である。
const Name = "discard-guard"

// Definition は discard-guard の定義を返す。
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

// snapshotRef は snapshot を積む ref である。
const snapshotRef = "refs/hhx/discard-snapshot"

// snapshotIndexName は一時 index のファイル名である。作業ツリーごとの git ディレクトリに置き、削除せずに再利用する（stat cache が効く）。
const snapshotIndexName = "hhx-discard-snapshot.index"

// snapshotIdentity は snapshot のコミットの作成者である。利用者の git 設定に user.name が無くても作れるように渡す。
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=hhx",
	"GIT_AUTHOR_EMAIL=hhx@localhost",
	"GIT_COMMITTER_NAME=hhx",
	"GIT_COMMITTER_EMAIL=hhx@localhost",
}

// hook 全体の持ち時間と、git 1 回あたりの上限。対象のリポジトリが複数あると保存も複数回走るので、
// 合計が agent 側の hook のタイムアウト（60 秒）を超えないよう、git 1 回ごとに残り時間を上限にする。
// 使い切ったらその git は失敗として扱い（deny）、agent 側のタイムアウトで無言に落ちるのを避ける。
// テストで短くするために変数にしている。
var (
	timeBudget = 50 * time.Second
	gitTimeout = 30 * time.Second
)

// gate は一次ゲートである。全 Bash 呼び出しで走るので、git を含まない入力は解析しない。
func gate(input []byte) bool {
	return bytes.Contains(input, []byte("git"))
}

func run(c *hookrt.Context) error {
	runner := gitRunner{deadline: time.Now().Add(timeBudget)}
	var command, cwd string
	if c.FromArgs {
		command = string(c.Input)
	} else {
		command, cwd = payloadFrom(c.Input)
	}
	if command == "" || !gitCallRE.MatchString(command) {
		return nil
	}
	labels := discardLabels(command)
	if len(labels) == 0 {
		return nil
	}
	label := strings.Join(labels, " / ")

	targets, err := resolveTargetDirs(command, cwd)
	if err != nil {
		// 作業ディレクトリが得られない（移植元では例外で無出力になっていた）。
		return err
	}
	if targets == nil {
		c.Deny(denyUnresolved(label))
		return nil
	}
	// 候補が同じ作業ツリーの別のディレクトリなら、保存は 1 回にする（toplevel で重複を除く）。
	// toplevel は --show-toplevel の値のまま比べる。正規化を変えるとメッセージの "@ <toplevel>" も変わる。
	seen := map[string]bool{}
	for _, target := range targets {
		top, ok := runner.git(target, nil, "rev-parse", "--show-toplevel")
		if !ok || top == "" {
			c.Deny(denyFailed(label))
			return nil
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		if snapshot(runner, top, label) == failed {
			c.Deny(denyFailed(label))
			return nil
		}
	}
	// 保存できた、または保存するものが無かった。判定は通常の permission の流れに任せる。
	return nil
}

// payloadFrom は payload から command と cwd を取り出す。型の違う値は空として扱う。
// 文字列でない command は、移植元では判定の途中で例外になり無出力で終わっていた。
func payloadFrom(raw []byte) (command, cwd string) {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", ""
	}
	if toolInput, ok := payload["tool_input"].(map[string]any); ok {
		command, _ = toolInput["command"].(string)
	}
	cwd, _ = payload["cwd"].(string)
	return command, cwd
}

// 以下の検出用のパターンは「多めに拾って、対象の特定で絞る」方針である。検出が漏れると snapshot なしで破棄が走るため、迷ったら広く拾う。
// \s と \S は Python の意味（Unicode の空白）にする。RE2 の \s は ASCII だけなので、全角空白を挟んだ形の検出が漏れる。
// Python の $ は末尾の改行の直前にも一致するが、$ を置いた位置はどれも空白（改行を含む）との選択なので、RE2 の $ でも一致は変わらない。
const (
	// gitBase は git の起動である。(git ...) や sh -c 'git ...'、パス指定の起動（/usr/bin/git ...）も拾う。
	gitBase = `(?:^|[` + py.SpaceChars + `;&|('"])(?:rtk` + py.Space + `+)?(?:` + py.NotSpace + `*/)?git`
	// optionValue はオプションの値である。\S+ だけにするとクォートの中の空白（-C 'my repo'）で切れ、サブコマンドに届かず検出が漏れる。
	optionValue = `(?:[^` + py.SpaceChars + `'"]*(?:'[^']*'|"[^"]*")|` + py.NotSpace + `+)`
	// globalOptions は git とサブコマンドの間に挟まる大域オプションである。許さないと git -C <path> reset --hard が漏れる。
	globalOptions = `(?:` + py.Space + `+(?:-[cC]` + py.Space + `*` + optionValue +
		`|-p|--paginate|--no-pager|--git-dir=` + optionValue + `|--work-tree=` + optionValue +
		`|--exec-path=` + optionValue + `|--literal-pathspecs))*`
	// gitCommand は git <大域オプション...> <サブコマンド> の、サブコマンドの直前までである。
	gitCommand = gitBase + globalOptions + py.Space + `+`
	// segment は同じコマンドの中（パイプ・コマンドの区切りを越えない）である。
	segment = `[^|;&]*`
	// end は末尾の境界である。空白と行末だけでは (git reset --hard) の閉じ括弧・閉じクォートを取り逃す。
	end = `(?:[` + py.SpaceChars + `);&|'"]|$)`
	// flagEnd はフラグの直後の境界である（--staged=x のような = も含む）。
	flagEnd = `(?:[` + py.SpaceChars + `=;&|'"]|$)`
)

var (
	gitCallRE = regexp.MustCompile(gitBase + py.Space)

	restoreRE        = regexp.MustCompile(gitCommand + `restore(` + segment + `)`)
	restoreStagedRE  = regexp.MustCompile(py.Space + `--staged` + flagEnd)
	restoreWorktree  = regexp.MustCompile(py.Space + `--worktree`)
	applyRE          = regexp.MustCompile(gitCommand + `apply(` + segment + `)`)
	applySafeFlags   = regexp.MustCompile(py.Space + `(?:--check|--stat|--summary|--numstat|--cached)` + flagEnd)
	applyDiscardFlag = regexp.MustCompile(py.Space + applyDiscardFlags + flagEnd)

	separatorRE = regexp.MustCompile(`[(){};&|]`)
)

// applyDiscardFlags の短縮形は、-p3 / -C3 の 3 を -3 と誤検出しないよう、R と 3 の組み合わせだけを並べる。
const applyDiscardFlags = `(?:--reverse|--3way|--reject|-(?:R3|3R|R|3))`

// isWorktreeRestore は git restore が作業ツリーを書き戻す形かを返す。
// パススペックを伴う restore は対象である。--staged だけの形は index の巻き戻しで作業ツリーに触らないので対象外で、
// --worktree を併記すれば対象になる。
func isWorktreeRestore(command string) bool {
	for _, match := range restoreRE.FindAllStringSubmatch(command, -1) {
		rest := match[1]
		if restoreStagedRE.MatchString(rest) && !restoreWorktree.MatchString(rest) {
			continue
		}
		for _, token := range py.Fields(rest) {
			if !strings.HasPrefix(token, "-") {
				return true
			}
		}
	}
	return false
}

// isDiscardingApply は git apply が作業ツリーの内容を破棄・上書きしうる形かを返す。
// -R はパッチが意図より広ければ必要な変更まで消し、--3way / --reject は文脈が一致しなくても書き込む。
// 既定の apply は文脈が一致しなければ何も書かずに失敗するので対象外で、--cached は index だけに触るので対象外である。
func isDiscardingApply(command string) bool {
	for _, match := range applyRE.FindAllStringSubmatch(command, -1) {
		rest := match[1]
		if applySafeFlags.MatchString(rest) {
			continue
		}
		if applyDiscardFlag.MatchString(rest) {
			return true
		}
	}
	return false
}

// discardRule は未コミットの変更を破棄する操作の 1 つである。
type discardRule struct {
	label   string
	matches func(string) bool
}

func pattern(expression string) func(string) bool {
	return regexp.MustCompile(expression).MatchString
}

// discardRules は snapshot の対象になる操作である。ラベルはこの順に " / " で連結して理由文に入れる。
var discardRules = []discardRule{
	{"git reset --hard", pattern(gitCommand + `reset` + segment + py.Space + `--hard` + end)},
	// -B（ブランチの付け替え）と、パススペック・強制形（checkout . / -- <path> / -f）。
	{"git checkout (破棄形)", pattern(gitCommand + `checkout` + segment + py.Space + `(?:-B|--|\.|:/|-f|--force)` + end)},
	{"git switch (強制切り替え)", pattern(gitCommand + `switch` + segment + py.Space +
		`(?:-f|--force|--discard-changes|-C)` + end)},
	{"git restore (作業ツリー)", isWorktreeRestore},
	// 短縮フラグは連結（-fd / -xdf / -ffd）を許すため、末尾の境界を要求しない。
	{"git clean -f", pattern(gitCommand + `clean` + segment + py.Space + `(?:-[a-zA-Z]*f|--force` + end + `)`)},
	{"git apply (破棄形)", isDiscardingApply},
}

// discardSubcommands は discardRules で拾うサブコマンドである。必ず一致させる（テストで縛っている）。
// 漏らすと、対象の特定が git -C <別のリポジトリ> を採用せず cwd を保存し、本来の対象が無防備になる。
var discardSubcommands = []string{"reset", "checkout", "switch", "restore", "clean", "apply"}

// discardLabels は command に当たったルールのラベルを、discardRules の順に返す。
func discardLabels(command string) []string {
	var labels []string
	for _, rule := range discardRules {
		if rule.matches(command) {
			labels = append(labels, rule.label)
		}
	}
	return labels
}

var shells = []string{"sh", "bash", "zsh", "dash", "ksh"}

// unsafeEnvPrefixes は作業ツリー・git ディレクトリを環境変数で差し替える形である。これは追わない。
var unsafeEnvPrefixes = []string{"GIT_WORK_TREE=", "GIT_DIR=", "GIT_INDEX_FILE="}

// unstaticChars を含むパス（変数展開・glob・クォート・シェルのメタ文字）は静的に解決できない。
const unstaticChars = "$`*?~'\"&;|()"

func isStaticPath(path string) bool {
	return !strings.ContainsAny(path, unstaticChars)
}

func isShell(token string) bool {
	if slices.Contains(shells, token) {
		return true
	}
	index := strings.LastIndex(token, "/")
	return index >= 0 && slices.Contains(shells, token[index+1:])
}

func hasUnsafeEnvPrefix(token string) bool {
	for _, prefix := range unsafeEnvPrefixes {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

// takeValue は tokens[index] から値を 1 つ取り出す。クォートが閉じていなければ、閉じるまで空白でつないで束ねる。
// 束ねないと -C 'my repo' の途中で切れてサブコマンドを取り違え、別のリポジトリを対象と誤認する。
// 戻り値は値と、最後に消費した位置である。閉じないまま尽きたら、そこまでを返す。
func takeValue(tokens []string, index int) (string, int) {
	value := tokens[index]
	for index+1 < len(tokens) && (strings.Count(value, "'")%2 == 1 || strings.Count(value, `"`)%2 == 1) {
		index++
		value += " " + tokens[index]
	}
	return value, index
}

// expandUser は Python の os.path.expanduser を、先頭が ~ か ~/ のパスに対して行う。
func expandUser(path string) string {
	home := py.Home()
	if home == "~" {
		// ホームディレクトリが得られなければ、Python と同じく展開しない。
		return path
	}
	if home == "/" {
		home = ""
	}
	if expanded := home + path[1:]; expanded != "" {
		return expanded
	}
	return "/"
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// resolveTargetDirs は snapshot の対象のディレクトリを出現順に返す。静的に特定できなければ nil を返す。
//
// cd / git -C が別のリポジトリを指しているのに cwd を見ると、無関係なリポジトリを保存して本来の対象を無防備に破棄させる。
// そこで cd を出現順に辿ってシェルの作業ディレクトリを模擬し、破棄系の git ごとに「その時点の作業ディレクトリ（-C があればその先）」を候補にする。
// cd / -C の行き先が存在しなければ特定できないものとする。存在しない cd は実行時に失敗して前のディレクトリに留まるので、
// その後の相対パス（cd ..）を取り違えると本来の対象を保存し損ねる。
// (cd dir && git ...) の括弧や make -C のような無関係な -C を正規表現の境界だけで見分けるのは無理があるので、トークン単位で走査する。
//
// 次の 2 点は移植元の Python 実装から意図して変えている。どちらも移植元では別のリポジトリを保存して通し、本来の対象の変更を失っていた。
//   - ( ... ) と $( ... ) はサブシェルなので、中の cd を閉じ括弧で取り消す。移植元は括弧を空白として読み、閉じた後も cd を残していた。
//     ただし括弧はクォートや case の分岐を見分けずに数えるので、実際には閉じていない括弧で cd を取り消しうる。
//     そこで括弧を無視した作業ディレクトリ（移植元の意味）も並行して辿り、両方を候補にする。移植元より保存先が減る入力は無い。
//   - 1 つの git に -C が複数あれば、git と同じく出現順に適用し、相対パスは直前の -C の先から辿る。移植元は最後の -C だけを cwd から解決していた。
func resolveTargetDirs(command, cwd string) ([]string, error) {
	tokens, parens := tokenize(command)
	count := len(tokens)

	// base は模擬しているシェルの作業ディレクトリである。
	base := cwd
	if base == "" {
		var err error
		if base, err = py.Getcwd(); err != nil {
			return nil, err
		}
	}
	if !isDir(base) {
		return nil, nil
	}
	// flat は括弧を無視して辿る作業ディレクトリである（閉じ括弧で cd を取り消さない）。
	flat := base
	var targets []string
	add := func(target string) {
		if !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	// outer はサブシェルに入る前の作業ディレクトリを積む。対応の無い閉じ括弧（case の分岐など）は無視する。
	var outer []string

	// resolve は from を基準にパスを解決する。静的に追えない・存在しないなら空文字列を返す。
	// 先頭の ~ / ~/ だけは展開する（hook の HOME はシェルと同じなので決まる）。クォートつきの "~/x" はシェルも展開しないので、
	// クォートを静的でないものとして特定できないままにする。~user の形は追わない。
	resolve := func(from, path string) string {
		if path == "~" || strings.HasPrefix(path, "~/") {
			path = expandUser(path)
		}
		if !isStaticPath(path) {
			return ""
		}
		full := path
		if !strings.HasPrefix(path, "/") {
			full = py.Join(from, path)
		}
		if !isDir(full) {
			return ""
		}
		return full
	}

	for index, token := range tokens {
		for _, paren := range parens[index] {
			if paren == '(' {
				outer = append(outer, base)
			} else if len(outer) > 0 {
				base = outer[len(outer)-1]
				outer = outer[:len(outer)-1]
			}
		}
		next := ""
		if index+1 < count {
			next = tokens[index+1]
		}
		switch {
		case token == "cd" || token == "pushd" || token == "popd":
			if token != "cd" {
				// pushd / popd は行き先を追えない。
				return nil, nil
			}
			if next == "" || strings.HasPrefix(next, "-") {
				// 引数の無い cd（ホームへ移る）と cd - は追わない。
				return nil, nil
			}
			path, _ := takeValue(tokens, index+1)
			destination, flatDestination := resolve(base, path), resolve(flat, path)
			if destination == "" || flatDestination == "" {
				return nil, nil
			}
			base, flat = destination, flatDestination
		case isShell(token):
			// 別のシェル経由（sh -c '...' / bash -lc '...'）は、中身をトークンとして追えない。
			if strings.HasPrefix(next, "-") {
				return nil, nil
			}
		case hasUnsafeEnvPrefix(token):
			return nil, nil
		case token == "git" || strings.HasSuffix(token, "/git"):
			directories, subcommand, ok := parseGitOptions(tokens, index+1)
			if !ok {
				return nil, nil
			}
			// -C は破棄系のサブコマンドに付いていた場合だけ採用する（git -C <other> fetch && git reset --hard に乗っ取られないため）。
			if !slices.Contains(discardSubcommands, subcommand) {
				continue
			}
			for _, start := range []string{base, flat} {
				target := start
				for _, directory := range directories {
					if target = resolve(target, directory); target == "" {
						return nil, nil
					}
				}
				add(target)
			}
		}
	}
	// 破棄系の git をトークンとして拾えなかった（クォートの中など）が、discardRules には当たっている。
	// 多めに拾う方針なので、最後の作業ディレクトリを保存しておく。
	if len(targets) == 0 {
		add(base)
		add(flat)
	}
	return targets, nil
}

// tokenize は区切り文字を空白に寄せて command をトークンに分ける。区切りと語がくっついた形でも cd / git をトークンとして拾うためである。
// parens[i] は tokens[i] の直前にある丸括弧を出現順に持つ（サブシェルの出入りを辿るため）。
// トークンの列は、括弧を空白として読んだ移植元と同じにする（括弧そのものはトークンにしない）。
func tokenize(command string) (tokens []string, parens [][]byte) {
	var pending []byte
	rest := command
	for {
		location := separatorRE.FindStringIndex(rest)
		piece := rest
		if location != nil {
			piece = rest[:location[0]]
		}
		for _, token := range py.Fields(piece) {
			tokens = append(tokens, token)
			parens = append(parens, pending)
			pending = nil
		}
		if location == nil {
			return tokens, parens
		}
		if separator := rest[location[0]]; separator == '(' || separator == ')' {
			pending = append(pending, separator)
		}
		rest = rest[location[1]:]
	}
}

// parseGitOptions は tokens[start:] を git の大域オプションとして読み、-C の値を出現順に並べたものとサブコマンドを返す。
// サブコマンドが無ければ空文字列を返す。--git-dir / --work-tree と、値の無い -C は特定できないものとして ok を偽にする。
func parseGitOptions(tokens []string, start int) (directories []string, subcommand string, ok bool) {
	count := len(tokens)
	index := start
	for ; index < count; index++ {
		option := tokens[index]
		switch {
		case option == "-C":
			if index+1 >= count {
				return nil, "", false
			}
			var directory string
			directory, index = takeValue(tokens, index+1)
			directories = append(directories, directory)
		case strings.HasPrefix(option, "-C"):
			var value string
			value, index = takeValue(tokens, index)
			directories = append(directories, value[2:])
		case strings.HasPrefix(option, "--git-dir") || strings.HasPrefix(option, "--work-tree"):
			// 作業ツリーが cwd から外れるが、対象の特定までは踏み込まない。
			return nil, "", false
		case option == "-c":
			if index+1 < count {
				// -c k=v の値を読み飛ばす。
				_, index = takeValue(tokens, index+1)
			}
		case strings.HasPrefix(option, "-"):
		default:
			return directories, option, true
		}
	}
	return directories, "", true
}

// snapshotResult は snapshot の結果である。
type snapshotResult int

const (
	saved snapshotResult = iota
	// clean は保存するものが無かった（作業ツリーが HEAD と同じ）ことを示す。
	clean
	failed
)

// snapshot は作業ツリー top の未コミットの変更を snapshot のコミットにして snapshotRef に積む。
// top は rev-parse --show-toplevel の値である。
func snapshot(runner gitRunner, top, label string) snapshotResult {
	gitDir, ok := runner.git(top, nil, "rev-parse", "--absolute-git-dir")
	if !ok || gitDir == "" {
		return failed
	}
	// linked worktree ごとに git ディレクトリが分かれるので、一時 index も作業ツリーごとに分かれる。共有すると stat cache が混線する。
	indexEnv := []string{"GIT_INDEX_FILE=" + py.Join(gitDir, snapshotIndexName)}

	// 空の一時 index に add -A すると、作業ツリーそのものの木ができる（read-tree は要らない）。
	if _, ok := runner.git(top, indexEnv, "add", "-A"); !ok {
		return failed
	}
	// add -A は一時 index に無い ignored のファイルを足さないので、本物の index で追跡中の ignored のファイル（add -f で登録したもの）を
	// update-index で取り込む（無くなっていれば --remove で消す）。本物の index を一時 index へコピーしないのは、同じ作業ツリーで並行して
	// 走ったときに、他方の add -A と write-tree の間で一時 index を巻き戻さないため。
	ignored, ok := runner.run(top, nil, "", "ls-files", "-z", "--cached", "--ignored", "--exclude-standard")
	if !ok {
		return failed
	}
	if ignored != "" {
		if _, ok := runner.run(top, indexEnv, ignored, "update-index", "--add", "--remove", "-z", "--stdin"); !ok {
			return failed
		}
	}
	tree, ok := runner.git(top, indexEnv, "write-tree")
	if !ok || tree == "" {
		return failed
	}

	head, _ := runner.git(top, nil, "rev-parse", "--verify", "-q", "HEAD")
	// ref は作業ツリーの間で共有されるので、どの作業ツリーのものかをメッセージに残す。
	message := "wt-snapshot: " + label + " @ " + top

	args := []string{"commit-tree"}
	if head != "" {
		// HEAD と同じ木なら保存するものが無い（追跡していない ignored のファイルは元から含まない）。
		if headTree, ok := runner.git(top, nil, "rev-parse", "-q", "HEAD^{tree}"); ok && headTree == tree {
			return clean
		}
		args = append(args, "-p", head)
	}
	args = append(args, "-m", message, tree)

	commit, ok := runner.git(top, snapshotIdentity, args...)
	if !ok || commit == "" {
		return failed
	}
	// 独自の ref の reflog は --create-reflog が無いと記録されない。
	if _, ok := runner.git(top, nil, "update-ref", "--create-reflog", "-m", message, snapshotRef, commit); !ok {
		return failed
	}
	return saved
}

// gitRunner は hook 全体の持ち時間の中で git を起動する。
type gitRunner struct {
	deadline time.Time
}

// git は `git -C <directory> <args...>` を実行し、stdout の両端の空白を除いて返す。失敗・タイムアウトなら ok は偽になる。
func (r gitRunner) git(directory string, env []string, args ...string) (string, bool) {
	output, ok := r.run(directory, env, "", args...)
	return py.Strip(output), ok
}

// run は `git -C <directory> <args...>` を stdin を渡して実行し、stdout をそのまま返す（-z の出力のパスの空白を削らない）。
// 利用者の環境（GIT_* を含む）はそのまま継承し、env の分だけ足す。
func (r gitRunner) run(directory string, env []string, stdin string, args ...string) (string, bool) {
	remaining := time.Until(r.deadline)
	if remaining <= 0 {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), min(gitTimeout, remaining))
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	if env != nil {
		command.Env = append(os.Environ(), env...)
	}
	// タイムアウトでは SIGKILL ではなく SIGTERM で止める。git は SIGTERM を捕捉して一時 index の lock を消すが、
	// SIGKILL だと lock が残り、以後その作業ツリーの snapshot が失敗し続ける（破棄系のコマンドがすべて deny になる）。
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	// SIGTERM で終わらない場合と、子孫のプロセスが出力を開いたまま残った場合に、この猶予の後に kill して待ち続けない。
	command.WaitDelay = time.Second
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if command.Run() != nil {
		return "", false
	}
	return stdout.String(), true
}
