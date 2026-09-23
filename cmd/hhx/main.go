// Command hhx は Claude Code と Codex の agent hook を 1 つのバイナリで提供する。
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/HappyOnigiri/hhx/internal/config"
	"github.com/HappyOnigiri/hhx/internal/hookrt"
	"github.com/HappyOnigiri/hhx/internal/registry"
	"github.com/HappyOnigiri/hhx/internal/version"
)

const usage = `Usage:
  hhx hook <name> [input]   Run a hook (invoked by Claude Code / Codex)
  hhx install [--agent claude|codex]...
                            Register hhx hooks in the agent settings
  hhx uninstall [--agent claude|codex]...
                            Remove hhx hooks from the agent settings
  hhx version               Print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "hook":
		return runHook(args[1:], stdin, stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "-v", "--version", "version":
		_, _ = fmt.Fprintln(stdout, "hhx version "+version.String())
		return 0
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "hhx: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// runHook は agent から呼ばれる入口である。hook を止めないことが最優先なので、
// 名前が未知・設定が壊れている・本体が失敗した、のどれでも無出力で 0 を返す。
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		// agent は必ず名前を付けて呼ぶ。名前が無いのは手で打った場合だけなので、使い方を示す。
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	hookrt.Run(registry.Lookup(args[0]), hookrt.Invocation{
		Args:   args[1:],
		Stdin:  stdin,
		Stdout: stdout,
		LoadConfig: func() (*config.Config, error) {
			path, err := config.DefaultPath()
			if err != nil {
				return nil, err
			}
			return config.Load(path)
		},
	})
	return 0
}
