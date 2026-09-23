// Package pycompat は、Python から移植した hook の判定を変えないために、Python の文字列と正規表現の意味を Go で再現する。
//
// Go の regexp の \s・\d・\b と strings.Fields は ASCII か Go 独自の空白の定義で動き、Python（str のパターン）と
// 一致する文字の範囲が違う。移植した正規表現はここにある文字クラスで書き、空白での分割もここの関数で行う。
// シェルのコマンドの分割やトークンの解釈は hook ごとに違うので、ここには置かない（AGENTS.md の不変条件）。
package pycompat

import "strings"

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
const (
	Word    = `[\p{L}\p{N}_]`
	NotWord = `[^\p{L}\p{N}_]`
)

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
