// Package pycompat は、Python から移植した hook の判定を変えないために、Python の文字列と正規表現の意味を Go で再現する。
//
// Go の regexp の \s・\d・\b と strings.Fields は ASCII か Go 独自の空白の定義で動き、Python（str のパターン）と
// 一致する文字の範囲が違う。移植した正規表現はここにある文字クラスで書き、空白での分割もここの関数で行う。
// シェルのコマンドの分割やトークンの解釈は hook ごとに違うので、ここには置かない（AGENTS.md の不変条件）。
// 文字の分類は Go の unicode の表で行うので、Unicode の版の違い（Python 3.14 は 16.0）による差は残る。
package pycompat

import (
	"strings"
	"unicode"
)

// SpaceChars は Python の str.isspace() と、str パターンの \s が一致する文字の集合を、文字クラスの中身として書いたものである。
// `[^/\s]` のような文字クラスは `[^/` + SpaceChars + `]` と書く。
// U+001C〜U+001F と U+0085 は Go の unicode.IsSpace と扱いが違う。
const (
	spaceCharsExceptNewline = `\t\x0B\f\r\x1C-\x1F \x{85}\x{A0}\x{1680}` +
		`\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`
	SpaceChars = `\n` + spaceCharsExceptNewline
)

// Space は Python の \s にあたる文字クラス、NotSpace は \S にあたる文字クラスである。
const (
	Space    = `[` + SpaceChars + `]`
	NotSpace = `[^` + SpaceChars + `]`
)

// SpaceExceptNewline は Python の \s から改行（\n）だけを除いた文字クラスである。
const SpaceExceptNewline = `[` + spaceCharsExceptNewline + `]`

// Digit は Python の \d（Unicode の Nd）にあたる文字クラスである。
const Digit = `\p{Nd}`

// Word は Python の \w、NotWord は \W にあたる文字クラスである。
// Python の \w は str.isalnum() の文字と _ で、Go の \w と \b は ASCII の英数字と _ しか語の文字とみなさない。
// WordChars はその文字クラスの中身で、`[\w.-]` のような文字クラスは `[` + WordChars + `.-]` と書く。
const (
	WordChars = `\p{L}\p{N}_`
	Word      = `[` + WordChars + `]`
	NotWord   = `[^` + WordChars + `]`
)

// IsWord は Python の \w と同じ判定を 1 文字に対して行う。
func IsWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
}

// NotWordOrEnd は Python の \w の直後に置いた \b と同じ位置で一致する。
// 1 文字を消費するので、後ろに続くパターンが無いときにだけ使う。
const NotWordOrEnd = `(?:` + NotWord + `|$)`

// NotWordOrStart は Python の \w の直前に置いた \b と同じ位置で一致する。
// 直前の 1 文字を消費するので、前に続くパターンが無いときにだけ使う。
const NotWordOrStart = `(?:^|` + NotWord + `)`

// IsSpace は Python の str.isspace() と同じ判定を 1 文字に対して行う。
func IsSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r >= 0x1C && r <= 0x20, r == 0x85, r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A, r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000:
		return true
	default:
		return false
	}
}

// Fields は Python の str.split()（引数なし）と同じく、空白の並びで分割し、空の要素を返さない。
func Fields(s string) []string {
	return strings.FieldsFunc(s, IsSpace)
}

// Strip は Python の str.strip()（引数なし）と同じく、両端の空白を除く。
func Strip(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// LStrip は Python の str.lstrip()（引数なし）と同じく、先頭の空白を除く。
func LStrip(s string) string {
	return strings.TrimLeftFunc(s, IsSpace)
}

// Lower は Python の str.lower() と同じく小文字にする。
// Go の strings.ToLower と違い、İ（U+0130）を i と結合用のドット（U+0307）の 2 文字にし、
// Σ は語末（Unicode の Final_Sigma の文脈）でだけ ς にする。
func Lower(s string) string {
	runes := []rune(s)
	var out strings.Builder
	for index, r := range runes {
		switch r {
		case 'İ':
			out.WriteString("i\u0307")
		case 'Σ':
			if finalSigma(runes, index) {
				out.WriteRune('ς')
			} else {
				out.WriteRune('σ')
			}
		default:
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

// finalSigma は runes[index] の Σ が Final_Sigma の文脈にあるかを返す（CPython の handle_capital_sigma と同じ判定）。
// 前に（大文字小文字を無視できる文字を飛ばして）大文字小文字のある文字があり、後ろには無いときである。
func finalSigma(runes []rune, index int) bool {
	before := index - 1
	for before >= 0 && isCaseIgnorable(runes[before]) {
		before--
	}
	if before < 0 || !isCased(runes[before]) {
		return false
	}
	after := index + 1
	for after < len(runes) && isCaseIgnorable(runes[after]) {
		after++
	}
	return after == len(runes) || !isCased(runes[after])
}

func isCased(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

// isCaseIgnorable は Unicode の Case_Ignorable である。
// Go の unicode に Word_Break の表が無いので、MidLetter・MidNumLet・Single_Quote の文字は並べて書く。
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xB7, 0x387, 0x55F, 0x5F4, 0x2018, 0x2019, 0x2024, 0x2027,
		0xFE13, 0xFE52, 0xFE55, 0xFF07, 0xFF0E, 0xFF1A:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// isLineBoundary は Python の str.splitlines() が行の区切りとみなす文字である。
func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\x0B', '\f', 0x1C, 0x1D, 0x1E, 0x85, 0x2028, 0x2029:
		return true
	default:
		return false
	}
}

// SplitLines は Python の str.splitlines() と同じく行に分ける。
// \r\n は 1 つの区切りとし、末尾の区切りの後ろに空の行を作らない。
func SplitLines(s string) []string {
	var lines []string
	start := 0
	for index, r := range s {
		if index < start || !isLineBoundary(r) {
			continue
		}
		lines = append(lines, s[start:index])
		start = index + len(string(r))
		if r == '\r' && strings.HasPrefix(s[start:], "\n") {
			start++
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// QuoteJSON は Python の json.dumps(s, ensure_ascii=False) と同じ JSON 文字列を返す。
// Go の encoding/json と違い、< > & と U+2028・U+2029 をエスケープしない。
// 理由文に埋め込んだ文字列をモデルがそのまま読むためである。
func QuoteJSON(s string) string {
	const hex = "0123456789abcdef"
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hex[r>>4])
				out.WriteByte(hex[r&0xF])
				continue
			}
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
