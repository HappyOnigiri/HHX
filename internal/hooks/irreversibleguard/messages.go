package irreversibleguard

// 理由文は「何を・なぜ・どうする」の 3 点を返す。回避を思いとどまらせるのはこの文面だけなので、
// 「迂回する別コマンド・別ツールを試さない」ことを対応に書く。
const (
	how = "迂回する別コマンド・別ツールを試さず、実行したい操作・対象・目的をユーザーに伝えて" +
		"指示を仰いでください。承認された場合も、原則ユーザー自身が実行します。"

	whyCred = "資格情報の失効・削除は元に戻せません。その資格情報にどのシステムが依存しているかは" +
		"ユーザーにしか分からないため、実行の可否はユーザーが判断します。"
	whyRemote  = "リモートリソースの恒久削除で、履歴やゴミ箱からの復元ができません。"
	whyPublish = "外部への公開・取り下げは即座に世界へ伝播し、取り消せません" +
		" (レジストリによっては同名での再公開も恒久にできません)。"
	whyGit     = "到達不能オブジェクトの物理削除で、reflog や fsck による復元の余地ごと失われます。"
	whyMachine = "マシン上のデータ・バックアップの不可逆な消去です。"
	whySecret  = "Git 管理外の秘密情報・鍵は、誤った作成・編集・削除による影響をエージェント側で" +
		"判断できません。秘密ファイルへの直接書き込みは、新規作成も含めユーザーが行います。"
	whyGuard = "エージェントの安全ガード (hooks・設定ファイル) の削除・移動です。" +
		"ガードの構成変更はユーザーが行います。"
)

// 発火したルールの対象の表記。理由文の「❌ ブロック:」の後ろに出し、テストはこれでどのルールが発火したかを見る。
const (
	targetHTTPDelete = "HTTP クライアントによるクラウド API の DELETE"
	targetDB         = "データベースの破壊操作 (DROP/TRUNCATE)"

	targetSecretDelete   = "秘密ファイルの削除・破壊"
	targetSecretInPlace  = "秘密ファイルの in-place 書き換え"
	targetSecretMove     = "秘密ファイルの移動"
	targetSecretRedirect = "秘密ファイルの上書きリダイレクト"
	targetSecretTee      = "秘密ファイルへの tee 書き込み"
	targetSecretEdit     = "秘密ファイルへの直接編集"
	targetSecretPatch    = "秘密ファイルへのパッチ適用"
	targetGuard          = "エージェントガードの削除・移動"
)

func reason(target, why string) string {
	return "❌ ブロック: " + target + "\n\n理由: " + why + "\n\n対応: " + how
}

// withToken は対象の表記に、発火したトークン（パス）を括弧で添える。
func withToken(target, token string) string {
	return target + " (" + token + ")"
}
