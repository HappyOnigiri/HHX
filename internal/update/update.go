// Package update は GitHub Releases から最新のリリースタグを読み、手元の版と比べて更新の有無を決める。
// 更新そのものは Release に添付した install.sh に委ねるため、バイナリの取得・検証・置換は持たない。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/HappyOnigiri/hhx/internal/version"
)

// 配布元は固定である。scripts/install.sh も同じリポジトリのリリース資産だけを取得する。
const (
	ownerRepository = "HappyOnigiri/HHX"
	// LatestReleaseAPI は draft と prerelease を除いた最新リリースを返す。
	// 公開に失敗して draft のまま残ったリリースを拾わないため、tags API ではなくこれを使う。
	LatestReleaseAPI = "https://api.github.com/repos/" + ownerRepository + "/releases/latest"
	// ReleasesPage は案内に載せる人間向けの一覧である。
	ReleasesPage = "https://github.com/" + ownerRepository + "/releases"
	// DownloadBase はタグごとのリリース資産の取得元である。
	DownloadBase = "https://github.com/" + ownerRepository + "/releases/download"
)

// ErrRateLimited は未認証のレート制限に触れたことを表す。次に取る行動が「待つ」である点だけが
// ネットワーク障害と違うため、他の失敗と区別する。
var ErrRateLimited = errors.New("the GitHub API rate limit was reached; try again later")

// ErrUnavailable は公開済みのリリースがまだ 1 つも無いことを表す。
var ErrUnavailable = errors.New("no release has been published yet")

// maxResponseBytes は応答の読み取り上限である。リリースの説明文は長くなりうるので、要る項目を読める範囲で切る。
const maxResponseBytes = 1 << 20

// Release は更新の判断に使うリリースの要点である。
type Release struct {
	// Tag は vX.Y.Z 形式のリリースタグ。
	Tag string
	// URL はリリースの人間向けのページで、案内に載せる。
	URL string
}

// ReleaseBuild は配布用ビルドかどうかを返す。開発ビルドは BuildMeta が dev のままで、
// これは Go の既定値でもあるので、テストのバイナリも必ず false になる。
func ReleaseBuild() bool { return version.BuildMeta == "" }

// Checker は最新リリースの取得口である。Endpoint と Client はテストで差し替える。
type Checker struct {
	// Endpoint は空なら LatestReleaseAPI を使う。
	Endpoint string
	// Client は空なら http.DefaultClient を使う。上限は呼び出し側が context に付ける。
	Client *http.Client
}

// Latest は公開済みの最新リリースを返す。tag_name が vX.Y.Z でない応答は ErrUnavailable にする。
// 配布元へ利用者の資格情報を送る理由が無いので、未認証で読む。
func (c Checker) Latest(ctx context.Context) (Release, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = LatestReleaseAPI
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "hhx/"+version.String())
	response, err := client.Do(request)
	if err != nil {
		return Release{}, fmt.Errorf("check the latest release: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, ErrRateLimited
	case http.StatusNotFound:
		return Release{}, ErrUnavailable
	default:
		return Release{}, fmt.Errorf("check the latest release: GitHub responded %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return Release{}, fmt.Errorf("check the latest release: %w", err)
	}
	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Release{}, fmt.Errorf("check the latest release: %w", err)
	}
	if _, ok := ParseVersion(payload.TagName); !ok {
		return Release{}, ErrUnavailable
	}
	release := Release{Tag: payload.TagName, URL: payload.HTMLURL}
	if release.URL == "" {
		release.URL = ReleasesPage
	}
	return release, nil
}

// Version はリリースタグの 3 つの整数である。タグは vX.Y.Z に固定で prerelease を許さないため、この形だけを扱う。
type Version struct{ Major, Minor, Patch int }

// ParseVersion は vX.Y.Z を読む。先行ゼロ・prerelease・-dev 付きの開発ビルドの表示は受け付けない。
func ParseVersion(value string) (Version, bool) {
	rest, found := strings.CutPrefix(value, "v")
	if !found {
		return Version{}, false
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	numbers := make([]int, 0, 3)
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') || strings.TrimLeft(part, "0123456789") != "" {
			return Version{}, false
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, false
		}
		numbers = append(numbers, number)
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, true
}

// Newer は candidate が current より新しいリリースかを返す。どちらかが vX.Y.Z でなければ false を返す。
func Newer(current, candidate string) bool {
	base, ok := ParseVersion(current)
	if !ok {
		return false
	}
	next, ok := ParseVersion(candidate)
	if !ok {
		return false
	}
	switch {
	case next.Major != base.Major:
		return next.Major > base.Major
	case next.Minor != base.Minor:
		return next.Minor > base.Minor
	default:
		return next.Patch > base.Patch
	}
}
