// Package toolresponse は PostToolUse の payload にあるツールの実行結果（tool_response）を読む。
//
// push-ci-context と pr-body-staleness が同じ判定を使う。Claude Code の PostToolUse は成功したときだけ発火するが、
// 失敗を別のイベントにしない CLI に備えて、出力の側でも失敗を弾く（移植元と同じ）。
// 値は encoding/json に UseNumber を付けて読んだもの（map[string]any・[]any・json.Number・string・bool・nil）を受け取る。
package toolresponse

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"

	py "github.com/HappyOnigiri/hhx/internal/pycompat"
)

// exitCodeKeys は CLI ごとに違う終了コードの項目名である。
var exitCodeKeys = []string{"exit_code", "exitCode", "returnCode", "return_code", "code"}

// outputKeys は出力の項目名である。Text はこの順に並べる。
var outputKeys = []string{"stdout", "stderr", "output", "aggregated_output"}

// noChangeMarkers は、push しても新しい commit が無く CI も PR の commit も動かない結果である。
var noChangeMarkers = []string{"everything up-to-date", "everything up to date"}

// failureMarkers は push が失敗したことを示す出力である（終了コードを持たない CLI 向けの保険）。
var failureMarkers = []string{"! [rejected]", "error: failed to push", "fatal:", "permission denied"}

// Succeeded はツールの実行結果が成功かを返す。判断材料が無ければ成功として扱う。
func Succeeded(response any) bool {
	fields, ok := response.(map[string]any)
	if !ok {
		return true
	}
	for _, key := range exitCodeKeys {
		// Python の isinstance(value, int) は bool を除いた整数だけを見る。1.0 のような小数は終了コードとみなさない。
		if number, ok := fields[key].(json.Number); ok && isInteger(number) && !isZero(number) {
			return false
		}
	}
	if success, ok := fields["success"].(bool); ok && !success {
		return false
	}
	if interrupted, ok := fields["interrupted"].(bool); ok && interrupted {
		return false
	}
	text := py.Lower(Text(response))
	for _, marker := range noChangeMarkers {
		if strings.Contains(text, marker) {
			return false
		}
	}
	for _, marker := range failureMarkers {
		if strings.Contains(text, marker) {
			return false
		}
	}
	return true
}

// Text は実行結果の出力を 1 つの文字列にまとめる（移植元の " ".join(str(response.get(key) or "") ...)）。
// 実行結果がオブジェクトでなければ空文字列を返す。
func Text(response any) string {
	fields, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	parts := make([]string, len(outputKeys))
	for index, key := range outputKeys {
		if value := fields[key]; Truthy(value) {
			parts[index] = PyStr(value)
		}
	}
	return strings.Join(parts, " ")
}

// Truthy は Python の bool(value) である。
func Truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case json.Number:
		return !isZero(typed)
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// PyStr は Python の str(value) である。
// オブジェクトの項目の順序は JSON の順を保たないので、名前の順に並べる（出力の文言の検索にしか使わない）。
func PyStr(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return pyRepr(value)
}

func pyRepr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case json.Number:
		return pyNumber(typed)
	case string:
		return pyStringRepr(typed)
	case []any:
		items := make([]string, len(typed))
		for index, item := range typed {
			items[index] = pyRepr(item)
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]string, len(keys))
		for index, key := range keys {
			items[index] = pyStringRepr(key) + ": " + pyRepr(typed[key])
		}
		return "{" + strings.Join(items, ", ") + "}"
	default:
		return ""
	}
}

func isInteger(number json.Number) bool {
	return !strings.ContainsAny(number.String(), ".eE")
}

func isZero(number json.Number) bool {
	value, err := strconv.ParseFloat(number.String(), 64)
	return err == nil && value == 0
}

// pyNumber は JSON の数値を Python の str(int) か repr(float) の形にする。
func pyNumber(number json.Number) string {
	text := number.String()
	if isInteger(number) {
		text = strings.TrimLeft(strings.TrimPrefix(text, "-"), "0")
		switch {
		case text == "":
			return "0"
		case strings.HasPrefix(number.String(), "-"):
			return "-" + text
		default:
			return text
		}
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	exponent, _ := strconv.Atoi(scientific[strings.IndexByte(scientific, 'e')+1:])
	if exponent < -4 || exponent >= 16 {
		return scientific
	}
	fixed := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(fixed, ".") {
		fixed += ".0"
	}
	return fixed
}

// pyStringRepr は Python の repr(str) である。表示できない文字は \x・\u・\U で書く。
func pyStringRepr(value string) string {
	quote := byte('\'')
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		quote = '"'
	}
	var out strings.Builder
	out.WriteByte(quote)
	for _, char := range value {
		switch {
		case char == rune(quote) || char == '\\':
			out.WriteByte('\\')
			out.WriteRune(char)
		case char == '\n':
			out.WriteString(`\n`)
		case char == '\r':
			out.WriteString(`\r`)
		case char == '\t':
			out.WriteString(`\t`)
		case unicode.IsPrint(char):
			out.WriteRune(char)
		case char < 0x100:
			out.WriteString(`\x` + hex(int(char), 2))
		case char < 0x10000:
			out.WriteString(`\u` + hex(int(char), 4))
		default:
			out.WriteString(`\U` + hex(int(char), 8))
		}
	}
	out.WriteByte(quote)
	return out.String()
}

func hex(value, width int) string {
	text := strconv.FormatInt(int64(value), 16)
	return strings.Repeat("0", width-len(text)) + text
}
