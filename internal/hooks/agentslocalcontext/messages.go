package agentslocalcontext

import "github.com/HappyOnigiri/hhx/internal/i18n"

// warningPrefix は利用者への警告（systemMessage）の書き出しである。hook の名前なので訳さない。
const warningPrefix = "⚠️ AGENTS.local.md hook: "

// 警告の文面である。利用者に、注入できなかった理由や重複抑止が効かない理由を伝える。
const (
	idMore            = "agents-local-context.warning.more"
	idNoCwd           = "agents-local-context.warning.no-cwd"
	idInvalidInput    = "agents-local-context.warning.invalid-input"
	idNotObject       = "agents-local-context.warning.not-object"
	idContextLimit    = "agents-local-context.warning.context-limit"
	idNoSessionClaim  = "agents-local-context.warning.no-session-claim"
	idStateClaim      = "agents-local-context.warning.state-claim"
	idNoSessionStored = "agents-local-context.warning.no-session-stored"
	idStateStored     = "agents-local-context.warning.state-stored"
	idStateReplace    = "agents-local-context.warning.state-replace"
	idReadRule        = "agents-local-context.warning.read-rule"
	idReadRuleNotUTF8 = "agents-local-context.warning.read-rule-not-utf8"
	idUnexpected      = "agents-local-context.warning.unexpected"
)

var messages = i18n.Register(i18n.Catalog{
	// 4 件目以降を件数だけにした部分。
	idMore:  {EN: "{{.Count}} more", JA: "ほか {{.Count}} 件"},
	idNoCwd: {EN: "no cwd, so the target paths cannot be determined", JA: "cwd がないため対象パスを判定できない"},
	idInvalidInput: {
		EN: "cannot parse the hook input: {{.Error}}",
		JA: "hook入力を解析できない: {{.Error}}",
	},
	idNotObject: {EN: "the hook input is not a JSON object", JA: "hook入力がJSON objectではない"},
	idContextLimit: {
		EN: "not injected because of the {{.Limit}}-byte context limit: {{.Path}}",
		JA: "context上限 {{.Limit}} bytes のため未注入: {{.Path}}",
	},
	idNoSessionClaim: {
		EN: "no session_id, so duplicate suppression is disabled",
		JA: "session_id がないため重複抑止を無効化",
	},
	idStateClaim: {
		EN: "the state file is unavailable, so duplicate suppression is disabled: {{.Error}}",
		JA: "状態ファイルを利用できないため重複抑止を無効化: {{.Error}}",
	},
	idNoSessionStored: {
		EN: "no session_id, so the rules already loaded cannot be restored",
		JA: "session_id がないため読込済みルールを復元できない",
	},
	idStateStored: {
		EN: "cannot restore the rules already loaded from the state file: {{.Error}}",
		JA: "状態ファイルから読込済みルールを復元できない: {{.Error}}",
	},
	idStateReplace: {
		EN: "failed to update the state file after compact: {{.Error}}",
		JA: "compact後の状態ファイル更新に失敗: {{.Error}}",
	},
	idReadRule: {
		EN: "cannot read the rule ({{.Path}}): {{.Error}}",
		JA: "ルールを読み込めない ({{.Path}}): {{.Error}}",
	},
	idReadRuleNotUTF8: {
		EN: "cannot read the rule ({{.Path}}): not valid UTF-8",
		JA: "ルールを読み込めない ({{.Path}}): UTF-8 として読めない",
	},
	idUnexpected: {
		EN: "unexpected error ({{.Kind}}): {{.Error}}",
		JA: "予期しないエラー ({{.Kind}}): {{.Error}}",
	},
})

// texts は警告の文面を表示言語で組み立てる。判定の途中で作る警告を、出力の直前でなく作った場所で言語に合わせるために持ち回る。
type texts struct {
	language i18n.Language
}

func (t texts) warn(id string, data map[string]any) string {
	return messages.Text(t.language, id, data)
}
