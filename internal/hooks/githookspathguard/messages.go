package githookspathguard

import "github.com/HappyOnigiri/hhx/internal/i18n"

const idReason = "git-hookspath-guard.reason"

var messages = i18n.Register(i18n.Catalog{
	// 書き換えを止めるだけでなく、正しい手段（git config）と置き場所（.git/hooks/）を示す。
	// 回避を思いとどまらせるのはこの文面だけなので、迂回せずにユーザーへ伝えることも書く。
	idReason: {
		EN: "Do not edit Git config files directly; change Git settings with `git config <key> <value>`. " +
			"Changing core.hooksPath, however, is forbidden: it breaks the global-to-local hook delegation chain. " +
			"Put repository-specific hooks directly in .git/hooks/. " +
			"If you still need the change, do not retry with a different command or tool to get around this block; " +
			"tell the user what you want to change and why, and ask for instructions.",
		JA: "Git 設定ファイルを直接編集せず、Git 設定は `git config <key> <value>` で" +
			"書き換えてください。ただし core.hooksPath の変更は禁止されています。" +
			"global→local hook 委譲チェーンが壊れます。リポジトリ固有の hook は .git/hooks/ に" +
			"直接配置してください。" +
			"それでも変更が必要なら、迂回する別コマンド・別ツールを試さず、変更したい内容と目的をユーザーに伝えて指示を仰いでください。",
	},
})
