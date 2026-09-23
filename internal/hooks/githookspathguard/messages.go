package githookspathguard

// denyReason は拒否の理由文である。書き換えを止めるだけでなく、正しい置き場所（.git/hooks/）を示す。
const denyReason = "Git 設定ファイルを直接編集せず、Git 設定は `git config <key> <value>` で" +
	"書き換えてください。ただし core.hooksPath の変更は禁止されています。" +
	"global→local hook 委譲チェーンが壊れます。リポジトリ固有の hook は .git/hooks/ に" +
	"直接配置してください。"
