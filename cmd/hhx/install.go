package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"github.com/HappyOnigiri/hhx/internal/config"
	"github.com/HappyOnigiri/hhx/internal/hookrt"
	"github.com/HappyOnigiri/hhx/internal/install"
	"github.com/HappyOnigiri/hhx/internal/registry"
)

func runInstall(args []string, stdout, stderr io.Writer) int {
	agents, ok := parseAgentFlags("install", args, stderr)
	if !ok {
		return 2
	}
	// 実行時の hook は壊れた設定を既定値で読み流すため、壊れていることに気付く機会は install しかない。
	if err := validateConfig(registry.All()); err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx install: %v\n", err)
		return 1
	}
	options, err := installOptions()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx install: %v\n", err)
		return 1
	}
	if options.Binary, err = resolveBinary(); err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx install: %v\n", err)
		return 1
	}
	return applyAgents("install", agents, options, stdout, stderr, func(agent hookrt.Agent) (install.Result, error) {
		return install.Install(options, agent, registry.All())
	})
}

func runUninstall(args []string, stdout, stderr io.Writer) int {
	agents, ok := parseAgentFlags("uninstall", args, stderr)
	if !ok {
		return 2
	}
	options, err := installOptions()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hhx uninstall: %v\n", err)
		return 1
	}
	return applyAgents("uninstall", agents, options, stdout, stderr, func(agent hookrt.Agent) (install.Result, error) {
		return install.Uninstall(options, agent)
	})
}

// parseAgentFlags は --agent を読む。省略時は nil を返し、applyAgents が設定ディレクトリのある CLI を選ぶ。
func parseAgentFlags(command string, args []string, stderr io.Writer) ([]hookrt.Agent, bool) {
	flags := pflag.NewFlagSet(command, pflag.ContinueOnError)
	flags.SetOutput(stderr)
	names := flags.StringSlice("agent", nil, "agent to configure: claude or codex (repeatable; default: every agent whose config directory exists)")
	if err := flags.Parse(args); err != nil {
		return nil, false
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "hhx %s: unexpected argument %q\n", command, flags.Arg(0))
		return nil, false
	}
	var agents []hookrt.Agent
	for _, name := range *names {
		agent, ok := parseAgent(name)
		if !ok {
			_, _ = fmt.Fprintf(stderr, "hhx %s: unknown agent %q (want claude or codex)\n", command, name)
			return nil, false
		}
		agents = append(agents, agent)
	}
	return agents, true
}

func parseAgent(name string) (hookrt.Agent, bool) {
	for _, agent := range hookrt.Agents() {
		if string(agent) == strings.ToLower(name) {
			return agent, true
		}
	}
	return "", false
}

// applyAgents は agents（省略時は設定ディレクトリのある CLI）へ apply を順に適用する。
// 1 つが失敗しても残りは処理し、終了コードで失敗を伝える。
func applyAgents(command string, agents []hookrt.Agent, options install.Options, stdout, stderr io.Writer, apply func(hookrt.Agent) (install.Result, error)) int {
	if len(agents) == 0 {
		agents = detectAgents(options.Home)
		if len(agents) == 0 {
			_, _ = fmt.Fprintf(stderr, "hhx %s: neither ~/.claude nor ~/.codex exists; pass --agent to choose explicitly\n", command)
			return 1
		}
	}
	code := 0
	for _, agent := range agents {
		result, err := apply(agent)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hhx %s: %s: %v\n", command, agent, err)
			code = 1
			continue
		}
		status := "unchanged"
		if result.Changed {
			status = "updated"
		}
		_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", agent, result.Path, status)
		if result.Changed && result.Resolved != result.Path {
			_, _ = fmt.Fprintf(stdout, "  wrote the symlink target %s; commit it where it is managed\n", result.Resolved)
		}
		if result.Backup != "" {
			_, _ = fmt.Fprintf(stdout, "  previous content saved to %s\n", result.Backup)
		}
	}
	return code
}

func detectAgents(home string) []hookrt.Agent {
	var agents []hookrt.Agent
	for _, agent := range hookrt.Agents() {
		if info, err := os.Stat(filepath.Join(home, "."+string(agent))); err == nil && info.IsDir() {
			agents = append(agents, agent)
		}
	}
	return agents
}

func installOptions() (install.Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Options{}, err
	}
	return install.Options{
		Home:      home,
		BackupDir: filepath.Join(home, ".local", "state", "hhx", "backups"),
	}, nil
}

// validateConfig は設定ファイルの構文、hook 名の綴り、hook 固有の設定の型を definitions に照らして確かめる。
func validateConfig(definitions []hookrt.Definition) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, definition := range definitions {
		known[definition.Name] = true
	}
	if unknown := cfg.UnknownHooks(func(name string) bool { return known[name] }); len(unknown) > 0 {
		return fmt.Errorf("%s: unknown hooks: %s", path, strings.Join(unknown, ", "))
	}
	for _, definition := range definitions {
		if definition.NewSettings == nil {
			continue
		}
		if err := cfg.Decode(definition.Name, definition.NewSettings()); err != nil {
			return fmt.Errorf("%s: hooks.%s: %v", path, definition.Name, err)
		}
	}
	return nil
}

// resolveBinary は登録するコマンド文字列に書く hhx の絶対パスを PATH から決める。
// 実行中のバイナリ（os.Executable）へは退避しない。go run や開発 build の一時的なパスを設定ファイルへ焼き付けないためである。
func resolveBinary() (string, error) {
	binary, err := exec.LookPath("hhx")
	if err != nil {
		return "", errors.New("hhx is not on PATH; install it (e.g. make install) and add its directory to PATH first")
	}
	return filepath.Abs(binary)
}
