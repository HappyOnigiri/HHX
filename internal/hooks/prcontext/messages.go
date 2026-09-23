package prcontext

// 注入する文面は英語のまま移した（移植元と同じ）。モデルが読む注記で、利用者向けの理由文ではない。

// note はローカルの作業ツリーを PR の中身だと思い込ませないための注記である。毎回付ける。
const note = "note: do not assume the current local files contain this PR. When investigating code, " +
	"inspect files by explicitly specifying the PR branch or head SHA/ref, or first verify " +
	"that the current codebase contains the relevant PR changes. If the PR head is not " +
	"available locally, retrieve it with Git or `gh`; failure to resolve a local object is " +
	"not evidence that the code is absent."

// noteMerged は MERGED の PR を出すときの注記である。MERGED でもローカルの現行ファイルが PR の内容とは限らない。
// stacked PR なら merge 先が main ではないし、あとから revert や修正が入ることもある。
const noteMerged = "note: MERGED does not put the code in main or prove it is present now - it may " +
	"have merged into another branch, or been reverted/amended since. Verify the " +
	"relevant change."

// header は注入の見出しである。タグ名で用途は自明なので、本文を含まないことだけを添える。
const header = "<pr-context> PRs referenced in the prompt (metadata only, no body)"

const footer = "</pr-context>"

// ローカルの状態の行。既存の worktree の利用先は案内しない。object が無ければ取得して調査を続けさせる。
const (
	localHeadMatches = "  local: current HEAD matches the PR head; verify relevant files before concluding"
	localAvailable   = "  local: PR head object is available; inspect it by explicit SHA/ref"
	localMissing     = "  local: PR head object is not available; retrieve it with Git or gh before code conclusions"
	localNoClone     = "  local: no clone of this repo here - do not read local files for it"
)
