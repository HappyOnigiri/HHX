// Package i18n は hhx が出す文面（deny の理由文・注入するコンテキスト・CLI の出力）を言語ごとに引く。
//
// 文面は ID ごとに英語と日本語を持つカタログに置き、各パッケージの messages.go が自分の表を Register する。
// 機械が読む部分（JSON のキー、注入のタグと項目名、`wait-ci:` の接頭辞と最終行、check の結果の語）と、
// 外部の文字列（PR のタイトル・パス・利用者が設定で書いた文）はカタログを通さない。
//
// hook は全ツール呼び出しで走るので、起動のたびにカタログ全体を組み立て直さない。
// 表は package 変数の map のまま引き、差し込みのある文だけを使うときに text/template で展開する。
package i18n

import (
	"fmt"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// Language は hhx が表示に対応する言語である。
type Language string

const (
	English  Language = "en"
	Japanese Language = "ja"
)

// Parse は設定の値を言語として読む。空は英語（既定）とし、en・ja 以外はエラーにする。
// エラーにするのは install の検査だけで、表示の解決は Normalize で英語へ倒す。
func Parse(value string) (Language, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(English):
		return English, nil
	case string(Japanese):
		return Japanese, nil
	default:
		return English, fmt.Errorf("language must be en or ja, got %q", value)
	}
}

// Normalize は設定の値を表示に使う言語へ解決する。未指定・不正な値は英語にする。
func Normalize(value string) Language {
	language, err := Parse(value)
	if err != nil {
		return English
	}
	return language
}

// Entry は 1 ID の英語と日本語の文面である。差し込みは `{{.Name}}` の形だけを使う。
type Entry struct {
	EN string
	JA string
}

// Catalog は 1 パッケージ分の ID と文面の表である。ID にはパッケージの利用者名（hook 名など）を前置きする。
type Catalog map[string]Entry

// catalogs は Register された表である。検査（Validate）が全体を見るために集める。
var catalogs []Catalog

// Register は表を登録して返す。各パッケージの messages.go が package 変数の初期化で呼ぶ。
func Register(catalog Catalog) Catalog {
	catalogs = append(catalogs, catalog)
	return catalog
}

// Text は id の文面を language で返し、data を差し込む。
// 未知の ID は ID そのものを返す（欠落が見て分かるように）。訳が空か展開に失敗したら英語に戻す。
func (c Catalog) Text(language Language, id string, data map[string]any) string {
	entry, ok := c[id]
	if !ok {
		return id
	}
	text := entry.EN
	if language == Japanese && entry.JA != "" {
		text = entry.JA
	}
	if rendered, err := render(id, text, data); err == nil {
		return rendered
	}
	if rendered, err := render(id, entry.EN, data); err == nil {
		return rendered
	}
	return entry.EN
}

// T は差し込みの無い文面を返す。
func (c Catalog) T(language Language, id string) string {
	return c.Text(language, id, nil)
}

func render(id, text string, data map[string]any) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	parsed, err := template.New(id).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := parsed.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

// Text は登録された全表から id を探して文面を返す。パッケージをまたいで文面を確かめるテストのためにある。
// 実行時は各パッケージの Catalog.Text を使う（全表を探さない）。
func Text(language Language, id string, data map[string]any) string {
	for _, catalog := range catalogs {
		if _, ok := catalog[id]; ok {
			return catalog.Text(language, id, data)
		}
	}
	return id
}

// All は登録された全表を 1 つにまとめて返す。ID の重複はエラーにする。
func All() (map[string]Entry, error) {
	all := map[string]Entry{}
	for _, catalog := range catalogs {
		for id, entry := range catalog {
			if _, duplicate := all[id]; duplicate {
				return nil, fmt.Errorf("message %q is registered twice", id)
			}
			all[id] = entry
		}
	}
	return all, nil
}

// Validate は登録された全表を検査する。
//   - ID が重複していない
//   - 英語と日本語の両方が空でない
//   - template として読め、英語と日本語で差し込みの名前の集合が同じ
//   - 差し込みは `{{.Name}}` だけ（条件分岐などの制御を文面に持たせない）
func Validate() error {
	all, err := All()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := all[id]
		if strings.TrimSpace(entry.EN) == "" || strings.TrimSpace(entry.JA) == "" {
			return fmt.Errorf("message %q must have non-empty en and ja text", id)
		}
		english, err := fields(id, entry.EN)
		if err != nil {
			return fmt.Errorf("message %q (en): %w", id, err)
		}
		japanese, err := fields(id, entry.JA)
		if err != nil {
			return fmt.Errorf("message %q (ja): %w", id, err)
		}
		if strings.Join(english, ",") != strings.Join(japanese, ",") {
			return fmt.Errorf("message %q uses %v in en but %v in ja", id, english, japanese)
		}
	}
	return nil
}

// fields は text の差し込みの名前を昇順・重複なしで返す。`{{.Name}}` 以外の action はエラーにする。
func fields(id, text string) ([]string, error) {
	parsed, err := template.New(id).Parse(text)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, node := range parsed.Root.Nodes {
		switch node := node.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			name, ok := fieldName(node)
			if !ok {
				return nil, fmt.Errorf("only {{.Name}} is allowed, got %s", node)
			}
			seen[name] = true
		default:
			return nil, fmt.Errorf("only {{.Name}} is allowed, got %s", node)
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func fieldName(node *parse.ActionNode) (string, bool) {
	if node.Pipe == nil || len(node.Pipe.Decl) > 0 || len(node.Pipe.Cmds) != 1 || len(node.Pipe.Cmds[0].Args) != 1 {
		return "", false
	}
	field, ok := node.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
	if !ok || len(field.Ident) != 1 {
		return "", false
	}
	return field.Ident[0], true
}
