package pycompat

import (
	"reflect"
	"regexp"
	"testing"
)

// pythonSpaces は Python の str.isspace() が真になる文字の一覧である（Python 3.14 で列挙したもの）。
var pythonSpaces = []rune{
	0x9, 0xA, 0xB, 0xC, 0xD, 0x1C, 0x1D, 0x1E, 0x1F, 0x20, 0x85, 0xA0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A,
	0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
}

func TestSpaceMatchesPython(t *testing.T) {
	space := regexp.MustCompile(`^` + Space + `$`)
	notSpace := regexp.MustCompile(`^` + NotSpace + `$`)
	exceptNewline := regexp.MustCompile(`^` + SpaceExceptNewline + `$`)
	want := map[rune]bool{}
	for _, r := range pythonSpaces {
		want[r] = true
	}
	for r := rune(0); r <= 0x3100; r++ {
		text := string(r)
		if got := IsSpace(r); got != want[r] {
			t.Errorf("IsSpace(%U)=%v, want %v", r, got, want[r])
		}
		if got := space.MatchString(text); got != want[r] {
			t.Errorf("Space matches %U: %v, want %v", r, got, want[r])
		}
		if got := notSpace.MatchString(text); got == want[r] {
			t.Errorf("NotSpace matches %U: %v", r, got)
		}
		if got := exceptNewline.MatchString(text); got != (want[r] && r != '\n') {
			t.Errorf("SpaceExceptNewline matches %U: %v", r, got)
		}
	}
}

func TestDigitAndWordBoundary(t *testing.T) {
	digit := regexp.MustCompile(`^` + Digit + `+$`)
	for text, want := range map[string]bool{"0123": true, "١٢": true, "１": true, "x": false, "²": false} {
		if got := digit.MatchString(text); got != want {
			t.Errorf("Digit matches %q: %v, want %v", text, got, want)
		}
	}
	boundary := regexp.MustCompile(`api` + NotWordOrEnd)
	for text, want := range map[string]bool{"api": true, "api x": true, "api/x": true, "apix": false, "api_": false, "apié": false, "api١": false} {
		if got := boundary.MatchString(text); got != want {
			t.Errorf("NotWordOrEnd after %q: %v, want %v", text, got, want)
		}
	}
	leading := regexp.MustCompile(NotWordOrStart + `api`)
	for text, want := range map[string]bool{"api": true, "x api": true, "x/api": true, "xapi": false, "_api": false, "éapi": false, "١api": false} {
		if got := leading.MatchString(text); got != want {
			t.Errorf("NotWordOrStart before %q: %v, want %v", text, got, want)
		}
	}
	word := regexp.MustCompile(`^` + Word + `$`)
	notWord := regexp.MustCompile(`^` + NotWord + `$`)
	for text, want := range map[string]bool{"a": true, "_": true, "é": true, "١": true, "²": true, "日": true, "-": false, " ": false, "/": false} {
		if got := word.MatchString(text); got != want {
			t.Errorf("Word matches %q: %v, want %v", text, got, want)
		}
		if got := notWord.MatchString(text); got == want {
			t.Errorf("NotWord matches %q: %v", text, got)
		}
	}
}

func TestFieldsAndStrip(t *testing.T) {
	if got := Fields(" a\x1cb　c\t\n"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Fields=%q", got)
	}
	if got := Fields("   "); len(got) != 0 {
		t.Errorf("Fields of spaces=%q", got)
	}
	if got := Strip("\x1f　 a b \u0085"); got != "a b" {
		t.Errorf("Strip=%q", got)
	}
}

func TestIsWordAndLStrip(t *testing.T) {
	for r, want := range map[rune]bool{'a': true, '_': true, 'é': true, '١': true, '²': true, '日': true, '-': false, ' ': false} {
		if got := IsWord(r); got != want {
			t.Errorf("IsWord(%q)=%v, want %v", r, got, want)
		}
	}
	if got := LStrip("\x1f　 a b \u0085"); got != "a b \u0085" {
		t.Errorf("LStrip=%q", got)
	}
}

// 期待値は Python 3.14 の str.lower() で確かめたものである。
func TestLower(t *testing.T) {
	for text, want := range map[string]string{
		"ABC/Def": "abc/def", "İ": "i\u0307", "/TMP/İX": "/tmp/i\u0307x",
		// Σ は前に大文字小文字のある文字があり、後ろに無いときだけ語末の ς になる。
		"Σ": "σ", "AΣ": "aς", "AΣB": "aσb", "AΣ/B": "aς/b", "AΣ.B": "aσ.b", "A.Σ": "a.ς", "İΣ": "i\u0307ς",
		"AΣ'": "aς'", "1Σ": "1σ",
	} {
		if got := Lower(text); got != want {
			t.Errorf("Lower(%q)=%q, want %q", text, got, want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	for text, want := range map[string][]string{
		"":               nil,
		"a":              {"a"},
		"a\n":            {"a"},
		"a\n\nb":         {"a", "", "b"},
		"a\r\nb\rc\x0bd": {"a", "b", "c", "d"},
		"a\x1cb\u0085c ": {"a", "b", "c"},
		"\n":             {""},
		"a\x1fb":         {"a\x1fb"},
	} {
		if got := SplitLines(text); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitLines(%q)=%q, want %q", text, got, want)
		}
	}
}

func TestQuoteJSON(t *testing.T) {
	for text, want := range map[string]string{
		"echo ok":              `"echo ok"`,
		"a\nb":                 `"a\nb"`,
		`q"b\`:                 `"q\"b\\"`,
		"\t\r\b\f\x01\x1f\x7f": `"\t\r\b\f\u0001\u001f` + "\x7f" + `"`,
		"<&>   日本語":            "\"<&>   日本語\"",
	} {
		if got := QuoteJSON(text); got != want {
			t.Errorf("QuoteJSON(%q)=%q, want %q", text, got, want)
		}
	}
}

// 期待値は Python の bytes.decode("utf-8", "replace") の結果である。
func TestDecodeUTF8Replace(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"\xe3\x81", "�"},
		{"\xe3\x81A", "�A"},
		{"\xe3\x81\x82", "あ"},
		{"\xf0\x9f\x98", "�"},
		{"\xf0\x9f\x98\x80", "\U0001f600"},
		{"\xed\xa0\x80", "���"},
		{"\xc0\xaf", "��"},
		{"\xe0\x80\xaf", "���"},
		{"\xf4\x90\x80\x80", "����"},
		{"\xf5", "�"},
		{"\x80\x80", "��"},
		{"\xc2", "�"},
		{"\xe0\xa0", "�"},
		{"\xf0\x90\x80", "�"},
		{"a\xffb", "a�b"},
		{"\xef\xbf\xbd", "�"},
	} {
		if got := DecodeUTF8Replace([]byte(test.in)); got != test.want {
			t.Errorf("DecodeUTF8Replace(%q)=%q, want %q", test.in, got, test.want)
		}
	}
}
