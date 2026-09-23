package waitci

import "github.com/HappyOnigiri/hhx/internal/i18n"

// wait-ci の出力のうち、エージェントが読む地の文（結論の理由と進捗の本文）をカタログに置く。
// 接頭辞 `wait-ci:`、最終行 `wait-ci: exit=...`、check の結果の語・名前・URL・秒数の書式（`12s`）は契約なので訳さない。
const (
	idNoPR           = "wait-ci.no-pr"
	idNoPRRetried    = "wait-ci.no-pr.retried"
	idPRMissing      = "wait-ci.progress.pr-missing"
	idGHFailedCount  = "wait-ci.progress.gh-failed"
	idRetryLookup    = "wait-ci.progress.retry-lookup"
	idFetchFailed    = "wait-ci.progress.fetch-failed"
	idWaitHead       = "wait-ci.progress.wait-head"
	idHeadChanged    = "wait-ci.progress.head-changed"
	idCIUnknown      = "wait-ci.progress.ci-unknown"
	idCountUnread    = "wait-ci.gh.count-unreadable"
	idJSONUnread     = "wait-ci.gh.json-unreadable"
	idJSONNotObject  = "wait-ci.gh.json-not-object"
	idNoDetachedHead = "wait-ci.gh.no-detached-sha"
)

var messages = i18n.Register(i18n.Catalog{
	// commit を含む open PR が無い。cmd 側で「監視をスキップした」を続ける。
	idNoPR: {EN: "no open PR for commit {{.SHA}}", JA: "commit {{.SHA}} に open PR が無い"},
	// 反映遅れを待って引き直した秒数。idNoPR の直後に付ける。
	idNoPRRetried: {EN: " (retried for {{.Seconds}}s)", JA: " ({{.Seconds}}s 再試行した)"},
	// PR の特定を引き直す理由。
	idPRMissing:     {EN: "no PR found", JA: "PR が見つからない"},
	idGHFailedCount: {EN: "gh call failed (attempt {{.Count}})", JA: "gh の呼び出しに失敗 ({{.Count}} 回目)"},
	idRetryLookup: {
		EN: "{{.Elapsed}}s commit {{.SHA}}: {{.Reason}}; retrying",
		JA: "{{.Elapsed}}s commit {{.SHA}} の {{.Reason}}。再試行する",
	},
	// 監視中の進捗。
	idFetchFailed: {
		EN: "{{.Elapsed}}s gh call failed (attempt {{.Count}})",
		JA: "{{.Elapsed}}s gh の呼び出しに失敗 ({{.Count}} 回目)",
	},
	idWaitHead: {
		EN: "{{.Elapsed}}s waiting for head={{.Head}} to become {{.Target}}",
		JA: "{{.Elapsed}}s head={{.Head}} が {{.Target}} になるのを待つ",
	},
	idHeadChanged: {EN: "head changed {{.From}} -> {{.To}}", JA: "head が {{.From}} -> {{.To}} へ変わった"},
	idCIUnknown: {
		EN: "cannot tell whether the repo has CI ({{.Error}}); still waiting",
		JA: "CI の有無を判定できない ({{.Error}})。待ちを続ける",
	},
	// gh の出力を読めなかった理由。
	idCountUnread:   {EN: "cannot read the gh output as a count: {{.Output}}", JA: "gh の出力を件数として読めない: {{.Output}}"},
	idJSONUnread:    {EN: "cannot read the gh output as JSON: {{.Error}}", JA: "gh の出力を JSON として読めない: {{.Error}}"},
	idJSONNotObject: {EN: "cannot read the gh output as JSON: not an object", JA: "gh の出力を JSON として読めない: オブジェクトでない"},
	idNoDetachedHead: {
		EN: "cannot get the commit SHA of the detached HEAD",
		JA: "detached HEAD の commit SHA を取得できない",
	},
})
