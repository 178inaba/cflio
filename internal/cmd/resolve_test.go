package cmd

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// spaceHomepageAPI answers the space lookup with spacesJSON and a page read of
// the homepage it names, recording the keys each lookup asked for and every
// path requested. Paths are matched exactly so that a request the command
// should not be making fails the test.
type spaceHomepageAPI struct {
	spacesJSON string
	pagePath   string

	keys  []string
	paths []string
}

func (a *spaceHomepageAPI) handler(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		a.paths = append(a.paths, r.URL.Path)
		switch r.URL.Path {
		case spacesPath:
			a.keys = append(a.keys, r.URL.Query().Get("keys"))
			_, _ = w.Write([]byte(a.spacesJSON))
		case a.pagePath:
			_, _ = w.Write([]byte(pageResponse(t, "<p>home</p>", testPageWebUI)))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}
}

func readInto(t *testing.T, arg string, extra ...string) (cliRun, error) {
	t.Helper()
	return runRead(t, arg, filepath.Join(t.TempDir(), "page.xml"), extra...)
}

func TestReadResolvesASpaceURLToItsHomepage(t *testing.T) {
	for _, arg := range []string{
		testSpaceOverviewURL,
		testWikiBase + "/spaces/DEV",
		testWikiBase + "/spaces/DEV/",
	} {
		t.Run(arg, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			api := &spaceHomepageAPI{
				spacesJSON: testSpaceJSON,
				pagePath:   "/wiki/api/v2/pages/123456",
			}
			startAPI(t, api.handler(t))

			if _, err := readInto(t, arg); err != nil {
				t.Fatalf("read error = %v", err)
			}
			want := []string{spacesPath, api.pagePath}
			if strings.Join(api.paths, " ") != strings.Join(want, " ") {
				t.Errorf("requests = %v, want one lookup then the homepage read %v", api.paths, want)
			}
			if len(api.keys) != 1 || api.keys[0] != "DEV" {
				t.Errorf("keys = %v, want [DEV]", api.keys)
			}
		})
	}
}

// The profile comes from the URL's host just as it does for a page URL. Every
// profile reaches the same test server, so the site the printed URL is built
// on is what tells them apart.
func TestReadSelectsTheProfileFromTheSpaceURLHost(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net")

	api := &spaceHomepageAPI{
		spacesJSON: `{"results":[{"key":"OPS","homepageId":"123456"}]}`,
		pagePath:   "/wiki/api/v2/pages/123456",
	}
	startAPI(t, api.handler(t))

	run, err := readInto(t, "https://other.atlassian.net/wiki/spaces/OPS/overview")
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if want := "https://other.atlassian.net/wiki" + testPageWebUI; !strings.Contains(run.stdout, want) {
		t.Errorf("output = %q, want the page under the other site (%s)", run.stdout, want)
	}
}

func TestReadTakesTheHomepageIDFromTheURLWithoutALookup(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	api := &spaceHomepageAPI{pagePath: "/wiki/api/v2/pages/163938"}
	startAPI(t, api.handler(t))

	if _, err := readInto(t, testWikiBase+"/spaces/DEV/overview?homepageId=163938"); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if len(api.keys) != 0 {
		t.Errorf("space lookups = %v, want none when the URL carries homepageId", api.keys)
	}
}

// Go's query encoding leaves `~` as it is, so the assertion is on the value the
// server decoded rather than on the raw query.
func TestReadSendsAPersonalSpaceKeyUnchanged(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	const key = "~557058fae99fa6ec164eb494cd6e480da6cabf"
	api := &spaceHomepageAPI{
		spacesJSON: `{"results":[{"key":"` + key + `","homepageId":"123456"}]}`,
		pagePath:   "/wiki/api/v2/pages/123456",
	}
	startAPI(t, api.handler(t))

	if _, err := readInto(t, testWikiBase+"/spaces/%7E557058fae99fa6ec164eb494cd6e480da6cabf/overview"); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if len(api.keys) != 1 || api.keys[0] != key {
		t.Errorf("keys = %v, want [%s]", api.keys, key)
	}
}

func TestReadFailsNamingTheKeyWhenTheSpaceHasNoHomepageToRead(t *testing.T) {
	tests := []struct {
		name       string
		spacesJSON string
	}{
		{name: "no such space", spacesJSON: `{"results":[]}`},
		{name: "space without a homepage", spacesJSON: `{"results":[{"key":"DEV","homepageId":""}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			// No page path: a page request after the failed lookup is an
			// unexpected one.
			api := &spaceHomepageAPI{spacesJSON: tt.spacesJSON}
			startAPI(t, api.handler(t))

			_, err := readInto(t, testSpaceOverviewURL)
			if err == nil {
				t.Fatal("read error = nil, want an error")
			}
			if !strings.Contains(err.Error(), `"DEV"`) {
				t.Errorf("error = %q, want it to name the key DEV", err)
			}
			if code, _ := describeFailure(err, time.Second); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
		})
	}
}

func TestReadReportsADeadlineThatExpiresDuringTheSpaceLookup(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path != spacesPath {
			t.Errorf("unexpected request to %s", r.URL.Path)
			return
		}
		<-r.Context().Done()
	})

	_, err := readInto(t, testSpaceOverviewURL, "--timeout=200ms")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to carry context.DeadlineExceeded", err)
	}
	if code, _ := describeFailure(err, 200*time.Millisecond); code != 124 {
		t.Errorf("exit code = %d, want 124", code)
	}
}

// The refusal's wording, including its list of accepted forms, is
// TestParseErrors' job; what is left here is that nothing is sent.
func TestReadRefusesOtherSpaceURLsBeforeAnyRequest(t *testing.T) {
	for _, arg := range []string{
		testWikiBase + "/spaces/DEV/pages",
		testWikiBase + "/spaces/DEV/blog/2026/01/01/123456/Post",
	} {
		t.Run(arg, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			startAPI(t, neverCalled(t, "for a URL that names no page"))

			if _, err := readInto(t, arg); err == nil {
				t.Error("read error = nil, want an error")
			}
		})
	}
}

// pageIDOf's default branch cannot be reached through a parsed argument, since
// parsing never yields an empty target; it is what keeps a reference that
// names nothing from turning into a request for /pages/ with no ID.
func TestPageIDOfRefusesATargetThatNamesNoPage(t *testing.T) {
	id, err := pageIDOf(t.Context(), nil, nil)
	if err == nil {
		t.Fatalf("pageIDOf(nil) = %q, want an error", id)
	}
}
