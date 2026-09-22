package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyOnigiri/hhx/internal/version"
)

func checkerFor(t *testing.T, status int, body string) Checker {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("the release check sent credentials: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return Checker{Endpoint: server.URL, Client: server.Client()}
}

func TestLatestReadsTheReleaseTag(t *testing.T) {
	checker := checkerFor(t, http.StatusOK, `{"tag_name":"v1.2.3","html_url":"https://example.test/r/v1.2.3"}`)
	release, err := checker.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v1.2.3" || release.URL != "https://example.test/r/v1.2.3" {
		t.Fatalf("release=%+v", release)
	}
}

func TestLatestFallsBackToTheReleasesPage(t *testing.T) {
	release, err := checkerFor(t, http.StatusOK, `{"tag_name":"v1.2.3"}`).Latest(context.Background())
	if err != nil || release.URL != ReleasesPage {
		t.Fatalf("release=%+v err=%v", release, err)
	}
}

func TestLatestClassifiesFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "rate limited (403)", status: http.StatusForbidden, want: ErrRateLimited},
		{name: "rate limited (429)", status: http.StatusTooManyRequests, want: ErrRateLimited},
		{name: "no release", status: http.StatusNotFound, want: ErrUnavailable},
		{name: "non-release tag", status: http.StatusOK, body: `{"tag_name":"nightly"}`, want: ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := checkerFor(t, test.status, test.body).Latest(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
}

func TestLatestReportsOtherFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusBadGateway},
		{name: "broken json", status: http.StatusOK, body: "{"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := checkerFor(t, test.status, test.body).Latest(context.Background())
			if err == nil || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUnavailable) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	if got, ok := ParseVersion("v10.0.3"); !ok || got != (Version{Major: 10, Patch: 3}) {
		t.Fatalf("ParseVersion(v10.0.3)=(%+v, %v)", got, ok)
	}
	for _, value := range []string{"", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3", "v1.2.3-dev", "v1.+2.3", "v1..3", "vx.y.z"} {
		if _, ok := ParseVersion(value); ok {
			t.Errorf("ParseVersion(%q) was accepted", value)
		}
	}
}

func TestNewer(t *testing.T) {
	tests := []struct {
		current, candidate string
		want               bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.10.0", "v1.9.9", false},
		{"v2.0.0", "v1.9.9", false},
		{"v1.2.3-dev", "v9.9.9", false},
		{"v1.2.3", "latest", false},
	}
	for _, test := range tests {
		if got := Newer(test.current, test.candidate); got != test.want {
			t.Errorf("Newer(%q, %q)=%v, want %v", test.current, test.candidate, got, test.want)
		}
	}
}

func TestReleaseBuildFollowsBuildMeta(t *testing.T) {
	old := version.BuildMeta
	t.Cleanup(func() { version.BuildMeta = old })
	if ReleaseBuild() {
		t.Fatal("the test binary must be a development build")
	}
	version.BuildMeta = ""
	if !ReleaseBuild() {
		t.Fatal("an empty BuildMeta must be a release build")
	}
}

func TestConstantsPointAtTheDistributionRepository(t *testing.T) {
	for _, value := range []string{LatestReleaseAPI, ReleasesPage, DownloadBase} {
		if !strings.Contains(value, ownerRepository) {
			t.Errorf("%q does not point at %s", value, ownerRepository)
		}
	}
}
