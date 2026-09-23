package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
)

func writeManifest(reportDir string, value manifest) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(filepath.Join(reportDir, "manifest.json"), data)
}

func runRecordFromResult(result testResult, command []string, label, coverage string) runRecord {
	coverageName := ""
	if coverage != "" {
		coverageName = filepath.Base(coverage)
	}
	return runRecord{
		Command:          command,
		Exit:             result.Exit,
		Status:           result.Status,
		Signal:           result.Signal,
		JSONL:            label + ".jsonl",
		Log:              label + ".log",
		Stderr:           label + ".stderr",
		Coverage:         coverageName,
		Shuffle:          result.Shuffle,
		ShuffleByPackage: result.ShuffleByPackage,
		StartedAt:        result.StartedAt,
		FinishedAt:       result.FinishedAt,
		DurationMS:       durationMS(result.StartedAt, result.FinishedAt),
		LogExcerpt:       result.LogExcerpt,
	}
}

// testSHA はテストした commit を返す。git が無い・リポジトリでない場合は空にし、記録だけを欠く。
func testSHA(root string) string {
	command := exec.CommandContext(context.Background(), "git", "-C", root, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
