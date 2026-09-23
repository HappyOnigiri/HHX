package hookexec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutput(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out, ok := Output(dir, 5*time.Second, "pwd", "-P"); !ok || out != resolved+"\n" {
		t.Fatalf("Output=%q %v, want the directory", out, ok)
	}
	if _, ok := Output(dir, 5*time.Second, "false"); ok {
		t.Fatal("a nonzero exit must not be ok")
	}
	if _, ok := Output(dir, 5*time.Second, "hhx-no-such-command"); ok {
		t.Fatal("a missing command must not be ok")
	}
	if _, ok := Output(filepath.Join(dir, "missing"), 5*time.Second, "true"); ok {
		t.Fatal("a missing directory must not be ok")
	}
	started := time.Now()
	if _, ok := Output(dir, 200*time.Millisecond, "sleep", "30"); ok {
		t.Fatal("a timed-out command must not be ok")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the timeout did not stop the command: %s", elapsed)
	}
}

func TestOutputUsesTheProcessDirectoryWhenEmpty(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(cwd)
	if out, ok := Output("", 5*time.Second, "pwd", "-P"); !ok || out != resolved+"\n" {
		t.Fatalf("Output=%q %v", out, ok)
	}
}
