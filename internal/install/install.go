// Package install は Claude の settings.json と Codex の hooks.json に、hhx のエントリだけを書き込み・削除する。
//
// wx や利用者の hook は、同じグループにも入れないし消しもしない。
// JSON の読み書き（順序と字下げの保持）と書き込みの安全策は wx の internal/hookconfig から移したものである。
package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HappyOnigiri/hhx/internal/hookrt"
)

// maxHookConfigSize は読み書きする設定ファイルの上限である。
const maxHookConfigSize = 4 << 20

// Options は install / uninstall の環境である。テストは一時的な HOME を渡す。
type Options struct {
	Home string
	// Binary は登録するコマンド文字列に書く hhx の絶対パスである。Uninstall では使わない。
	Binary string
	// BackupDir は書き換える前の設定ファイルを 1 世代だけ控える場所である。空なら控えない。
	BackupDir string
}

// Result は 1 つの CLI への適用結果である。
// Resolved は symlink を辿った実体で、dotfile リポジトリ側での commit を案内するために返す。
type Result struct {
	Agent    hookrt.Agent
	Path     string
	Resolved string
	Backup   string
	Changed  bool
}

// TargetPath は agent の hook 設定の読み書き先を、ファイルが存在しなくても返す。
// Claude は ~/.claude/settings.local.json があるとそちらを優先して読むため、書き込み先もそちらに揃える。
func TargetPath(home string, agent hookrt.Agent) (string, error) {
	switch agent {
	case hookrt.Codex:
		return filepath.Join(home, ".codex", "hooks.json"), nil
	case hookrt.Claude:
		local := filepath.Join(home, ".claude", "settings.local.json")
		info, err := os.Stat(local)
		switch {
		case err == nil && info.Mode().IsRegular():
			return local, nil
		case err == nil:
			return "", fmt.Errorf("%s is not a regular file", local)
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	default:
		return "", fmt.Errorf("unsupported agent %q", agent)
	}
}

// Install は definitions のうち agent に登録するものを、hhx 専用のグループとして書く。
// 冪等であり、書き戻す内容が既存の内容と同じならファイルに触らない。
func Install(options Options, agent hookrt.Agent, definitions []hookrt.Definition) (Result, error) {
	groups, err := desiredGroups(agent, options.Binary, definitions)
	if err != nil {
		return Result{}, err
	}
	return apply(options, agent, groups)
}

// Uninstall は agent の設定ファイルから hhx のエントリだけを取り除き、それで空になったグループとイベントを畳む。
func Uninstall(options Options, agent hookrt.Agent) (Result, error) {
	return apply(options, agent, nil)
}

func apply(options Options, agent hookrt.Agent, groups []managedGroup) (Result, error) {
	path, err := TargetPath(options.Home, agent)
	if err != nil {
		return Result{}, err
	}
	result := Result{Agent: agent, Path: path, Resolved: path}
	original, resolved, existed, err := readWritableTarget(path)
	if err != nil {
		return Result{}, err
	}
	result.Resolved = resolved
	if !existed && len(groups) == 0 {
		// 消すものが無いのに空の設定ファイルを作らない。
		return result, nil
	}
	document := &jsonNode{kind: jsonObject}
	if existed {
		document, err = decodeDocument(original)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", path, err)
		}
		if document.kind != jsonObject {
			return Result{}, fmt.Errorf("%s: the top level is not a JSON object", path)
		}
	}
	indent := documentIndentOf(original)
	// 描画は 1 行に書かれた配列なども展開するため、元のバイト列ではなく編集前の文書の描画と比べる。
	// そうしないと、hhx のエントリに変化が無くても利用者の書式を書き換えてしまう。
	before := renderDocument(document, indent)
	if err := applyGroups(document, groups); err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	rendered := renderDocument(document, indent)
	if existed && bytes.Equal(rendered, before) {
		return result, nil
	}
	if existed && options.BackupDir != "" {
		backup, err := backupHookConfig(options.BackupDir, agent, original)
		if err != nil {
			return Result{}, err
		}
		result.Backup = backup
	}
	if err := writeHookConfig(resolved, existed, rendered); err != nil {
		return Result{}, err
	}
	result.Changed = true
	return result, nil
}

// readWritableTarget は書き込み先の実体を確かめ、既存の内容を返す。
// symlink は辿って実体を書く。リンクの上に rename すると dotfile リポジトリとの接続が黙って切れるためである。
func readWritableTarget(path string) (data []byte, resolved string, existed bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	resolved = path
	if info.Mode()&os.ModeSymlink != 0 {
		if resolved, err = filepath.EvalSymlinks(path); err != nil {
			return nil, "", false, fmt.Errorf("%s: %w", path, err)
		}
	}
	target, err := os.Stat(resolved)
	if err != nil {
		return nil, "", false, err
	}
	if !target.Mode().IsRegular() {
		return nil, "", false, fmt.Errorf("%s is not a regular file", resolved)
	}
	if !ownedByCurrentUser(target) {
		return nil, "", false, fmt.Errorf("%s is not owned by the current user", resolved)
	}
	data, err = os.ReadFile(resolved)
	if err != nil {
		return nil, "", false, err
	}
	switch {
	case len(data) == 0:
		// CLI は 0 byte の設定を読めない。{} とみなして書くと、利用者の壊れたファイルを黙って上書きする。
		return nil, "", false, fmt.Errorf("%s is empty; remove it or restore valid JSON before installing hooks", resolved)
	case len(data) > maxHookConfigSize:
		return nil, "", false, fmt.Errorf("%s is larger than the 4MiB limit", resolved)
	}
	return data, resolved, true, nil
}

// writeHookConfig は同じディレクトリの一時ファイルへ書いてから rename する。
// 既存ファイルの permission を引き継ぎ、新規は 0o600 で作る。
func writeHookConfig(resolved string, existed bool, data []byte) error {
	mode := os.FileMode(0o600)
	if existed {
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(resolved), 0o700); err != nil {
		return err
	}
	directory := filepath.Dir(resolved)
	tmp, err := os.CreateTemp(directory, ".hhx-hooks-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, resolved); err != nil {
		return err
	}
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	err = handle.Sync()
	_ = handle.Close()
	return err
}

// backupHookConfig は書き換える前の内容を CLI ごとに 1 世代だけ控える。
// ~/.claude 直下に settings*.json に一致するファイルを増やさないため、hhx の状態ディレクトリへ置く。
func backupHookConfig(directory string, agent hookrt.Agent, original []byte) (string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, string(agent)+"-hooks.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
