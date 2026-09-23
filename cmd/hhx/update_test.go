package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/HappyOnigiri/hhx/internal/update"
)

// fakeUpdate は外部に届かない update の差し替えを入れ、呼ばれた操作を記録する。
type fakeUpdate struct {
	releaseBuild bool
	latest       update.Release
	latestErr    error
	supported    bool
	applied      update.Applied
	applyErr     error
	onPath       string

	checked      bool
	appliedTag   string
	applyWritten string
}

func installFakeUpdate(t *testing.T, fake *fakeUpdate) {
	t.Helper()
	old := updateCommand
	t.Cleanup(func() { updateCommand = old })
	updateCommand = updateAdapters{
		releaseBuild: func() bool { return fake.releaseBuild },
		current:      func() string { return "v1.0.0" },
		latest: func(context.Context) (update.Release, error) {
			fake.checked = true
			return fake.latest, fake.latestErr
		},
		supported: func() bool { return fake.supported },
		apply: func(_ context.Context, tag string, output io.Writer) (update.Applied, error) {
			fake.appliedTag = tag
			if fake.applyWritten != "" {
				_, _ = io.WriteString(output, fake.applyWritten)
			}
			return fake.applied, fake.applyErr
		},
		onPath: func() string { return fake.onPath },
	}
}

func newerRelease() update.Release {
	return update.Release{Tag: "v1.1.0", URL: "https://example.test/releases/v1.1.0"}
}

func TestUpdateIsDisabledForDevelopmentBuilds(t *testing.T) {
	fake := &fakeUpdate{}
	installFakeUpdate(t, fake)
	code, stdout, _ := runCommand(t, "", "update", "--apply")
	if code != 0 || stdout != messages.Text(testLanguage, idDevelopment, map[string]any{"Version": "v1.0.0"})+"\n" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if fake.checked || fake.appliedTag != "" {
		t.Fatal("a development build reached the network")
	}
}

func TestUpdateReportsAnAvailableRelease(t *testing.T) {
	fake := &fakeUpdate{releaseBuild: true, latest: newerRelease(), supported: true}
	installFakeUpdate(t, fake)
	code, stdout, _ := runCommand(t, "", "update")
	if code != 0 || stdout != messages.Text(testLanguage, idAvailable, map[string]any{
		"Latest": "v1.1.0", "Version": "v1.0.0", "URL": newerRelease().URL,
	}) || !strings.Contains(stdout, "hhx update --apply") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if fake.appliedTag != "" {
		t.Fatal("the check alone applied the update")
	}
}

func TestUpdateReportsUpToDate(t *testing.T) {
	fake := &fakeUpdate{releaseBuild: true, latest: update.Release{Tag: "v1.0.0"}, supported: true}
	installFakeUpdate(t, fake)
	code, stdout, _ := runCommand(t, "", "update", "--apply")
	if code != 0 || !strings.Contains(stdout, messages.Text(testLanguage, idUpToDate, map[string]any{"Version": "v1.0.0"})) ||
		fake.appliedTag != "" {
		t.Fatalf("code=%d stdout=%q applied=%q", code, stdout, fake.appliedTag)
	}
}

func TestUpdateCheckFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		want     string
	}{
		{name: "rate limited", err: update.ErrRateLimited, wantCode: 1, want: "rate limit"},
		{name: "no release", err: update.ErrUnavailable, wantCode: 0, want: messages.Text(testLanguage, idNoReleaseYet, map[string]any{"Version": "v1.0.0"})},
		{name: "network", err: errors.New("dial tcp: connection refused"), wantCode: 1, want: "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installFakeUpdate(t, &fakeUpdate{releaseBuild: true, latestErr: test.err, supported: true})
			code, stdout, stderr := runCommand(t, "", "update")
			if code != test.wantCode || !strings.Contains(stdout+stderr, test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestUpdateApplyRunsTheInstaller(t *testing.T) {
	fake := &fakeUpdate{
		releaseBuild: true, latest: newerRelease(), supported: true,
		applied:      update.Applied{Tag: "v1.1.0", Path: "/Users/alice/.local/bin/hhx"},
		applyWritten: "Installed hhx v1.1.0 to /Users/alice/.local/bin/hhx\n",
		onPath:       "/Users/alice/.local/bin/hhx",
	}
	installFakeUpdate(t, fake)
	code, stdout, stderr := runCommand(t, "", "update", "--apply")
	if code != 0 || fake.appliedTag != "v1.1.0" {
		t.Fatalf("code=%d applied=%q stderr=%q", code, fake.appliedTag, stderr)
	}
	if !strings.Contains(stdout, "Installed hhx v1.1.0") || strings.Contains(stdout, "/opt/tools") ||
		strings.Contains(stdout, messages.Text(testLanguage, idOtherOnPath, map[string]any{"OnPath": "/Users/alice/.local/bin/hhx", "Path": "/Users/alice/.local/bin/hhx"})) {
		t.Fatalf("stdout=%q", stdout)
	}
}

func TestUpdateApplyWarnsWhenPathUsesAnotherBinary(t *testing.T) {
	installFakeUpdate(t, &fakeUpdate{
		releaseBuild: true, latest: newerRelease(), supported: true,
		applied: update.Applied{Tag: "v1.1.0", Path: "/Users/alice/.local/bin/hhx"},
		onPath:  "/opt/tools/hhx",
	})
	code, stdout, _ := runCommand(t, "", "update", "--apply")
	if code != 0 || !strings.Contains(stdout, messages.Text(testLanguage, idOtherOnPath, map[string]any{
		"OnPath": "/opt/tools/hhx", "Path": "/Users/alice/.local/bin/hhx",
	})) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestUpdateApplyFailures(t *testing.T) {
	t.Run("installer failed", func(t *testing.T) {
		installFakeUpdate(t, &fakeUpdate{
			releaseBuild: true, latest: newerRelease(), supported: true,
			applyErr: errors.New("the installer for v1.1.0 failed: exit status 1"),
		})
		code, _, stderr := runCommand(t, "", "update", "--apply")
		if code != 1 || !strings.Contains(stderr, "exit status 1") {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	})
	t.Run("unsupported platform", func(t *testing.T) {
		fake := &fakeUpdate{releaseBuild: true, latest: newerRelease()}
		installFakeUpdate(t, fake)
		code, _, stderr := runCommand(t, "", "update", "--apply")
		if code != 1 || stderr != messages.T(testLanguage, idUnsupported)+"\n" || fake.appliedTag != "" {
			t.Fatalf("code=%d stderr=%q applied=%q", code, stderr, fake.appliedTag)
		}
	})
}

func TestUpdateRejectsUnexpectedArguments(t *testing.T) {
	installFakeUpdate(t, &fakeUpdate{})
	if code, _, _ := runCommand(t, "", "update", "now"); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if code, _, _ := runCommand(t, "", "update", "--bogus"); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if code, _, _ := runCommand(t, "", "update", "--help"); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestDefaultUpdateAdaptersAreWired(t *testing.T) {
	adapters := defaultUpdateAdapters()
	if adapters.releaseBuild() {
		t.Fatal("the test binary must be a development build")
	}
	if adapters.current() == "" || adapters.latest == nil || adapters.apply == nil || adapters.supported == nil {
		t.Fatal("an adapter is missing")
	}
	t.Setenv("PATH", t.TempDir())
	if got := adapters.onPath(); got != "" {
		t.Fatalf("onPath()=%q with an empty PATH", got)
	}
}
