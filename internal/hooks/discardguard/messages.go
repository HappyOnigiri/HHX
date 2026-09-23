package discardguard

import "strings"

// 理由文には「ガードが働く形への書き直し」だけを書く。cp 退避のような迂回路を案内すると、snapshot なしの破棄へ誘導することになる。
// Claude Code と Codex の両方に出るので、片方にしか無いツール名を書かない。

// unresolvedTemplate は、対象のリポジトリを静的に特定できないときの理由文である。{label} に発火したルールのラベルが入る。
const unresolvedTemplate = `❌ ブロック: {label}

理由: 未コミットの変更を破棄する操作ですが、cd や git -C でどのリポジトリが対象か
      静的に特定できず、復元用スナップショットを作れません。追えないのは、変数・glob・
      クォートを含むパス、フック実行時に存在しないパス、pushd / popd、sh -c 経由、
      GIT_DIR / --git-dir / --work-tree による差し替えです。

対応: 次のように書き直せば、スナップショットを作ってから通ります。
      1. cd / git -C のパスを変数ではなくリテラルで書く (cd は複数回あってよい)
      2. sh -c や pushd を使わず、破棄コマンドを単独の Bash 呼び出しにする
      実行せず文字列として書きたいだけなら、ファイルを編集するツールを使ってください。
      cp 退避や別コマンドでの迂回はスナップショットを作らないので代替になりません。
      書き直せない場合は、操作・対象・目的をユーザーに伝えて指示を仰いでください。`

// failedTemplate は、snapshot を作れなかったときの理由文である。
const failedTemplate = `❌ ブロック: {label}

理由: 未コミットの変更を破棄する操作ですが、復元用スナップショットの作成に失敗しました
      (git 管理下でない、git コマンドが失敗した、タイムアウトしたなど)。

対応: 書き直しでは通りません。index.lock の競合のような一時的な失敗が疑われる場合だけ
      同じコマンドを 1 度再実行し、それ以外は迂回せず、操作・対象・目的をユーザーに伝えて
      指示を仰いでください。作業ツリーが汚れて進められない場合も勝手に片付けないこと。`

func denyUnresolved(label string) string {
	return strings.Replace(unresolvedTemplate, "{label}", label, 1)
}

func denyFailed(label string) string {
	return strings.Replace(failedTemplate, "{label}", label, 1)
}
