package version

import (
	"os"
	"path/filepath"
	"testing"
)

// 確認用: 1 回目だけ落ち、citest の再実行で通る。
func TestFlakyProbe(t *testing.T) {
	marker := filepath.Join(os.TempDir(), "hhx-flaky-probe")
	if _, err := os.Stat(marker); err != nil {
		if err := os.WriteFile(marker, []byte("seen"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Fatal("first run fails on purpose")
	}
}
