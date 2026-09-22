package main

import (
	"strings"
	"testing"
)

func TestCheckContent(t *testing.T) {
	longText := strings.Repeat("a", markdownLineLimit+1)
	testCases := []struct {
		name    string
		content string
		want    []lineViolation
	}{
		{
			name:    "accepts limit",
			content: strings.Repeat("a", markdownLineLimit),
		},
		{
			name:    "rejects prose above limit",
			content: "short\n" + longText + " text\n",
			want:    []lineViolation{{line: 2, length: markdownLineLimit + 6}},
		},
		{
			name:    "counts Unicode characters",
			content: strings.Repeat("界", markdownLineLimit) + " 界",
			want:    []lineViolation{{line: 1, length: markdownLineLimit + 2}},
		},
		{
			name:    "checks Japanese prose without spaces",
			content: strings.Repeat("界", markdownLineLimit+1),
			want:    []lineViolation{{line: 1, length: markdownLineLimit + 1}},
		},
		{
			name:    "allows fenced code",
			content: "```text\n" + longText + " text\n```\n~~~\n" + longText + " text\n~~~~",
		},
		{
			name:    "allows outer-pipe table rows",
			content: "| " + longText + " text |",
		},
		{
			name:    "allows standalone indivisible token",
			content: longText,
		},
		{
			name:    "checks prose after a closing fence",
			content: "````\n" + longText + "\n````\n" + longText + " text",
			want:    []lineViolation{{line: 4, length: markdownLineLimit + 6}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := checkContent(testCase.content)
			if len(got) != len(testCase.want) {
				t.Fatalf("checkContent() = %#v, want %#v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf("checkContent()[%d] = %#v, want %#v", index, got[index], testCase.want[index])
				}
			}
		})
	}
}

func TestIsMarkdownFile(t *testing.T) {
	testCases := []struct {
		path string
		want bool
	}{
		{path: "README.md", want: true},
		{path: "docs/guide.MARKDOWN", want: true},
		{path: "docs/guide.txt", want: false},
	}

	for _, testCase := range testCases {
		if got := isMarkdownFile(testCase.path); got != testCase.want {
			t.Errorf("isMarkdownFile(%q) = %t, want %t", testCase.path, got, testCase.want)
		}
	}
}
