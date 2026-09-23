package pushcicontext

// waitCommand は push・PR 作成の後に待つコマンドである。hhx に内蔵した wait-ci に固定する。
const waitCommand = "hhx wait-ci --progress"

// waitLabel は Codex 向けの案内の「〜中は」に入る語である。
const waitLabel = "hhx wait-ci "

// dispatchLabel は workflow dispatch の後の待機を指す語である。
const dispatchLabel = "workflow run の完了待機"

// orderGuidance は待機を残作業の後に回させる。CI はモデルの待機と無関係に進むので、先に他の作業を片付けても
// 完了は遅れない。逆に先に待つと、PR 本文の更新や別 PR の改修が待ち時間ぶん止まる。
const orderGuidance = "ただし待機はこのターンの最後に回す。ユーザーの依頼・このコンテキストに入った他の指摘" +
	"（PR 本文の更新、別 PR の改修、レビュー指摘の反映など）で未着手のものがあれば先に片付け、" +
	"残りが無くなってから待機を始める。CI は待機の有無に関わらず進むため、先に作業しても完了は遅れない。" +
	"待機を理由に他の作業を後回しにしたり、待機前に報告してターンを終えたりしない。\n"

const guidance = "`" + waitCommand + "` を引数なしで実行し、このターンで終了まで待つ。pending / settling は未完了。" +
	"パイプで終了コードを失わず、exit 0 は成功とスキップを結論行で区別する。失敗は原因を直して再pushし、CI完了まで追跡する。\n" +
	orderGuidance

const dispatchGuidance = "workflow dispatch の受付が成功した。返された URL の run ID を使い、" +
	"`gh run watch <run-id> --exit-status` をこのターンで終了まで待つ。run ID がまだ取得できない" +
	"場合は、下の登録確認ループと watch を同一の長時間待機ツール呼び出し内で実行し、" +
	"30秒ごとにモデルへ制御を戻さない。\n" +
	orderGuidance

// codexGuidance は Codex の Code Mode での待ち方である。%[1]s は waitLabel か dispatchLabel、
// %[2]s は待つコマンドの JSON 文字列である。
const codexGuidance = "%[1]s中は定期報告と60秒を超える待機回避の例外とし、開始前と終了・判断が必要な時だけ報告する。" +
	"Code Mode では次の形で待つ。\n" +
	"```javascript\n" +
	"// @exec: {\"yield_time_ms\":3600000}\n" +
	"let r = await tools.exec_command({cmd:%[2]s,yield_time_ms:1000});\n" +
	"let output = r.output ?? \"\";\n" +
	"while (r.session_id != null) {\n" +
	"  r = await tools.write_stdin({session_id:r.session_id,chars:\"\",yield_time_ms:300000});\n" +
	"  output += r.output ?? \"\";\n" +
	"}\n" +
	"if (typeof r.exit_code !== \"number\") throw new Error(\"終了コード未確認\");\n" +
	"text({exit_code:r.exit_code,output});\n" +
	"```\n" +
	"外側が cell_id を返したら同じIDを functions.wait（yield_time_ms:3600000）で待つ。再起動・二重ポーリングはしない。" +
	"Code Mode がなければ空入力の write_stdin を利用可能な最大待機時間で終了まで追跡する。\n"

// bashGuidance は Claude Code の Bash での待ち方である。%s は待つコマンドである。
const bashGuidance = "Bash は次を `timeout:600000` で実行し、バックグラウンドへ移った場合も出力ファイルで終了まで追跡する: `%s`\n"

// eventContext は push・PR 作成の案内の書き出しである。PreToolUse は実行前なので「成功したら」と書く。
var eventContext = map[string]string{
	"PostToolUse": "push / PR 作成が成功した。",
	"PreToolUse":  "この push / PR 作成が成功したら、",
}

const (
	dispatchSucceeded = "workflow dispatch が成功した。"
	dispatchBefore    = "この workflow dispatch が成功したら、"
)

// 登録確認ループの中で使う文面である。
const (
	multipleCandidates  = "workflow run の候補が複数あり特定できない"
	registrationTimeout = "workflow run の登録待ちが90分でタイムアウトした"
)
