// Package forbiddentermguard は、PR や issue の本文に禁止語が入るのを PreToolUse で拒否する hook（forbidden-term-guard）である。
//
// 方針:
//   - 有効にするのはリポジトリ単位のオプトインである。共通の git ディレクトリの直下に forbidden-terms.txt がある
//     リポジトリでだけ検査する。語そのものをどのリポジトリにもコミットさせないため、語リストは Git の管理外に置く。
//     書式は docs/forbidden-terms.md にある。
//   - 対象は本文をリモートへ送るコマンド（gh pr / gh issue の create・edit・comment と gh api）である。
//     git commit と git push は git の commit-msg / pre-push hook が受け持つので対象外にする。
//   - コマンド文字列に加えて、--body-file / -F で渡すファイルの中身も読む。
//   - 変数展開・別シェル経由といった明示的な回避は対象外（README の既知の限界）。
//
// 語リストが無いリポジトリでは、ファイルの存在の確認だけで抜ける。git を含め外部コマンドは起動しない。
package forbiddentermguard

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
	py "github.com/HappyOnigiri/hhx/internal/pycompat"
)

// Name は hook の名前である。
const Name = "forbidden-term-guard"

const (
	// termsFileName は語リストのファイル名である。共通の git ディレクトリの直下に置く。
	termsFileName = "forbidden-terms.txt"
	maxReported   = 10
	maxLine       = 200
	maxBodyBytes  = 1 << 20
)

// Definition は forbidden-term-guard の定義を返す。
func Definition() hookrt.Definition {
	return hookrt.Definition{
		Name:           Name,
		DefaultEnabled: true,
		Registrations: []hookrt.Registration{
			{Agent: hookrt.Claude, Event: "PreToolUse", Matcher: "Bash"},
			{Agent: hookrt.Codex, Event: "PreToolUse", Matcher: "Bash"},
		},
		// 検査の対象はすべて gh のサブコマンドである。
		Gate: func(input []byte) bool { return strings.Contains(string(input), "gh") },
		Run:  run,
	}
}

var (
	// 本文をリモートへ送るコマンド。gh api はエンドポイントを問わず対象にする（本文を含む書き込みが REST でも通るため）。
	targetRE = regexp.MustCompile(`(?:^|[;&|(]|` + py.Space + `)(?:` + py.NotSpace + `*/)?gh` + py.Space +
		`+(?:(?:pr|issue)` + py.Space + `+(?:create|edit|comment)|api)` + py.NotWordOrEnd)
	// --body-file <path> / -F <path> / --body-file=<path>
	bodyFileRE = regexp.MustCompile(`(?:--body-file|-F)[=` + py.SpaceChars + `]+([^;&|` + py.SpaceChars + `]+)`)
)

func run(c *hookrt.Context) error {
	command, cwd, ok := commandAndCWD(c)
	if !ok || command == "" || !targetRE.MatchString(command) {
		return nil
	}
	gitDir := commonDir(cwd)
	if gitDir == "" {
		return nil
	}
	termsPath := joinPath(gitDir, termsFileName)
	if !isFile(termsPath) {
		// このリポジトリは対象外である。
		return nil
	}
	terms, invalid := loadTerms(termsPath)
	if len(invalid) > 0 {
		// 検査できない語があるまま送ると、その語が公開先へ出てしまう。
		c.Deny(invalidReason(invalid, termsPath))
		return nil
	}
	if len(terms) == 0 {
		return nil
	}
	findings := scan(command, terms, "command", nil)
	for _, file := range bodyFiles(command, cwd) {
		info, err := os.Stat(file.path)
		if err != nil || info.Size() > maxBodyBytes {
			continue
		}
		data, err := os.ReadFile(file.path)
		if err != nil {
			continue
		}
		findings = scan(strings.ToValidUTF8(string(data), "�"), terms, file.shown, findings)
	}
	if len(findings) > 0 {
		c.Deny(reason(findings, termsPath))
	}
	return nil
}

// commandAndCWD は判定するコマンドと、その実行ディレクトリを返す。
// payload が JSON の object でないか、tool_input が object でなければ（移植元で AttributeError になっていた形）、
// argv の経路ではその文字列をコマンドとし、プロセスの cwd を使う。stdin の経路では何もしない（fail-open）。
func commandAndCWD(c *hookrt.Context) (command, cwd string, ok bool) {
	var payload any
	if json.Unmarshal(c.Input, &payload) == nil {
		if object, isObject := payload.(map[string]any); isObject {
			if toolInput, present := object["tool_input"]; !present {
				return fromPayload(object, map[string]any{})
			} else if mapping, isMapping := toolInput.(map[string]any); isMapping {
				return fromPayload(object, mapping)
			}
		}
	}
	if !c.FromArgs {
		return "", "", false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	return string(c.Input), cwd, true
}

// fromPayload は payload の object と tool_input から command と cwd を取り出す。
// 移植元で例外になって無出力で終わっていた形（command が文字列でない、cwd が空でない文字列以外）は、何もしない。
func fromPayload(object, toolInput map[string]any) (command, cwd string, ok bool) {
	if value, present := toolInput["command"]; present {
		if command, ok = value.(string); !ok {
			return "", "", false
		}
	}
	switch value := object["cwd"].(type) {
	case string:
		cwd = value
	default:
		if truthy(value) {
			return "", "", false
		}
	}
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return "", "", false
		}
	}
	return command, cwd, true
}

// truthy は JSON の値が Python で真とみなされるかを返す。
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// commonDir は cwd から共通の git ディレクトリを求める。git は起動しない。見つからなければ空文字列を返す。
// linked worktree では、.git ファイルの gitdir: と、その先の commondir を解決する。
func commonDir(start string) string {
	if start == "" {
		return ""
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	gitDir := ""
	for gitDir == "" {
		candidate := filepath.Join(current, ".git")
		info, err := os.Stat(candidate)
		switch {
		case err == nil && info.IsDir():
			gitDir = candidate
		case err == nil && info.Mode().IsRegular():
			// linked worktree の .git は `gitdir: <path>` の 1 行のファイルである。
			data, err := os.ReadFile(candidate)
			if err != nil {
				return ""
			}
			head := py.Strip(firstLine(strings.ToValidUTF8(string(data), "�")))
			rest, found := strings.CutPrefix(head, "gitdir:")
			if !found {
				return ""
			}
			gitDir = joinPath(current, py.Strip(rest))
		default:
			parent := filepath.Dir(current)
			if parent == current {
				return ""
			}
			current = parent
		}
	}
	// linked worktree の git ディレクトリは、commondir で共通の側を指す。
	common := joinPath(gitDir, "commondir")
	if !isFile(common) {
		return gitDir
	}
	data, err := os.ReadFile(common)
	if err != nil {
		return gitDir
	}
	if relative := py.Strip(strings.ToValidUTF8(string(data), "�")); relative != "" {
		return joinPath(gitDir, relative)
	}
	return gitDir
}

// firstLine は Python のテキストモードの readline() と同じく、最初の改行（\n・\r\n・\r）までを返す。
func firstLine(text string) string {
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		return text[:index]
	}
	return text
}

// joinPath は Python の os.path.normpath(os.path.join(base, name)) と同じく、name が絶対パスならそれを使う。
func joinPath(base, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(base, name)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// termPattern は語リストの 1 行を、大文字小文字を区別しない部分一致の正規表現にしたものである。
type termPattern = *regexp.Regexp

// loadTerms は語リストを読む。書式は docs/forbidden-terms.md にある。読めなければ空を返す。
// invalid はコンパイルできない re: の行の行番号である。
func loadTerms(path string) (terms []termPattern, invalid []int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	return parseTerms(string(data))
}

// newlineRE は Python のテキストモード（universal newlines）が行の区切りとみなす改行である。
var newlineRE = regexp.MustCompile(`\r\n|\r|\n`)

// parseTerms は語リストの中身を解釈する。
// RE2 でコンパイルできない re: の行（Python の re では書ける後読みなど）は、1 始まりの行番号を invalid に返す。
func parseTerms(text string) (terms []termPattern, invalid []int) {
	for index, raw := range newlineRE.Split(strings.ToValidUTF8(text, "�"), -1) {
		line := py.Strip(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if expression, isRegexp := strings.CutPrefix(line, "re:"); isRegexp {
			expression = py.Strip(expression)
			if expression == "" {
				continue
			}
			pattern, err := regexp.Compile("(?i)" + expression)
			if err != nil {
				invalid = append(invalid, index+1)
				continue
			}
			terms = append(terms, pattern)
			continue
		}
		terms = append(terms, regexp.MustCompile("(?i)"+literalPattern(line)))
	}
	return terms, invalid
}

// dottedI は、Python の re.IGNORECASE が互いに一致させ、Go の (?i) は i・I と İ・ı を区別する 4 文字である。
// Python は 1 文字ずつの小文字化（İ→i）と、同じ大文字を持つ小文字の表（i と ı）で照合する。
// ほかの文字の大文字小文字の対応は、Go の (?i) と一致する（Unicode のバージョンで増えた文字を除く）。
const dottedI = "iIİı"

// literalPattern は、語を Python の re.escape と re.IGNORECASE の組と同じ文字に一致する正規表現にする。
func literalPattern(term string) string {
	var pattern strings.Builder
	for _, r := range term {
		if strings.ContainsRune(dottedI, r) {
			pattern.WriteString("[" + dottedI + "]")
			continue
		}
		pattern.WriteString(regexp.QuoteMeta(string(r)))
	}
	return pattern.String()
}

// matchesAny は text が語のどれかを含むかを返す。
func matchesAny(text string, terms []termPattern) bool {
	for _, pattern := range terms {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// scan は text の行ごとに語を探し、該当した行を findings に足して返す。
func scan(text string, terms []termPattern, location string, findings []string) []string {
	for index, line := range py.SplitLines(text) {
		if matchesAny(line, terms) {
			findings = append(findings, "  "+location+":"+strconv.Itoa(index+1)+": "+clip(line))
		}
	}
	return findings
}

// clip は該当した行を 1 行 maxLine 文字（コードポイント）で切る。
func clip(text string) string {
	runes := []rune(py.Strip(text))
	if len(runes) > maxLine {
		return string(runes[:maxLine]) + " …"
	}
	return string(runes)
}

type bodyFile struct {
	// shown は理由文に出す表記（コマンドに書かれたまま）、path は読むファイルのパスである。
	shown string
	path  string
}

// bodyFiles は --body-file / -F で渡すファイルを返す。- （stdin）は読まない。
func bodyFiles(command, cwd string) []bodyFile {
	var files []bodyFile
	for _, match := range bodyFileRE.FindAllStringSubmatch(command, -1) {
		raw := strings.Trim(match[1], `'"`)
		if raw == "" || raw == "-" {
			continue
		}
		path := raw
		if !filepath.IsAbs(raw) {
			base := cwd
			if base == "" {
				base = "."
			}
			path = pythonJoin(base, raw)
		}
		files = append(files, bodyFile{shown: raw, path: expandUser(path)})
	}
	return files
}

// pythonJoin は Python の os.path.join と同じく、区切りを足すだけで正規化しない（相対の .. を残す）。
func pythonJoin(base, name string) string {
	if strings.HasSuffix(base, "/") {
		return base + name
	}
	return base + "/" + name
}

// expandUser は Python の os.path.expanduser と同じく、先頭の ~ と ~user をホームディレクトリに置き換える。
func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	name, rest, _ := strings.Cut(path[1:], "/")
	var home string
	if name == "" {
		home = os.Getenv("HOME")
		if home == "" {
			if current, err := user.Current(); err == nil {
				home = current.HomeDir
			}
		}
	} else if account, err := user.Lookup(name); err == nil {
		home = account.HomeDir
	}
	if home == "" {
		return path
	}
	if !strings.Contains(path, "/") {
		return home
	}
	return strings.TrimRight(home, "/") + "/" + rest
}
