package pycompat

import (
	"errors"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// ここにある関数は Python の os.path（posixpath、Python 3.14）と同じ結果を返す。
// Go の path/filepath とは次の点が違う。
//   - Normpath は先頭がちょうど 2 個のスラッシュ（//a）を残す。filepath.Clean は 1 個に畳む。
//   - Realpath は存在しない末尾を残したまま、存在する部分の symlink を解決する。filepath.EvalSymlinks はエラーにする。
//   - Getcwd は PWD 環境変数を見ず、getcwd(3) の結果（symlink を解決した実体のパス）を返す。os.Getwd は PWD を返すことがある。

// ErrEmptyPath は Python の os.path.relpath が空のパスで送出する ValueError にあたる。
var ErrEmptyPath = errors.New("no path specified")

// ErrNUL は Python の os.lstat が NUL を含むパスで送出する ValueError にあたる。
// OSError と違い、os.path.realpath の中でも握りつぶされない。
var ErrNUL = errors.New("embedded null byte")

// Getcwd は Python の os.getcwd() と同じく、作業ディレクトリの実体のパスを返す。
func Getcwd() (string, error) {
	return syscall.Getwd()
}

// Home は Python の os.path.expanduser("~") と同じく、HOME 環境変数（無ければパスワードデータベース）から
// ホームディレクトリを返す。末尾のスラッシュを除き、空になれば "/" を返す。
// どちらからも得られなければ "~" を返す（Python は展開せずにそのまま返す）。
func Home() string {
	home, ok := os.LookupEnv("HOME")
	if !ok {
		account, err := user.LookupId(strconv.Itoa(os.Getuid()))
		if err != nil {
			return "~"
		}
		home = account.HomeDir
	}
	if home = strings.TrimRight(home, "/"); home == "" {
		return "/"
	}
	return home
}

// IsAbs は os.path.isabs と同じく、パスが / で始まるかを返す。
func IsAbs(path string) bool {
	return strings.HasPrefix(path, "/")
}

// Join は os.path.join(a, b) である。b が絶対パスなら a を捨てる。
func Join(a, b string) string {
	switch {
	case strings.HasPrefix(b, "/") || a == "":
		return b
	case strings.HasSuffix(a, "/"):
		return a + b
	default:
		return a + "/" + b
	}
}

// Dirname は os.path.dirname である。
func Dirname(path string) string {
	head := path[:strings.LastIndex(path, "/")+1]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head
}

// Normpath は os.path.normpath である。
func Normpath(path string) string {
	if path == "" {
		return "."
	}
	initialSlashes := 0
	if strings.HasPrefix(path, "/") {
		initialSlashes = 1
		if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
			initialSlashes = 2
		}
	}
	var components []string
	for _, component := range strings.Split(path, "/") {
		switch {
		case component == "" || component == ".":
			continue
		case component != ".." || (initialSlashes == 0 && len(components) == 0) ||
			(len(components) > 0 && components[len(components)-1] == ".."):
			components = append(components, component)
		case len(components) > 0:
			components = components[:len(components)-1]
		}
	}
	normalized := strings.Repeat("/", initialSlashes) + strings.Join(components, "/")
	if normalized == "" {
		return "."
	}
	return normalized
}

// Abspath は os.path.abspath である。相対パスは作業ディレクトリ（Getcwd）を基準にする。
func Abspath(path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		cwd, err := Getcwd()
		if err != nil {
			return "", err
		}
		path = Join(cwd, path)
	}
	return Normpath(path), nil
}

// Relpath は os.path.relpath(path, start) である。path が空なら ErrEmptyPath を返す。
func Relpath(path, start string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}
	startList, err := absoluteComponents(start)
	if err != nil {
		return "", err
	}
	pathList, err := absoluteComponents(path)
	if err != nil {
		return "", err
	}
	common := 0
	for common < len(startList) && common < len(pathList) && startList[common] == pathList[common] {
		common++
	}
	var relative []string
	for range len(startList) - common {
		relative = append(relative, "..")
	}
	relative = append(relative, pathList[common:]...)
	if len(relative) == 0 {
		return ".", nil
	}
	return strings.Join(relative, "/"), nil
}

func absoluteComponents(path string) ([]string, error) {
	absolute, err := Abspath(path)
	if err != nil {
		return nil, err
	}
	if tail := strings.TrimLeft(absolute, "/"); tail != "" {
		return strings.Split(tail, "/"), nil
	}
	return nil, nil
}

// Realpath は os.path.realpath(path)（strict=False）である。
// 存在しない要素や読めない symlink はそのまま残し、symlink の循環はその位置で解決をやめる。
// NUL を含むパスには ErrNUL を返す。相対パスで作業ディレクトリが得られなければ、その error を返す（Python の OSError）。
func Realpath(path string) (string, error) {
	resolved := "/"
	if !strings.HasPrefix(path, "/") {
		cwd, err := Getcwd()
		if err != nil {
			return "", err
		}
		resolved = cwd
	}
	if strings.ContainsRune(path, 0) {
		return "", ErrNUL
	}
	// rest は未処理の要素を逆順に積んだスタックである。symlinkMarker は、その symlink の解決が済んだことを表し、
	// 直後（スタックの 1 つ下）に symlink 自身のパスが積まれている。
	rest := reversed(strings.Split(path, "/"))
	seen := map[string]*string{}
	for len(rest) > 0 {
		name := rest[len(rest)-1]
		rest = rest[:len(rest)-1]
		if name == symlinkMarker {
			link := rest[len(rest)-1]
			rest = rest[:len(rest)-1]
			value := resolved
			seen[link] = &value
			continue
		}
		switch name {
		case "", ".":
			continue
		case "..":
			resolved = resolved[:strings.LastIndex(resolved, "/")]
			if resolved == "" {
				resolved = "/"
			}
			continue
		}
		next := resolved + "/" + name
		if resolved == "/" {
			next = "/" + name
		}
		info, err := os.Lstat(next)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		if cached, ok := seen[next]; ok {
			// 解決済みならその結果を使い、解決中（循環）なら symlink のパスのまま進める。
			resolved = next
			if cached != nil {
				resolved = *cached
			}
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			resolved = next
			continue
		}
		if strings.HasPrefix(target, "/") {
			resolved = "/"
		}
		seen[next] = nil
		rest = append(rest, next, symlinkMarker)
		rest = append(rest, reversed(strings.Split(target, "/"))...)
	}
	return resolved, nil
}

// symlinkMarker は Realpath のスタックで、Python 実装の None にあたる。パスの要素は NUL を含まないので衝突しない。
const symlinkMarker = "\x00"

func reversed(items []string) []string {
	out := make([]string, len(items))
	for index, item := range items {
		out[len(items)-1-index] = item
	}
	return out
}

// Expanduser は os.path.expanduser である。~ は HOME 環境変数（無ければパスワードデータベース）で、
// ~user はパスワードデータベースで展開し、どちらも得られなければ path をそのまま返す。
// ~user は CGO を使わない build では /etc/passwd だけを引くので、macOS の Directory Services にだけいる利用者は展開しない。
func Expanduser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	end := strings.Index(path[1:], "/") + 1
	if end == 0 {
		end = len(path)
	}
	var home string
	if end == 1 {
		value, ok := os.LookupEnv("HOME")
		if !ok {
			account, err := user.LookupId(strconv.Itoa(os.Getuid()))
			if err != nil {
				return path
			}
			value = account.HomeDir
		}
		home = value
	} else {
		account, err := user.Lookup(path[1:end])
		if err != nil {
			return path
		}
		home = account.HomeDir
	}
	if expanded := strings.TrimRight(home, "/") + path[end:]; expanded != "" {
		return expanded
	}
	return "/"
}

// expandvarsRE は posixpath._varpattern（re.ASCII）である。
var expandvarsRE = regexp.MustCompile(`\$([A-Za-z0-9_]+|\{[^}]*\}?)`)

// Expandvars は os.path.expandvars である。$name と ${name} を環境変数で置き換え、無い変数と閉じない ${ はそのまま残す。
func Expandvars(path string) string {
	if !strings.Contains(path, "$") {
		return path
	}
	return expandvarsRE.ReplaceAllStringFunc(path, func(match string) string {
		name := match[1:]
		if strings.HasPrefix(name, "{") {
			if !strings.HasSuffix(name, "}") || len(name) < 2 {
				return match
			}
			name = name[1 : len(name)-1]
		}
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		return match
	})
}
