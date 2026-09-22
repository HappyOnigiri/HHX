package config

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestMissingFileUsesDefaults(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled("any", true) || config.Enabled("any", false) {
		t.Fatal("a missing file must fall back to each hook's default")
	}
}

func TestEmptyOrCommentOnlyFileUsesDefaults(t *testing.T) {
	for _, content := range []string{"", "# nothing yet\n", "hooks:\n"} {
		config, err := load(t, content)
		if err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		if !config.Enabled("any", true) {
			t.Fatalf("%q must fall back to defaults", content)
		}
	}
}

func TestEnabledAndHookSettings(t *testing.T) {
	config, err := load(t, `
hooks:
  off-guard:
    enabled: false
  on-guard:
    enabled: true
  tuned-guard:
    message: hello
    limit: 3
  bare-guard:
`)
	if err != nil {
		t.Fatal(err)
	}
	if config.Enabled("off-guard", true) || !config.Enabled("on-guard", false) {
		t.Fatal("enabled must override the default")
	}
	if !config.Enabled("tuned-guard", true) || !config.Enabled("bare-guard", true) || config.Enabled("bare-guard", false) {
		t.Fatal("a hook without enabled must keep its default")
	}
	var settings struct {
		Message string `yaml:"message"`
		Limit   int    `yaml:"limit"`
	}
	if err := config.Decode("tuned-guard", &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Message != "hello" || settings.Limit != 3 {
		t.Fatalf("hook settings were not decoded: %+v", settings)
	}
	settings.Message = "kept"
	if err := config.Decode("absent-guard", &settings); err != nil || settings.Message != "kept" {
		t.Fatal("decoding an absent hook must leave the value unchanged")
	}
}

func TestInvalidConfigIsReported(t *testing.T) {
	for name, content := range map[string]string{
		"unknown top-level key": "hookz: {}\n",
		"hooks not mapping":     "hooks: [a]\n",
		"hook not mapping":      "hooks:\n  x: yes\n",
		"enabled not bool":      "hooks:\n  x:\n    enabled: maybe\n",
		"duplicate hook":        "hooks:\n  x: {}\n  x: {}\n",
		"multiple documents":    "hooks: {}\n---\nhooks: {}\n",
		"syntax error":          "hooks: [\n",
	} {
		if _, err := load(t, content); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestUnknownHooks(t *testing.T) {
	config, err := load(t, "hooks:\n  known: {}\n  typo-b: {}\n  typo-a: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	got := config.UnknownHooks(func(name string) bool { return name == "known" })
	if len(got) != 2 || got[0] != "typo-a" || got[1] != "typo-b" {
		t.Fatalf("UnknownHooks()=%v", got)
	}
}

func TestDefaultPathHonorsEnvironment(t *testing.T) {
	t.Setenv(PathEnv, "/tmp/custom.yaml")
	if path, err := DefaultPath(); err != nil || path != "/tmp/custom.yaml" {
		t.Fatalf("DefaultPath()=(%q, %v)", path, err)
	}
	t.Setenv(PathEnv, "")
	t.Setenv("HOME", "/home/someone")
	if path, err := DefaultPath(); err != nil || path != "/home/someone/.config/hhx/config.yaml" {
		t.Fatalf("DefaultPath()=(%q, %v)", path, err)
	}
}
