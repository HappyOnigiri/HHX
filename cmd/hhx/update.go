package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/pflag"

	"github.com/HappyOnigiri/hhx/internal/update"
	"github.com/HappyOnigiri/hhx/internal/version"
)

// updateCheckTimeout は最新リリースの問い合わせを待つ上限である。
const updateCheckTimeout = 20 * time.Second

// updateAdapters は update コマンドが触る外部（HTTP・プロセスの実行・実行環境）の差し替え点である。
type updateAdapters struct {
	// releaseBuild は配布用ビルドかどうかを返す。
	releaseBuild func() bool
	// current は手元のバイナリの版を返す。
	current func() string
	// latest は公開済みの最新リリースを問い合わせる。
	latest func(context.Context) (update.Release, error)
	// supported は install.sh が扱える環境かどうかを返す。
	supported func() bool
	// apply は指定したタグの install.sh を取得して実行する。
	apply func(ctx context.Context, tag string, output io.Writer) (update.Applied, error)
	// onPath は PATH から解決した hhx の実体のパスを返す。見つからなければ空を返す。
	onPath func() string
}

func defaultUpdateAdapters() updateAdapters {
	return updateAdapters{
		releaseBuild: update.ReleaseBuild,
		current:      version.String,
		latest:       update.Checker{}.Latest,
		// install.sh は macOS arm64 以外を前提条件の失敗として落とす。読みにくい失敗にせず、先に断る。
		supported: func() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" },
		apply: func(ctx context.Context, tag string, output io.Writer) (update.Applied, error) {
			return update.Applier{Output: output}.Apply(ctx, tag)
		},
		onPath: func() string {
			binary, err := exec.LookPath("hhx")
			if err != nil {
				return ""
			}
			return realPath(binary)
		},
	}
}

// updateCommand は runUpdate が使う実装である。テストだけが差し替える。
var updateCommand = defaultUpdateAdapters()

// runUpdate は新しいリリースの有無を確かめ、--apply ならそのタグの install.sh で更新する。
// hook の実行中にネットワークへ出ないよう、確認はこのコマンドを明示的に実行したときだけ行う。
func runUpdate(args []string, stdout, stderr io.Writer) int {
	language := displayLanguage()
	flags := pflag.NewFlagSet("update", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	apply := flags.Bool("apply", false, messages.T(language, idApplyFlag))
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, messages.Text(language, idUnexpectedArgument, map[string]any{
			"Command": "update", "Argument": strconv.Quote(flags.Arg(0)),
		}))
		return 2
	}
	adapters := updateCommand
	current := adapters.current()
	// 開発ビルドは版が vX.Y.Z ではなく比較できず、install.sh の置き換え先とも一致するとは限らない。
	if !adapters.releaseBuild() {
		_, _ = fmt.Fprintln(stdout, messages.Text(language, idDevelopment, map[string]any{"Version": current}))
		return 0
	}
	checkContext, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()
	release, err := adapters.latest(checkContext)
	if errors.Is(err, update.ErrUnavailable) {
		_, _ = fmt.Fprintln(stdout, messages.Text(language, idNoReleaseYet, map[string]any{"Version": current}))
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx update: %v\n", err)
		return 1
	}
	if !update.Newer(current, release.Tag) {
		_, _ = fmt.Fprintln(stdout, messages.Text(language, idUpToDate, map[string]any{"Version": current}))
		return 0
	}
	if !*apply {
		_, _ = fmt.Fprint(stdout, messages.Text(language, idAvailable, map[string]any{
			"Latest": release.Tag, "Version": current, "URL": release.URL,
		}))
		return 0
	}
	if !adapters.supported() {
		_, _ = fmt.Fprintln(stderr, messages.T(language, idUnsupported))
		return 1
	}
	updating := messages.Text(language, idUpdating, map[string]any{"Version": current, "Latest": release.Tag})
	_, _ = fmt.Fprintln(stdout, updating)
	applied, err := adapters.apply(context.Background(), release.Tag, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx update: %v\n", err)
		return 1
	}
	// hook の登録には install 時に PATH で解決した絶対パスが入っている。
	// 別の場所の hhx を使っていると、置き換えた先は hook から呼ばれない。
	if resolved := adapters.onPath(); resolved != "" && resolved != realPath(applied.Path) {
		note := messages.Text(language, idOtherOnPath, map[string]any{"OnPath": resolved, "Path": applied.Path})
		_, _ = fmt.Fprint(stdout, note)
	}
	return 0
}

// realPath は symlink を解決した絶対パスを返す。解決できなければ元のパスを返す。
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}
