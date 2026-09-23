package hooktest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIdentity はテスト用のリポジトリを作る git に渡す設定である。CI のランナーには git の利用者設定が無い。
var gitIdentity = []string{
	"-c", "user.name=hook-test",
	"-c", "user.email=hook-test@localhost",
	"-c", "commit.gpgsign=false",
	"-c", "init.defaultBranch=main",
}

// Main は TestMain から呼び、テストの終了コードを返す。
//   - テストのバイナリが偽の gh として起動されたなら、gh として振る舞って終わる（RunFakeGHIfInvoked）。
//   - git を利用者の設定から切り離す。
//   - HOME とプロセスの作業ディレクトリを git 管理下でない一時ディレクトリにし、XDG_CACHE_HOME を外す。
//     キャッシュや状態は一時的な HOME の下に書かれ、デバッグ経路や空の cwd は開発中のリポジトリを指さない。
func Main(m *testing.M) int {
	RunFakeGHIfInvoked()
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_SYSTEM":   "/dev/null",
		"GIT_TERMINAL_PROMPT": "0",
	} {
		_ = os.Setenv(key, value)
	}
	_ = os.Unsetenv("XDG_CACHE_HOME")
	root, err := os.MkdirTemp("", "hhx-hook-test-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	// macOS の一時ディレクトリは symlink の先にあるので、実体のパスで揃える（hook は realpath で比べる）。
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "nonrepo")
	for _, directory := range []string{home, work} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			panic(err)
		}
	}
	_ = os.Setenv("HOME", home)
	if err := os.Chdir(work); err != nil {
		panic(err)
	}
	return m.Run()
}

// TempDir は t.TempDir の実体のパス（symlink を解決したもの）を返す。
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Git は dir で git を実行し、stdout の両端の空白を除いて返す。失敗したらテストを止める。
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	arguments := append(append([]string{"-C", dir}, gitIdentity...), args...)
	command := exec.CommandContext(context.Background(), "git", arguments...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String())
}

// GitRepo は path にリポジトリを作る。origin が空でなければ origin を登録し、commit が真なら commit を 1 つ作る。
func GitRepo(t *testing.T, path, origin string, commit bool) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, path, "init", "-q")
	if origin != "" {
		Git(t, path, "remote", "add", "origin", origin)
	}
	if commit {
		WriteFile(t, filepath.Join(path, "tracked.txt"), "v1\n")
		Git(t, path, "add", "tracked.txt")
		Git(t, path, "commit", "-q", "-m", "init")
	}
	return path
}

// WriteFile は親のディレクトリを作ってから path に content を書く。
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
