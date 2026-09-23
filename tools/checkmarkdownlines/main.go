// Package main は Markdown の行長を検査する。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const markdownLineLimit = 200

type lineViolation struct {
	line   int
	length int
}

type codeFence struct {
	marker byte
	length int
}

func main() {
	repositoryRoot, err := os.Getwd()
	if err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "failed to determine repository root: %v\n", err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
	os.Exit(run(repositoryRoot, os.Stdout))
}

func run(repositoryRoot string, output io.Writer) int {
	paths, err := findMarkdownFiles(repositoryRoot)
	if err != nil {
		_, _ = fmt.Fprintf(output, "failed to list Markdown files: %v\n", err)
		return 1
	}

	errors := make([]string, 0)
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read file: %v", path, err))
			continue
		}
		for _, violation := range checkContent(string(content)) {
			errors = append(
				errors,
				fmt.Sprintf(
					"%s:%d: %d characters (maximum is %d)",
					path,
					violation.line,
					violation.length,
					markdownLineLimit,
				),
			)
		}
	}

	if len(errors) > 0 {
		_, _ = fmt.Fprintln(output, "Markdown line-length errors:")
		for _, line := range errors {
			_, _ = fmt.Fprintln(output, "- "+line)
		}
		return 1
	}

	_, _ = fmt.Fprintf(output, "No Markdown prose lines exceed %d characters.\n", markdownLineLimit)
	return 0
}

func findMarkdownFiles(repositoryRoot string) ([]string, error) {
	command := exec.CommandContext(
		context.Background(),
		"git",
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
		"-z",
	)
	command.Dir = repositoryRoot
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	paths := make([]string, 0)
	for _, path := range strings.Split(string(output), "\x00") {
		if path == "" || !isMarkdownFile(path) {
			continue
		}
		fileInfo, err := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil || !fileInfo.Mode().IsRegular() {
			continue
		}
		paths = append(paths, filepath.ToSlash(path))
	}
	sort.Strings(paths)
	return paths, nil
}

func isMarkdownFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}

func checkContent(content string) []lineViolation {
	lines := strings.Split(content, "\n")
	violations := make([]lineViolation, 0)
	var fence *codeFence

	for index, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if fence != nil {
			if isClosingFence(line, *fence) {
				fence = nil
			}
			continue
		}
		if openingFence, ok := parseOpeningFence(line); ok {
			fence = &openingFence
			continue
		}

		length := utf8.RuneCountInString(line)
		if length <= markdownLineLimit || isTableRow(line) || isStandaloneToken(line) {
			continue
		}
		violations = append(violations, lineViolation{line: index + 1, length: length})
	}

	return violations
}

func parseOpeningFence(line string) (codeFence, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return codeFence{}, false
	}
	marker := trimmed[0]
	length := countLeadingByte(trimmed, marker)
	if length < 3 {
		return codeFence{}, false
	}
	return codeFence{marker: marker, length: length}, true
}

func isClosingFence(line string, fence codeFence) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < fence.length || trimmed[0] != fence.marker {
		return false
	}
	length := countLeadingByte(trimmed, fence.marker)
	return length >= fence.length && strings.TrimSpace(trimmed[length:]) == ""
}

func countLeadingByte(value string, target byte) int {
	for index := 0; index < len(value); index++ {
		if value[index] != target {
			return index
		}
	}
	return len(value)
}

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func isStandaloneToken(line string) bool {
	fields := strings.Fields(line)
	if len(fields) != 1 {
		return false
	}
	for _, character := range fields[0] {
		if character >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
