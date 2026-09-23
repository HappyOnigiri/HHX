package agentslocalcontext

// warningPrefix は利用者への警告（systemMessage）の書き出しである。
const warningPrefix = "⚠️ AGENTS.local.md hook: "

// 警告の文面である。%s や %d には理由や件数が入る。
const (
	warningMore            = "ほか %d 件"
	warningNoCwd           = "cwd がないため対象パスを判定できない"
	warningInvalidInput    = "hook入力を解析できない: %s"
	warningNotObject       = "hook入力がJSON objectではない"
	warningContextLimit    = "context上限 %d bytes のため未注入: %s"
	warningNoSessionClaim  = "session_id がないため重複抑止を無効化"
	warningStateClaim      = "状態ファイルを利用できないため重複抑止を無効化: %s"
	warningNoSessionStored = "session_id がないため読込済みルールを復元できない"
	warningStateStored     = "状態ファイルから読込済みルールを復元できない: %s"
	warningStateReplace    = "compact後の状態ファイル更新に失敗: %s"
	warningReadRule        = "ルールを読み込めない (%s): %s"
	warningUnexpected      = "予期しないエラー (%s): %s"
)
