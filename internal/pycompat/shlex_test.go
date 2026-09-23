package pycompat

import (
	"strings"
	"testing"
)

// 期待値は Python 3.14 の shlex で作った。
func TestShlexSplit(t *testing.T) {
	for _, testCase := range []struct {
		input, whitespace, punctuation string
		want                           []string
		fails                          bool
	}{
		{"a b", " \t\r", ";&|\n", []string{"a", "b"}, false},
		{"a b", " \t\r\n", "", []string{"a", "b"}, false},
		{"  a\tb  ", " \t\r", ";&|\n", []string{"a", "b"}, false},
		{"  a\tb  ", " \t\r\n", "", []string{"a", "b"}, false},
		{"a'b c'd", " \t\r", ";&|\n", []string{"ab cd"}, false},
		{"a'b c'd", " \t\r\n", "", []string{"ab cd"}, false},
		{"a\"b\\\"c\"", " \t\r", ";&|\n", []string{"ab\"c"}, false},
		{"a\"b\\\"c\"", " \t\r\n", "", []string{"ab\"c"}, false},
		{"a\\ b", " \t\r", ";&|\n", []string{"a b"}, false},
		{"a\\ b", " \t\r\n", "", []string{"a b"}, false},
		{"'a\\b'", " \t\r", ";&|\n", []string{"a\\b"}, false},
		{"'a\\b'", " \t\r\n", "", []string{"a\\b"}, false},
		{"\"a\\b\"", " \t\r", ";&|\n", []string{"a\\b"}, false},
		{"\"a\\b\"", " \t\r\n", "", []string{"a\\b"}, false},
		{"\"a\\\\b\"", " \t\r", ";&|\n", []string{"a\\b"}, false},
		{"\"a\\\\b\"", " \t\r\n", "", []string{"a\\b"}, false},
		{"''", " \t\r", ";&|\n", []string{""}, false},
		{"''", " \t\r\n", "", []string{""}, false},
		{"\"\"x", " \t\r", ";&|\n", []string{"x"}, false},
		{"\"\"x", " \t\r\n", "", []string{"x"}, false},
		{"a;b", " \t\r", ";&|\n", []string{"a", ";", "b"}, false},
		{"a;b", " \t\r\n", "", []string{"a;b"}, false},
		{"a;;b|c&&d", " \t\r", ";&|\n", []string{"a", ";;", "b", "|", "c", "&&", "d"}, false},
		{"a;;b|c&&d", " \t\r\n", "", []string{"a;;b|c&&d"}, false},
		{"a ;b", " \t\r", ";&|\n", []string{"a", ";", "b"}, false},
		{"a ;b", " \t\r\n", "", []string{"a", ";b"}, false},
		{"x'|'y", " \t\r", ";&|\n", []string{"x|y"}, false},
		{"x'|'y", " \t\r\n", "", []string{"x|y"}, false},
		{"a\nb", " \t\r", ";&|\n", []string{"a", "\n", "b"}, false},
		{"a\nb", " \t\r\n", "", []string{"a", "b"}, false},
		{"a\\\nb", " \t\r", ";&|\n", []string{"a\nb"}, false},
		{"a\\\nb", " \t\r\n", "", []string{"a\nb"}, false},
		{"'unterminated", " \t\r", ";&|\n", nil, true},
		{"'unterminated", " \t\r\n", "", nil, true},
		{"tail\\", " \t\r", ";&|\n", nil, true},
		{"tail\\", " \t\r\n", "", nil, true},
		{"\"x", " \t\r", ";&|\n", nil, true},
		{"\"x", " \t\r\n", "", nil, true},
		{"echo 'a;b'; git push", " \t\r", ";&|\n", []string{"echo", "a;b", ";", "git", "push"}, false},
		{"echo 'a;b'; git push", " \t\r\n", "", []string{"echo", "a;b;", "git", "push"}, false},
		{"a|&b", " \t\r", ";&|\n", []string{"a", "|&", "b"}, false},
		{"a|&b", " \t\r\n", "", []string{"a|&b"}, false},
		{"# not a comment", " \t\r", ";&|\n", []string{"#", "not", "a", "comment"}, false},
		{"# not a comment", " \t\r\n", "", []string{"#", "not", "a", "comment"}, false},
		{"日本 語", " \t\r", ";&|\n", []string{"日本", "語"}, false},
		{"日本 語", " \t\r\n", "", []string{"日本", "語"}, false},
		{"a\rb", " \t\r", ";&|\n", []string{"a", "b"}, false},
		{"a\rb", " \t\r\n", "", []string{"a", "b"}, false},
		{";", " \t\r", ";&|\n", []string{";"}, false},
		{";", " \t\r\n", "", []string{";"}, false},
		{"a';'", " \t\r", ";&|\n", []string{"a;"}, false},
		{"a';'", " \t\r\n", "", []string{"a;"}, false},
		{"x\"'\"y", " \t\r", ";&|\n", []string{"x'y"}, false},
		{"x\"'\"y", " \t\r\n", "", []string{"x'y"}, false},
		{"", " \t\r", ";&|\n", []string{}, false},
		{"", " \t\r\n", "", []string{}, false},
		{"   ", " \t\r", ";&|\n", []string{}, false},
		{"   ", " \t\r\n", "", []string{}, false},
		{"a ''", " \t\r", ";&|\n", []string{"a", ""}, false},
		{"a ''", " \t\r\n", "", []string{"a", ""}, false},
		{"'' b", " \t\r", ";&|\n", []string{"", "b"}, false},
		{"'' b", " \t\r\n", "", []string{"", "b"}, false},
		{"a;'' b", " \t\r", ";&|\n", []string{"a", ";", "", "b"}, false},
		{"a;'' b", " \t\r\n", "", []string{"a;", "b"}, false},
	} {
		got, err := ShlexSplit(testCase.input, testCase.whitespace, testCase.punctuation)
		if (err != nil) != testCase.fails || strings.Join(got, "\x00") != strings.Join(testCase.want, "\x00") || len(got) != len(testCase.want) {
			t.Errorf("ShlexSplit(%q, %q, %q)=%q %v, want %q (fails=%v)", testCase.input, testCase.whitespace,
				testCase.punctuation, got, err, testCase.want, testCase.fails)
		}
	}
}

func TestShlexQuote(t *testing.T) {
	for _, testCase := range []struct{ input, want string }{
		{"", "''"},
		{"abc", "abc"},
		{"a b", "'a b'"},
		{"it's", "'it'\"'\"'s'"},
		{"日本", "'日本'"},
		{"a-b_c.d/e:f=g+h@i%j,k", "a-b_c.d/e:f=g+h@i%j,k"},
		{"$x", "'$x'"},
		{"a\nb", "'a\nb'"},
	} {
		if got := ShlexQuote(testCase.input); got != testCase.want {
			t.Errorf("ShlexQuote(%q)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
}
