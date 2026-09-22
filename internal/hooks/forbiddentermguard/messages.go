package forbiddentermguard

import (
	"strconv"
	"strings"
)

// 理由文は「該当箇所・なぜ・どうする」を返す。回避を思いとどまらせるのはこの文面だけなので、
// 語リストの編集や別コマンドでの迂回をしないことを対応に書く。
const (
	why = "このリポジトリの禁止語リストに載っている語が本文に含まれています。" +
		"他リポジトリやサービスの名称を公開先へ持ち出さないための設定です。"
	how = "該当箇所から語を取り除いて実行し直してください。語リストの編集・削除や、" +
		"別コマンドでの迂回はしないでください。取り除くと意味が通らなくなる場合は、" +
		"何をどう書きたいかをユーザーに伝えて判断を仰いでください。"
)

// reason は理由文を組み立てる。該当箇所は最大 maxReported 件まで出し、残りは件数だけを示す。
func reason(findings []string, termsPath string) string {
	shown := findings
	if len(shown) > maxReported {
		shown = shown[:maxReported]
	}
	detail := strings.Join(shown, "\n")
	if remaining := len(findings) - maxReported; remaining > 0 {
		detail += "\n  … 他 " + strconv.Itoa(remaining) + " 件"
	}
	return "❌ ブロック: 禁止語を含む本文の送信\n\n該当箇所:\n" + detail + "\n\n" +
		"理由: " + why + " (語リスト: " + termsPath + ")\n\n対応: " + how
}
