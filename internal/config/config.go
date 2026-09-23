// Package config は hhx の設定ファイル（既定は ~/.config/hhx/config.yaml）を読む。
//
// 定義するのは表示言語（language）と、hook ごとの enabled と、hook 固有の設定を置く場所だけである。
// hook 固有の項目は各 hook が Decode で自分の型へ読み込み、ここでは中身を解釈しない。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/HappyOnigiri/hhx/internal/i18n"
)

// PathEnv は設定ファイルの場所を差し替える環境変数である。テストが一時的な設定を渡すために使う。
const PathEnv = "HHX_CONFIG"

// maxConfigSize は読み込む設定ファイルの上限である。hook は全 Bash 呼び出しで走るため、巨大なファイルで遅くしない。
const maxConfigSize = 1 << 20

// Config は読み込んだ設定である。ゼロ値は「設定ファイルが無い」状態と同じで、すべての hook が既定値で動く。
type Config struct {
	// Language は設定に書かれた表示言語の値そのものである。解釈は DisplayLanguage と install の検査が行う。
	// 不正な値でも読み込みはエラーにしない。hook は英語へ倒して動き続け、install が報告する。
	Language string
	Hooks    map[string]Hook
}

// Hook は hook 1 本分の設定である。
// Enabled が nil なら hook の既定に従う。node は hook 固有の項目を含む mapping 全体を保つ。
type Hook struct {
	Enabled *bool
	node    *yaml.Node
}

type document struct {
	Language string               `yaml:"language"`
	Hooks    map[string]yaml.Node `yaml:"hooks"`
}

// DefaultPath は設定ファイルの場所を返す。PathEnv が空でなければそれを優先する。
func DefaultPath() (string, error) {
	if path := os.Getenv(PathEnv); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "hhx", "config.yaml"), nil
}

// Load は path の設定を読む。ファイルが無ければ空の設定を返し、エラーにしない。
// 未知のトップレベルキー・型の違い・複数文書はエラーにする。
// hook の実行時はエラーを既定値へ退避させ（fail-open）、install はエラーを報告して止まる。この使い分けは呼び出し側が行う。
func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxConfigSize {
		return nil, fmt.Errorf("%s: larger than the 1MiB limit", path)
	}
	config, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return config, nil
}

func parse(data []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var raw document
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			// 空のファイルやコメントだけのファイルは、ファイルが無いのと同じに扱う。
			return &Config{}, nil
		}
		return nil, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents")
		}
		return nil, err
	}
	config := &Config{Language: raw.Language, Hooks: map[string]Hook{}}
	for name, node := range raw.Hooks {
		hook, err := parseHook(name, node)
		if err != nil {
			return nil, err
		}
		config.Hooks[name] = hook
	}
	return config, nil
}

func parseHook(name string, node yaml.Node) (Hook, error) {
	// `name:` のように値を書かない行は null になる。空の mapping と同じく、既定値で動かす。
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return Hook{}, nil
	}
	if node.Kind != yaml.MappingNode {
		return Hook{}, fmt.Errorf("hooks.%s: must be a mapping", name)
	}
	hook := Hook{node: &node}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value != "enabled" {
			continue
		}
		// null を bool へ Decode すると false になり、値の書き忘れで hook が黙って止まる。true / false だけを受け付ける。
		value := node.Content[index+1]
		var enabled bool
		if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
			return Hook{}, fmt.Errorf("hooks.%s.enabled: must be true or false", name)
		}
		if err := value.Decode(&enabled); err != nil {
			return Hook{}, fmt.Errorf("hooks.%s.enabled: must be true or false", name)
		}
		hook.Enabled = &enabled
	}
	return hook, nil
}

// DisplayLanguage は表示に使う言語を返す。未指定・不正な値は英語にする。
func (c *Config) DisplayLanguage() i18n.Language {
	if c == nil {
		return i18n.English
	}
	return i18n.Normalize(c.Language)
}

// Enabled は hook が有効かを返す。設定に enabled が無ければ fallback（hook の既定）を返す。
func (c *Config) Enabled(name string, fallback bool) bool {
	if c == nil {
		return fallback
	}
	hook, ok := c.Hooks[name]
	if !ok || hook.Enabled == nil {
		return fallback
	}
	return *hook.Enabled
}

// Decode は hook の設定 mapping を value へ読み込む。設定が無ければ value を変えずに nil を返す。
// enabled も同じ mapping にあるため、value の型は enabled を受け取るか、未知キーを許す必要がある。
func (c *Config) Decode(name string, value any) error {
	if c == nil {
		return nil
	}
	hook, ok := c.Hooks[name]
	if !ok || hook.node == nil {
		return nil
	}
	return hook.node.Decode(value)
}

// UnknownHooks は known が偽を返す hook 名を昇順で返す。install が綴りの誤りを報告するために使う。
func (c *Config) UnknownHooks(known func(string) bool) []string {
	if c == nil {
		return nil
	}
	var unknown []string
	for name := range c.Hooks {
		if !known(name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}
