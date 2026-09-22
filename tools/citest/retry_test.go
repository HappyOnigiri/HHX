package main

import (
	"strings"
	"testing"
)

func TestRetryCommandReplacesRunAndRestrictsPackages(t *testing.T) {
	command, _, err := retryCommand(config{
		Profile:  "race-coverage",
		RepoRoot: t.TempDir(),
		Command:  []string{"go", "test", "-run", "^OldRoot$", "-shuffle=on", "./internal/hookrt", "./internal/config"},
	}, "./internal/hookrt", []string{"TestFirst", "TestSecond"}, "123", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(command, " "), "go test -json -run=^(TestFirst|TestSecond)$ -shuffle=123 ./internal/hookrt"; got != want {
		t.Fatalf("retry command=%q, want %q", got, want)
	}
}

func TestRetryCommandReplacesSeparatedRunFlag(t *testing.T) {
	command, _, err := retryCommand(config{
		Profile:  "race-coverage",
		RepoRoot: t.TempDir(),
		Command:  []string{"go", "test", "-run", "^OldRoot$", "./internal/config"},
	}, "./internal/config", []string{"TestA"}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "-run=^(TestA)$") || strings.Contains(joined, "OldRoot") {
		t.Fatalf("retry command=%q did not replace -run", joined)
	}
}
