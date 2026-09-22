package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCommand(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// isolate は HOME・PATH・設定ファイルを一時ディレクトリへ向ける。install のテストが実機の設定に触れないためである。
func isolate(t *testing.T) (home, binary string) {
	t.Helper()
	home = t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	binary = filepath.Join(bin, "hhx")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	t.Setenv("HHX_CONFIG", "")
	return home, binary
}

func TestUnknownHookIsSilentSuccess(t *testing.T) {
	isolate(t)
	code, stdout, stderr := runCommand(t, `{"tool_name":"Bash","tool_input":{"command":"gh pr merge 1"}}`, "hook", "no-such-hook")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestVersionAndUsage(t *testing.T) {
	if code, stdout, _ := runCommand(t, "", "version"); code != 0 || !strings.HasPrefix(stdout, "hhx version ") {
		t.Fatalf("version: code=%d stdout=%q", code, stdout)
	}
	if code, _, _ := runCommand(t, ""); code != 2 {
		t.Fatalf("no arguments must exit 2, got %d", code)
	}
	if code, _, _ := runCommand(t, "", "bogus"); code != 2 {
		t.Fatalf("an unknown command must exit 2, got %d", code)
	}
}

func TestInstallAndUninstallWithEmptyRegistry(t *testing.T) {
	home, _ := isolate(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "{\n  \"model\": \"opus\"\n}\n"
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"install", "uninstall", "install"} {
		code, stdout, stderr := runCommand(t, "", command)
		if code != 0 || !strings.Contains(stdout, "claude: "+settings+" (unchanged)") {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", command, code, stdout, stderr)
		}
		if strings.Contains(stdout, "codex") {
			t.Fatalf("%s must skip agents without a config directory: %q", command, stdout)
		}
	}
	data, err := os.ReadFile(settings)
	if err != nil || string(data) != original {
		t.Fatalf("settings changed: %q", data)
	}
}

func TestInstallNeedsAnAgent(t *testing.T) {
	isolate(t)
	if code, _, stderr := runCommand(t, "", "install"); code != 1 || !strings.Contains(stderr, "--agent") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := runCommand(t, "", "install", "--agent", "cursor"); code != 2 {
		t.Fatalf("an unknown agent must exit 2, got %d", code)
	}
}

func TestInstallRejectsBrokenConfig(t *testing.T) {
	home, _ := isolate(t)
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("hooks:\n  no-such-hook:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HHX_CONFIG", path)
	code, _, stderr := runCommand(t, "", "install", "--agent", "claude")
	if code != 1 || !strings.Contains(stderr, "unknown hooks: no-such-hook") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// 実行時の hook は壊れた設定でも止まらない。
	if code, stdout, _ := runCommand(t, "{}", "hook", "no-such-hook"); code != 0 || stdout != "" {
		t.Fatalf("hook: code=%d stdout=%q", code, stdout)
	}
}

func TestInstallRequiresHHXOnPath(t *testing.T) {
	home, binary := isolate(t)
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCommand(t, "", "install"); code != 1 || !strings.Contains(stderr, "not on PATH") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// uninstall は hhx が PATH から消えた後でも使える。
	if code, _, stderr := runCommand(t, "", "uninstall"); code != 0 {
		t.Fatalf("uninstall: code=%d stderr=%q", code, stderr)
	}
}
