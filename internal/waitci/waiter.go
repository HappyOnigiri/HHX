package waitci

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Clock は単調時計と sleep である。テストは sleep した分だけ進む時計に差し替える。
type Clock interface {
	// Now は基準時点からの経過時間を返す。
	Now() time.Duration
	Sleep(d time.Duration)
}

type realClock struct{ base time.Time }

// NewClock は実時間の単調時計を返す。
func NewClock() Clock { return realClock{base: time.Now()} }

// Now は time.Since で測るので、壁時計が動いても単調に進む。
func (c realClock) Now() time.Duration { return time.Since(c.base) }

func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

func seconds(value int) time.Duration { return time.Duration(value) * time.Second }

// elapsedSeconds は Python の int(now - start) と同じく、秒へ切り捨てる。
// 出力の秒数と判定の境界はこの丸めに依存する。
func elapsedSeconds(d time.Duration) int { return int(d / time.Second) }

// ResolvePR は commit を含む open PR の番号を返す。push 直後の反映遅れの分だけ待つ。
//
// `gh pr list --search` は GitHub の検索インデックス越しに引くため、push が
// そこへ反映されるまでは「PR がまだ無い」ときと同じ空の結果を返す。1 回で
// 諦めると、PR が実在するのに監視を丸ごとスキップして成功扱いで終わる。
// lookupTimeout まで interval 間隔で引き直し、それでも空なら PR が無いとする。
// 失敗は *NoPullRequestError か *FetchError で返す。
func ResolvePR(
	sha string, lookupTimeout, interval int, find func(string) (string, error), clock Clock, progress func(string),
) (string, error) {
	start := clock.Now()
	failures := 0
	for {
		number, err := find(sha)
		if err == nil {
			return number, nil
		}
		var pending *FetchError
		var noPR *NoPullRequestError
		switch {
		case errors.As(err, &noPR):
		case errors.As(err, &pending):
			failures++
			if !pending.Retryable || failures >= FetchFailureLimit {
				return "", pending
			}
		default:
			return "", err
		}
		elapsed := elapsedSeconds(clock.Now() - start)
		// 次の試行が上限を越えるなら、待たずにここで確定させる。
		if elapsed+interval > lookupTimeout {
			if pending != nil {
				return "", pending
			}
			detail := ""
			if elapsed != 0 {
				detail = fmt.Sprintf(" (%ds 再試行した)", elapsed)
			}
			return "", &NoPullRequestError{Message: fmt.Sprintf("commit %s に open PR が無い%s", sha, detail)}
		}
		if progress != nil {
			reason := "PR が見つからない"
			if pending != nil {
				reason = fmt.Sprintf("gh の呼び出しに失敗 (%d 回目)", failures)
			}
			progress(fmt.Sprintf("%ds commit %s の %s。再試行する", elapsed, sha, reason))
		}
		clock.Sleep(seconds(interval))
	}
}

// Waiter は poll して「全件完了」を判定する。時計と取得を差し替えられるようにしてある。
type Waiter struct {
	// Fetch は PR の状態を 1 回取る。失敗は *NoPullRequestError か *FetchError で返す。
	Fetch func() (Snapshot, error)
	// SHA は PR の head になるまで判定を待つ commit である。空なら最初に見えた head を対象にする。
	SHA string
	// 以下は秒単位。
	Interval     int
	Timeout      int
	StartTimeout int
	Settle       int
	NoCITimeout  int
	// HasCI は repo に CI の形跡があるかを返す。判定できなければ *FetchError を返す。nil なら問い合わせない。
	HasCI    func() (bool, error)
	Clock    Clock
	Progress func(string)

	// ciKnown は形跡の問い合わせの結果である。repo ごとに 1 回で足りる。nil は未実施。
	ciKnown *bool
}

// watchState は Run の 1 回の監視の間だけ持つ状態である。
type watchState struct {
	start         time.Duration
	established   bool
	target        string
	checks        []Check
	head          string
	conflicting   bool
	failures      int
	doneSince     *time.Duration
	doneSignature string
}

// Run は監視が終わるまで poll し、結果を返す。
func (w *Waiter) Run() Outcome {
	state := &watchState{start: w.Clock.Now(), established: w.SHA == "", target: w.SHA}
	for {
		now := w.Clock.Now()
		elapsed := elapsedSeconds(now - state.start)
		snapshot, err := w.Fetch()
		if err != nil {
			var noPR *NoPullRequestError
			if errors.As(err, &noPR) {
				return Outcome{Status: StatusNoPR, Elapsed: elapsed, Message: noPR.Message}
			}
			retryable := true
			var fetchErr *FetchError
			if errors.As(err, &fetchErr) {
				retryable = fetchErr.Retryable
			}
			state.failures++
			if !retryable || state.failures >= FetchFailureLimit {
				return Outcome{Status: StatusError, Elapsed: elapsed, Message: err.Error()}
			}
			w.report(fmt.Sprintf("%ds gh の呼び出しに失敗 (%d 回目)", elapsed, state.failures))
			w.Clock.Sleep(seconds(w.Interval))
			continue
		}
		state.head, state.checks, state.conflicting = snapshot.Head, snapshot.Checks, snapshot.Conflicting()
		state.failures = 0

		var outcome *Outcome
		if !state.established && state.head != state.target {
			// push が GitHub へ反映される前は、旧 commit の完了済み check が見える。
			w.report(fmt.Sprintf("%ds head=%s が %s になるのを待つ", elapsed, orDefault(state.head, "?"), state.target))
		} else {
			elapsed, outcome = w.judge(state, now, elapsed)
			if outcome != nil {
				return *outcome
			}
		}
		if outcome = w.limit(state, elapsed); outcome != nil {
			return *outcome
		}
		w.Clock.Sleep(seconds(w.Interval))
	}
}

// judge は head が監視対象に一致した poll で、完了・コンフリクトを判定する。測り直したら elapsed を 0 にして返す。
func (w *Waiter) judge(state *watchState, now time.Duration, elapsed int) (int, *Outcome) {
	switch {
	case !state.established:
		state.established = true
		state.target = state.head
		state.start = now
		elapsed = 0
	case state.target == "":
		// SHA を指定しない監視の初回。最初に見えた commit を対象にする。
		state.target = state.head
	case state.head != "" && state.head != state.target:
		// 監視中の別 push。新しい commit を対象にして測り直す。
		w.report(fmt.Sprintf("head が %s -> %s へ変わった", state.target, state.head))
		state.target = state.head
		state.start = now
		elapsed = 0
		state.doneSince = nil
		state.doneSignature = ""
	}

	if state.conflicting && len(state.checks) == 0 {
		// コンフリクト中は `pull_request` の workflow が起動せず、待っても
		// check は現れない。push トリガ等で check が動いている間は待ち続ける。
		return elapsed, &Outcome{Status: StatusConflict, Head: state.target, Elapsed: elapsed, Conflicting: true}
	}

	pending := 0
	for _, check := range state.checks {
		if !check.Done {
			pending++
		}
	}
	if len(state.checks) > 0 && pending == 0 {
		names := make([]string, 0, len(state.checks))
		for _, check := range state.checks {
			names = append(names, check.Name)
		}
		sort.Strings(names)
		signature := strings.Join(names, "|")
		if signature == state.doneSignature && state.doneSince != nil && now-*state.doneSince >= seconds(w.Settle) {
			return elapsed, &Outcome{
				Status: StatusComplete, Head: state.target, Checks: state.checks,
				Elapsed: elapsed, Conflicting: state.conflicting,
			}
		}
		if signature != state.doneSignature {
			state.doneSignature = signature
			since := now
			state.doneSince = &since
		}
		w.report(fmt.Sprintf("%ds total=%d pending=0 settling", elapsed, len(state.checks)))
	} else {
		state.doneSince = nil
		state.doneSignature = ""
		w.report(fmt.Sprintf("%ds total=%d pending=%d", elapsed, len(state.checks), pending))
	}
	return elapsed, nil
}

// limit は待ちの上限を越えたかを判定する。
func (w *Waiter) limit(state *watchState, elapsed int) *Outcome {
	switch {
	case !state.established:
		if elapsed >= w.StartTimeout {
			return &Outcome{Status: StatusHeadTimeout, Head: state.head, Elapsed: elapsed, Message: state.target}
		}
	case len(state.checks) == 0:
		// 形跡の確認は no-CI timeout を越えてから行う。CI のある repo では
		// それまでに check が登録されるので、通常の経路で gh 呼び出しを増やさない。
		if elapsed >= w.NoCITimeout && w.ciAbsent() {
			return &Outcome{Status: StatusNoCI, Head: state.head, Elapsed: elapsed}
		}
		if elapsed >= w.StartTimeout {
			return &Outcome{Status: StatusEmptyTimeout, Head: state.head, Elapsed: elapsed}
		}
	case elapsed >= w.Timeout:
		return &Outcome{
			Status: StatusTimeout, Head: state.target, Checks: state.checks,
			Elapsed: elapsed, Conflicting: state.conflicting,
		}
	}
	return nil
}

// ciAbsent はこの repo に CI の形跡が無いなら真を返す。判定できなければ偽 (従来どおり待つ)。
func (w *Waiter) ciAbsent() bool {
	if w.HasCI == nil {
		return false
	}
	if w.ciKnown == nil {
		known, err := w.HasCI()
		if err != nil {
			// 分からないときは「CI はある」側に倒す。ここで待ちを打ち切ると、
			// 登録が遅れているだけの check を見ないまま成功で終わってしまう。
			w.report(fmt.Sprintf("CI の有無を判定できない (%s)。待ちを続ける", err.Error()))
			known = true
		}
		w.ciKnown = &known
	}
	return !*w.ciKnown
}

func (w *Waiter) report(text string) {
	if w.Progress != nil {
		w.Progress(text)
	}
}
