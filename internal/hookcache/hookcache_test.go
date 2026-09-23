package hookcache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	if dir, err := Dir("x"); err != nil || dir != filepath.Join(home, ".cache", "hhx", "x") {
		t.Fatalf("Dir=%q %v", dir, err)
	}
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	if dir, _ := Dir("x"); dir != filepath.Join(home, ".cache", "hhx", "x") {
		t.Fatalf("a relative XDG_CACHE_HOME must be ignored: %q", dir)
	}
	t.Setenv("XDG_CACHE_HOME", "/xdg")
	if dir, _ := Dir("x"); dir != "/xdg/hhx/x" {
		t.Fatalf("XDG_CACHE_HOME: %q", dir)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := Dir("x"); err == nil {
		t.Fatal("an empty HOME must be an error")
	}
	t.Setenv("HOME", "relative")
	if _, err := Dir("x"); err == nil {
		t.Fatal("a relative HOME must be an error")
	}
}

func TestWriteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := WriteFile(dir, "f.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, "f.json", []byte("[1]")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "f.json"))
	if err != nil || string(data) != "[1]" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	info, _ := os.Stat(dir)
	file, _ := os.Stat(filepath.Join(dir, "f.json"))
	if info.Mode().Perm() != 0o700 || file.Mode().Perm() != 0o600 {
		t.Fatalf("modes: dir=%v file=%v", info.Mode(), file.Mode())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files were left: %v", entries)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if err := WriteFile(filepath.Join(blocker, "sub"), "f", nil); err == nil {
		t.Fatal("a directory under a file must fail")
	}
	readOnly := filepath.Join(t.TempDir(), "ro")
	_ = os.Mkdir(readOnly, 0o500)
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	if os.Geteuid() != 0 {
		if err := WriteFile(readOnly, "f", nil); err == nil {
			t.Fatal("a read-only directory must fail")
		}
	}
}
