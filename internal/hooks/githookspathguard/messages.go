package githookspathguard

// denyReason は拒否の理由文である。書き換えを止めるだけでなく、正しい置き場所（.git/hooks/）を示す。
// 回避を思いとどまらせるのはこの文面だけなので、迂回せずにユーザーへ伝えることも書く。
const denyReason = "Git 設定ファイルを直接編集せず、Git 設定は `git config <key> <value>` で" +
	"書き換えてください。ただし core.hooksPath の変更は禁止されています。" +
	"global→local hook 委譲チェーンが壊れます。リポジトリ固有の hook は .git/hooks/ に" +
	"直接配置してください。" +
	"それでも変更が必要なら、迂回する別コマンド・別ツールを試さず、変更したい内容と目的をユーザーに伝えて指示を仰いでください。"
