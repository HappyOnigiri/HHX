package install

import (
	"fmt"
	"path/filepath"
	"strings"
)

// executableName は hhx のエントリを見分けるための先頭トークンの basename である。
const executableName = "hhx"

// HookCommand は hook name を登録するコマンド文字列を返す。形は `<binary> hook <name>` に固定する。
// binary は絶対パスで、shell が特別に扱う文字を含むときはダブルクオートで囲む。
// ダブルクオートの中でも展開・escape される文字（ドル記号、バッククオート、ダブルクオート、バックスラッシュ）と改行を含むパスは、書かずに拒否する。
func HookCommand(binary, name string) (string, error) {
	if !filepath.IsAbs(binary) {
		return "", fmt.Errorf("the hhx executable path must be absolute: %s", binary)
	}
	if strings.ContainsAny(binary, "$`\"\\\n\r") {
		return "", fmt.Errorf("the hhx executable path cannot be written as a hook command: %s", binary)
	}
	quoted := binary
	if strings.IndexFunc(binary, needsQuote) >= 0 {
		quoted = `"` + binary + `"`
	}
	command := quoted + " hook " + name
	fields, ok := splitHookCommand(command)
	if !ok || len(fields) != 3 || fields[0] != binary || fields[1] != "hook" || fields[2] != name {
		return "", fmt.Errorf("the hook command for %q cannot be written safely: %s", name, command)
	}
	return command, nil
}

func needsQuote(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return false
	default:
		return !strings.ContainsRune("/._-+@%,:=", char)
	}
}

// isHHXHookCommand は command が hhx のエントリかを返す。
// 先頭トークンの basename が hhx で、2 番目が hook のものだけを hhx のものとみなす。
// 実体が消えた古い記録も uninstall で回収できるよう、実行ファイルの存在は確かめない。
func isHHXHookCommand(command string) bool {
	fields, ok := splitHookCommand(command)
	return ok && len(fields) >= 2 && filepath.Base(fields[0]) == executableName && fields[1] == "hook"
}

// splitHookCommand は登録済みのコマンド文字列を、shell と同じ quote の規則でトークンに分ける。
// 展開・置換・演算子を含むものは hhx が書く形ではないので、分類せずに false を返す。
// これはエントリの持ち主を見分けるためだけのもので、hook が Bash のコマンドを解析する処理とは共有しない。
func splitHookCommand(command string) ([]string, bool) {
	var fields []string
	var field strings.Builder
	var quote byte
	escaped := false
	started := false
	for i := 0; i < len(command); i++ {
		char := command[i]
		// 改行は shell ではコマンドの区切りなので、quote の中でも受け付けない。
		if char == '\n' || char == '\r' {
			return nil, false
		}
		if escaped {
			field.WriteByte(char)
			escaped = false
			started = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
				continue
			}
			if quote == '"' && char == '`' {
				return nil, false
			}
			if quote == '"' && char == '\\' {
				// ダブルクオートの中で backslash が escape できるのは $、`、"、\ だけである。
				if i+1 >= len(command) || !strings.ContainsRune("$`\"\\", rune(command[i+1])) {
					return nil, false
				}
				escaped = true
				continue
			}
			field.WriteByte(char)
			started = true
			continue
		}
		switch {
		case char == '\\':
			escaped = true
			started = true
		case char == '\'' || char == '"':
			quote = char
			started = true
		// shell の既定の区切り（IFS）に合わせ、ASCII の空白とタブだけで区切る。バイト単位で見るため、
		// unicode.IsSpace では UTF-8 の継続バイト（0x85・0xA0）を空白と取り違える。
		case char == ' ' || char == '\t':
			if started {
				fields = append(fields, field.String())
				field.Reset()
				started = false
			}
		case strings.ContainsRune("|;&><`", rune(char)):
			return nil, false
		default:
			field.WriteByte(char)
			started = true
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	if started {
		fields = append(fields, field.String())
	}
	return fields, true
}
