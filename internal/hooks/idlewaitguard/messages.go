package idlewaitguard

import "github.com/HappyOnigiri/hhx/internal/i18n"

// 理由文は 2 系統ある。
//
//	WAIT … sleep を含む（待機の意図が明らか）。待ち方そのものを示す。
//	NOOP … 無効果のコマンドだけ（ターン繋ぎ）。ターンを終えるか別の作業をするよう促す。
//
// どちらも「別のコマンドへ書き換えて再実行するな」を明示する。deny を迂回して他言語の sleep に化けると、
// 検出だけが消えて浪費は残る。hook は Claude Code と Codex の両方に登録するので、片方にしか無いツール名は書かない。
// 系統のラベル（WAIT / NOOP）は機械が読む判別子なので訳さない。
const (
	waitLabel = "WAIT"
	noopLabel = "NOOP"
)

const (
	idReason         = "idle-wait-guard.reason"
	idWaitDetail     = "idle-wait-guard.wait.detail"
	idWaitBackground = "idle-wait-guard.wait.background"
	idWaitForeground = "idle-wait-guard.wait.foreground"
	idNoopDetail     = "idle-wait-guard.noop.detail"
)

var messages = i18n.Register(i18n.Catalog{
	// 先頭に拒否したコマンドを JSON 文字列として出し、複数行でも境界を明確にする
	// （UI で理由が省略されても、先頭だけで拒否の対象を特定できる）。
	idReason: {
		EN: "{{.Label}}: rejected command: {{.Command}}\n{{.Detail}}",
		JA: "{{.Label}}: 拒否対象コマンド: {{.Command}}\n{{.Detail}}",
	},
	// WAIT: 時間潰しは待機にならないと伝え、通知で再開する・条件で待つ・報告して終える、の順に案内する。
	// 最後に、他言語の sleep などへの書き換えを禁じる。
	idWaitDetail: {
		EN: "A command that only passes time is not a way to wait. {{.Pause}}\n" +
			"If you are waiting for asynchronous work to finish, and your environment notifies you when it " +
			"completes, end the turn and resume from the notification. Only if you need the result before the " +
			"notification, use a tool that can wait until a condition holds, and make the condition the " +
			"appearance or content of an output file. If no such tool exists, report the situation to the user " +
			"and end the turn.\n" +
			"If there is independent work you can do while waiting, do it first.\n" +
			"This is not about how the command is written. Do not rewrite it as a different command " +
			"(such as another language's equivalent of sleep) to run the same wait again.",
		JA: "時間を潰すコマンドは待機になりません。{{.Pause}}\n" +
			"非同期処理の完了を待っているなら、完了が通知される実行環境ではターンを終えて通知で" +
			"再開してください。通知前に結果が必要な場合だけ、条件が成立するまで待機できる手段を使い、" +
			"出力ファイルの出現や内容を条件にしてください。その手段が無ければ、状況をユーザーに報告" +
			"してターンを終えてください。\n" +
			"待っているあいだに進められる独立した作業があるなら、先にそれを実行してください。\n" +
			"これは書き方の指摘ではありません。別のコマンド (他言語の sleep 相当など) へ書き換えて" +
			"同じ待機を再実行しないでください。",
	},
	// バックグラウンド実行は「即座に戻る」ことが問題の核心なので、WAIT のバックグラウンドのときだけ補足する。
	idWaitBackground: {
		EN: "A background run returns immediately, so it does not wait at all; all that remains is a timer " +
			"that sends a completion notice later. Every one you start adds a turn later.",
		JA: "バックグラウンド実行は即座に戻るので一切待てておらず、残るのは後で完了通知を出す" +
			"タイマーだけです。投げた本数だけ後でターンが増えます。",
	},
	// 前景の WAIT では、待っているあいだ何も進まないことを伝える。
	idWaitForeground: {
		EN: "While it waits, nothing else makes progress.",
		JA: "待っているあいだ、他には何も進みません。",
	},
	// NOOP: 空ループになることを伝え、ターンを終えるか実際に必要なコマンドを直接実行するよう促す。
	// 最後に、別の無効果コマンドへの書き換えを禁じる。
	idNoopDetail: {
		EN: "This command changes no state and prints only a known literal. It returns immediately to the same " +
			"situation, so using it to keep the turn going becomes an empty loop that repeats the same decision.\n" +
			"If you are waiting for something to finish, and your environment notifies you when it completes, end " +
			"the turn and resume from the notification. If no notification will arrive, report the situation to " +
			"the user and end the turn.\n" +
			"If there is other independent work you can do, do it. If you want to check that the shell works, run " +
			"the command that gets the information you actually need.\n" +
			"This is not about how the command is written. Do not rewrite it as another no-op command and run it again.",
		JA: "状態を変えず、出力も既知のリテラルだけのコマンドです。即座に返って同じ状況に" +
			"戻るため、ターンを繋ぐ目的で使うと同じ判断を繰り返す空ループになります。\n" +
			"何かの完了を待っているなら、完了が通知される実行環境ではターンを終えて通知で再開して" +
			"ください。通知が届かない環境では、状況をユーザーに報告してターンを終えてください。\n" +
			"他に進められる独立した作業があるなら、それを実行してください。シェルが動くかの確認が" +
			"目的なら、実際に必要な情報を取るコマンドを直接実行してください。\n" +
			"これは書き方の指摘ではありません。別の無効果コマンドへ書き換えて再実行しないでください。",
	},
})

// reason は理由文を組み立てる。commandJSON は拒否したコマンドを JSON 文字列にしたものである。
func reason(language i18n.Language, label string, background bool, commandJSON string) string {
	detail := messages.T(language, idNoopDetail)
	if label == waitLabel {
		pause := idWaitForeground
		if background {
			pause = idWaitBackground
		}
		detail = messages.Text(language, idWaitDetail, map[string]any{"Pause": messages.T(language, pause)})
	}
	return messages.Text(language, idReason, map[string]any{"Label": label, "Command": commandJSON, "Detail": detail})
}
