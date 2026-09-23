package pycompat

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 期待値は Python 3.14 の os.path で確かめたものである。

func TestNormpath(t *testing.T) {
	for path, want := range map[string]string{
		"": ".", ".": ".", "/": "/", "//": "//", "///": "/", "//a//b/": "//a/b", "///a": "/a", "a/./b/../c": "a/c",
		"../a": "../a", "a/../..": "..", "/..": "/", "/../a": "/a", "a/b/../../..": "..", "../..": "../..",
		"a/\n/b": "a/\n/b",
	} {
		if got := Normpath(path); got != want {
			t.Errorf("Normpath(%q)=%q, want %q", path, got, want)
		}
	}
}

func TestSimplePathFunctions(t *testing.T) {
	for path, want := range map[string]string{
		"/a/b": "/a", "/a/b/": "/a/b", "a": "", "/": "/", "//": "//", "//a": "//", "a//b": "a", "/a": "/",
	} {
		if got := Dirname(path); got != want {
			t.Errorf("Dirname(%q)=%q, want %q", path, got, want)
		}
	}
	for pair, want := range map[[2]string]string{
		{"a", "b"}: "a/b", {"a/", "b"}: "a/b", {"a", "/b"}: "/b", {"", "b"}: "b", {"a", ""}: "a/",
	} {
		if got := Join(pair[0], pair[1]); got != want {
			t.Errorf("Join(%q, %q)=%q, want %q", pair[0], pair[1], got, want)
		}
	}
	if !IsAbs("/a") || IsAbs("a") || IsAbs("") {
		t.Error("IsAbs")
	}
}

func TestRelpath(t *testing.T) {
	for pair, want := range map[[2]string]string{
		{"/a/b", "/a/b"}: ".", {"/a/b", "/a"}: "b", {"/a", "/a/b"}: "..", {"/a/b", "/c/d"}: "../../a/b",
		{"/", "/a"}: "..", {"/a", "/"}: "a", {"//a", "/a"}: ".",
	} {
		if got, err := Relpath(pair[0], pair[1]); err != nil || got != want {
			t.Errorf("Relpath(%q, %q)=%q, %v; want %q", pair[0], pair[1], got, err, want)
		}
	}
	if _, err := Relpath("", "/a"); !errors.Is(err, ErrEmptyPath) {
		t.Errorf("Relpath of an empty path: %v", err)
	}
	cwd, err := Getcwd()
	if err != nil {
		t.Fatal(err)
	}
	// 相対パスは作業ディレクトリを基準にする。
	if got, err := Relpath("x", cwd); err != nil || got != "x" {
		t.Errorf("Relpath(x, cwd)=%q, %v", got, err)
	}
}

func TestRealpath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(root, "real", "sub"))
	mustSymlink(t, "real", filepath.Join(root, "link"))
	mustSymlink(t, filepath.Join(root, "real", "sub"), filepath.Join(root, "abs"))
	mustSymlink(t, "loop-b", filepath.Join(root, "loop-a"))
	mustSymlink(t, "loop-a", filepath.Join(root, "loop-b"))
	mustSymlink(t, "../real", filepath.Join(root, "real", "up"))
	for path, want := range map[string]string{
		root + "/link/sub":           root + "/real/sub",
		root + "/link/missing/x":     root + "/real/missing/x", // 存在しない末尾はそのまま残す
		root + "/abs/..":             root + "/real",
		root + "//link/./sub/../sub": root + "/real/sub",
		root + "/real/up/up/sub":     root + "/real/sub",
		root + "/loop-a/x":           root + "/loop-a/x", // 循環はその位置で解決をやめる
		"/":                          "/",
		"/..":                        "/",
	} {
		if got, err := Realpath(path); err != nil || got != want {
			t.Errorf("Realpath(%q)=%q, %v; want %q", path, got, err, want)
		}
	}
	t.Chdir(root)
	if got, err := Realpath("link"); err != nil || got != root+"/real" {
		t.Errorf("Realpath(link)=%q, %v", got, err)
	}
	if _, err := Realpath("/a\x00b"); !errors.Is(err, ErrNUL) {
		t.Errorf("Realpath with NUL: %v", err)
	}
}

func TestHome(t *testing.T) {
	for home, want := range map[string]string{"/Users/alice": "/Users/alice", "/Users/alice//": "/Users/alice", "": "/", "/": "/"} {
		t.Setenv("HOME", home)
		if got := Home(); got != want {
			t.Errorf("Home() with HOME=%q is %q, want %q", home, got, want)
		}
	}
	t.Setenv("HOME", "")
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatal(err)
	}
	// HOME が無ければパスワードデータベースのホームディレクトリを使う。
	if got := Home(); got == "" || got == "~" {
		t.Errorf("Home() without HOME=%q", got)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
