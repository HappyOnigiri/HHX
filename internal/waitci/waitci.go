// Package waitci は、PR の CI が全ジョブ完了したときに 1 回だけ結果を返す `hhx wait-ci` の判定の本体である。
//
// `gh pr checks --watch` はジョブが 1 件終わる度に出力するため、push 後の自動確認には使わない。
// 通常は `gh pr view --json headRefOid,mergeable,statusCheckRollup` の 1 リクエストで判定し、
// detached worktree では先に commit SHA から `gh pr list` で PR 番号を解決してから同じ判定を行い、
// CheckRun (GitHub Actions 等) と StatusContext (旧 status API) の両方を同じ形へ正規化する。
//
// push 直後の誤判定を防ぐために 3 つの待ちを持つ:
//  1. head 待ち … 監視対象の commit が PR の head になるまで判定を始めない。
//     これが無いと、push が GitHub へ反映される前に旧 commit の完了済み check を見て即座に成功と答える。
//  2. settle 待ち … 「pending 0 件」が settle 秒続き、かつ check の集合が変わらないことを確認する。
//     これが無いと、先行ジョブの結果で起動する後続 workflow (coverage 等) を取りこぼす。
//  3. PR 解決待ち … detached HEAD で commit から PR を引くのを lookup timeout まで再試行する。
//     これが無いと、検索インデックスへの反映が遅れている間に「PR が無い」と判断して監視ごと飛ばす。
//
// base とコンフリクトした PR では `pull_request` の workflow が起動しないため、check が
// 1 件も現れないまま start timeout まで待つことになる。mergeable が CONFLICTING で
// check が 0 件ならその時点で返す。check が動いている repo (push トリガ等) では通常どおり
// 完了まで待ち、結果にコンフリクトを併記する。
//
// CI を持たない repo でも check は 0 件のままなので、同じく start timeout まで待ってしまう。
// check が 1 件も無いまま no-CI timeout が過ぎたら、そこで初めて repo 側に CI の形跡が
// あるかを調べ、無ければ待たずに終わる (GH.CIEvidence を参照)。形跡があるか判定できない
// 場合は従来どおり start timeout まで待つ。
//
// Python 実装（wait-ci）から意味を 1 対 1 で移した。出力と終了コードの契約は cmd/hhx の wait-ci が持つ。
package waitci

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// FetchFailureLimit は gh の連続失敗の許容回数である。瞬断で監視を落とさない。
	FetchFailureLimit = 5
	// ciSample は CI の形跡を探す直近の merged PR の件数である。
	ciSample = 5
	// conflicting は GraphQL の MergeableState のうちコンフリクトを表す値である。
	// mergeable は GitHub 側の非同期計算で、push 直後やマージコミットの再計算中は UNKNOWN が返る。
	// 「MERGEABLE でない」ではなくこの値そのものと比べ、計算中を衝突と誤判定しない。
	conflicting = "CONFLICTING"
)

// passResults は成功とみなす結果である（大文字にしてから比べる）。
var passResults = map[string]bool{"SUCCESS": true, "NEUTRAL": true, "SKIPPED": true}

// NoPRMarkers は gh が「PR が無い」ときだけ stderr に出す語である。認証・通信の失敗と区別できる。
var NoPRMarkers = []string{"no pull requests found", "no open pull requests"}

// fatalMarkers は待っても直らない失敗の語である。リトライすると start timeout まで無駄に待つので即座に返す。
var fatalMarkers = []string{
	"could not determine current branch",
	"not a git repository",
	"no git remotes found",
	"could not resolve to a pullrequest",
	"could not resolve to a repository",
}

// FetchError は gh の呼び出しの失敗である。Retryable が偽なら待っても直らない。
type FetchError struct {
	Message   string
	Retryable bool
}

func (e *FetchError) Error() string { return e.Message }

// NoPullRequestError は対象に PR が無いことを表す。
type NoPullRequestError struct {
	Message string
}

func (e *NoPullRequestError) Error() string { return e.Message }

// Check は 1 件の check の正規化した状態である。
type Check struct {
	Name    string
	Done    bool
	Result  string
	URL     string
	Seconds int
}

// Failed は完了していて成功とみなせない結果なら真を返す。
func (c Check) Failed() bool {
	return c.Done && !passResults[strings.ToUpper(c.Result)]
}

// Snapshot は 1 回の poll で見えた PR の状態である。
type Snapshot struct {
	Head string
	// Mergeable は GraphQL の MergeableState。MERGEABLE / CONFLICTING / UNKNOWN のいずれか。
	Mergeable string
	Checks    []Check
}

// Conflicting は base とコンフリクトしているなら真を返す。
func (s Snapshot) Conflicting() bool { return s.Mergeable == conflicting }

// Status は監視の終わり方である。
type Status string

// 監視の終わり方。
const (
	StatusComplete     Status = "complete"
	StatusTimeout      Status = "timeout"
	StatusHeadTimeout  Status = "head_timeout"
	StatusEmptyTimeout Status = "empty_timeout"
	StatusNoCI         Status = "no_ci"
	StatusConflict     Status = "conflict"
	StatusNoPR         Status = "no_pr"
	StatusError        Status = "error"
)

// Outcome は監視の結果である。
type Outcome struct {
	Status      Status
	Head        string
	Checks      []Check
	Elapsed     int
	Message     string
	Conflicting bool
}

// Failed は失敗した check を元の順で返す。
func (o Outcome) Failed() []Check {
	var failed []Check
	for _, check := range o.Checks {
		if check.Failed() {
			failed = append(failed, check)
		}
	}
	return failed
}

// timestampPattern は Python の time.strptime(value, "%Y-%m-%dT%H:%M:%SZ") が受け付ける形である。
// strptime は大文字小文字を区別せず、%m・%d・%H・%M・%S は 1 桁も、%d は空白で埋めた 1 桁も、%S は 61 までを受け付ける。
// 選択肢の並びは _strptime の正規表現と同じにしてある（RE2 も先に書いた選択肢を優先する）。
// Python の \d は Unicode の数字にも一致するが、GitHub の時刻には現れないので ASCII に限る。
var timestampPattern = regexp.MustCompile(`(?i)^(\d{4})-(1[0-2]|0[1-9]|[1-9])-(3[01]|[12]\d|0[1-9]|[1-9]| [1-9])` +
	`T(2[0-3]|[0-1]\d|\d):([0-5]\d|\d):(6[0-1]|[0-5]\d|\d)Z$`)

// parseTimestamp は GitHub の UTC の時刻を Unix 秒にする。解釈できなければ偽を返す。
// ローカル時刻として解釈すると DST の境界で差がずれるので、UTC として扱う（Python の calendar.timegm）。
func parseTimestamp(value string) (int64, bool) {
	match := timestampPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	fields := make([]int, 6)
	for index := range fields {
		number, err := strconv.Atoi(strings.TrimSpace(match[index+1]))
		if err != nil {
			return 0, false
		}
		fields[index] = number
	}
	year, month, day := fields[0], time.Month(fields[1]), fields[2]
	// strptime は日付として存在しない日（2 月 30 日など）と、datetime が扱えない 0 年を ValueError にする。
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if year < 1 || date.Day() != day || date.Month() != month {
		return 0, false
	}
	// timegm は 60 秒・61 秒を次の分へ繰り上げて数える。
	return date.Unix() + int64(fields[3]*3600+fields[4]*60+fields[5]), true
}

// secondsBetween は 2 点間の秒数を返す。片方でも欠けているか、逆転していれば 0 を返す。
func secondsBetween(started, completed string) int {
	start, okStart := parseTimestamp(started)
	end, okEnd := parseTimestamp(completed)
	if !okStart || !okEnd || end < start {
		return 0
	}
	return int(end - start)
}

// stringField は JSON のオブジェクトから文字列の値を取る。
// 文字列でない値は欠けているものとして扱う（GitHub の応答では起きない）。
func stringField(node map[string]any, key string) string {
	value, _ := node[key].(string)
	return value
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Normalize は statusCheckRollup の 2 種類のノードを Check へ揃える。
//
// CheckRun は status が COMPLETED になって初めて conclusion が決まる。
// StatusContext には status が無く、state の PENDING/EXPECTED が未完了を表す。
// オブジェクトでないノードは読み飛ばす。__typename が無いノードは CheckRun として扱う。
func Normalize(rollup []any) []Check {
	checks := []Check{}
	for _, item := range rollup {
		node, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typename, present := node["__typename"]
		if !present || typename == "CheckRun" {
			workflow := stringField(node, "workflowName")
			name := orDefault(stringField(node, "name"), "?")
			if workflow != "" {
				name = workflow + " / " + name
			}
			status := stringField(node, "status")
			checks = append(checks, Check{
				Name:    name,
				Done:    status == "COMPLETED",
				Result:  orDefault(orDefault(stringField(node, "conclusion"), status), "?"),
				URL:     stringField(node, "detailsUrl"),
				Seconds: secondsBetween(stringField(node, "startedAt"), stringField(node, "completedAt")),
			})
			continue
		}
		state := strings.ToUpper(stringField(node, "state"))
		checks = append(checks, Check{
			Name:   orDefault(stringField(node, "context"), "?"),
			Done:   state != "PENDING" && state != "EXPECTED" && state != "",
			Result: orDefault(stringField(node, "state"), "?"),
			URL:    stringField(node, "targetUrl"),
		})
	}
	return checks
}

// Summarize は失敗を先頭に並べた結果一覧を返す。失敗にはジョブの URL を添える。
//
// 既定では失敗した check だけを返す。成功も含めて並べると 20 行を超え、読み手が
// tail で切り詰めて結論を落とす。全件が要るときだけ allChecks を立てる。
// 並びは Python の sorted(key=(not failed, name)) に合わせる（安定ソートで、名前は code point 順）。
func Summarize(checks []Check, allChecks bool) []string {
	sorted := append([]Check(nil), checks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Failed() != sorted[j].Failed() {
			return sorted[i].Failed()
		}
		return sorted[i].Name < sorted[j].Name
	})
	var lines []string
	for _, check := range sorted {
		if !allChecks && !check.Failed() {
			continue
		}
		// Python の {:<9} と同じく、文字数で数えて左詰めにする。
		lines = append(lines, "  "+padRight(strings.ToUpper(check.Result), 9)+" "+check.Name+"  "+
			strconv.Itoa(check.Seconds)+"s")
		if check.Failed() && check.URL != "" {
			lines = append(lines, "            "+check.URL)
		}
	}
	return lines
}

func padRight(value string, width int) string {
	if count := len([]rune(value)); count < width {
		return value + strings.Repeat(" ", width-count)
	}
	return value
}
