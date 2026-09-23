package prcontext

import "github.com/HappyOnigiri/hhx/internal/i18n"

// 注入する文面のうち、地の文だけをカタログに置く。タグ（<pr-context>）・項目名（note:・local:・head= など）・
// PR の状態の語（OPEN・MERGED など）は機械が読む部分なので訳さない。
const (
	idHeader           = "pr-context.header"
	idNote             = "pr-context.note"
	idNoteMerged       = "pr-context.note.merged"
	idLocalHeadMatches = "pr-context.local.head-matches"
	idLocalAvailable   = "pr-context.local.available"
	idLocalMissing     = "pr-context.local.missing"
	idLocalNoClone     = "pr-context.local.no-clone"
)

const footer = "</pr-context>"

var messages = i18n.Register(i18n.Catalog{
	// 注入の見出し。タグ名で用途は自明なので、本文を含まないことだけを添える。
	idHeader: {
		EN: "<pr-context> PRs referenced in the prompt (metadata only, no body)",
		JA: "<pr-context> プロンプトで参照された PR（メタデータのみ、本文なし）",
	},
	// ローカルの作業ツリーを PR の中身だと思い込ませないための注記。毎回付ける。
	// object を解決できないことを「コードが無い」根拠にさせない。
	idNote: {
		EN: "note: do not assume the current local files contain this PR. When investigating code, " +
			"inspect files by explicitly specifying the PR branch or head SHA/ref, or first verify " +
			"that the current codebase contains the relevant PR changes. If the PR head is not " +
			"available locally, retrieve it with Git or `gh`; failure to resolve a local object is " +
			"not evidence that the code is absent.",
		JA: "note: 現在のローカルのファイルにこの PR が含まれているとは限らない。コードを調べるときは、" +
			"PR のブランチか head の SHA/ref を明示してファイルを見るか、先に現在のコードベースに該当する PR の変更が" +
			"含まれていることを確かめる。PR の head がローカルに無ければ Git か `gh` で取得する。" +
			"ローカルで object を解決できないことは、コードが存在しない根拠にならない。",
	},
	// MERGED の PR を出すときの注記。stacked PR なら merge 先が main ではないし、あとから revert や修正が入ることもある。
	idNoteMerged: {
		EN: "note: MERGED does not put the code in main or prove it is present now - it may " +
			"have merged into another branch, or been reverted/amended since. Verify the " +
			"relevant change.",
		JA: "note: MERGED は、コードが main に入ったことも今もあることも示さない。別のブランチにマージされたか、" +
			"その後 revert・修正された可能性がある。該当する変更を確かめる。",
	},
	// ローカルの状態の行。既存の worktree の利用先は案内しない。object が無ければ取得して調査を続けさせる。
	idLocalHeadMatches: {
		EN: "  local: current HEAD matches the PR head; verify relevant files before concluding",
		JA: "  local: 現在の HEAD は PR の head と一致する。結論の前に該当するファイルを確かめる",
	},
	idLocalAvailable: {
		EN: "  local: PR head object is available; inspect it by explicit SHA/ref",
		JA: "  local: PR の head の object はローカルにある。SHA/ref を明示して調べる",
	},
	idLocalMissing: {
		EN: "  local: PR head object is not available; retrieve it with Git or gh before code conclusions",
		JA: "  local: PR の head の object はローカルに無い。コードについて結論する前に Git か gh で取得する",
	},
	idLocalNoClone: {
		EN: "  local: no clone of this repo here - do not read local files for it",
		JA: "  local: この repo の clone はここに無い。この repo のためにローカルのファイルを読まない",
	},
})
