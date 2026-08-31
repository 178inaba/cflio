package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func runSearchCmd(t *testing.T, cql string, limit int, extra ...string) (cflioRun, error) {
	t.Helper()
	return runLimitCmd(t, "search", cql, limit, extra...)
}

func TestSearchPassesTheQueryThroughUnchanged(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	cql := `type = page and space = "DEV" and text ~ "release notes"`

	var gotCQL string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotCQL = r.URL.Query().Get("cql")
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"Release Notes"},` +
			`"url":"/spaces/DEV/pages/1/Release+Notes"}],"totalSize":1}`))
	})

	run, err := runSearchCmd(t, cql, 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if gotCQL != cql {
		t.Errorf("cql = %q, want it passed through unchanged as %q", gotCQL, cql)
	}
	for _, want := range []string{"Release Notes", "page", "ID 1", testSite + "/spaces/DEV/pages/1/Release+Notes"} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
}

func TestSearchReportsHowManyResultsWereLeftOut(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/x"}],` +
			`"totalSize":5}`))
	})

	run, err := runSearchCmd(t, "type = page", 1)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if !strings.Contains(run.stdout, "4 more results") {
		t.Errorf("output = %q, want a notice saying 4 results remain (5 total - 1 shown)", run.stdout)
	}
	if noticeIdx, itemIdx := strings.Index(run.stdout, "more results"), strings.Index(run.stdout, "**A**"); noticeIdx < itemIdx {
		t.Errorf("output = %q, want the notice after the results", run.stdout)
	}
}

func TestSearchOmitsTheNoticeWhenTheServerRanOutEarly(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// totalSize claims far more than the server will actually hand back.
	// Fewer results than --limit means paging stopped because the server
	// ran out, so telling the caller to raise --limit would be useless.
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") == "0" {
			_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/x"}],` +
				`"totalSize":50}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"totalSize":50}`))
	})

	run, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(run.stdout, "more results") {
		t.Errorf("output = %q, want no notice when fewer than --limit results came back", run.stdout)
	}
}

func TestSearchOmitsTheNoticeWhenEverythingFits(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/x"}],` +
			`"totalSize":1}`))
	})

	run, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(run.stdout, "more results") {
		t.Errorf("output = %q, want no truncation notice", run.stdout)
	}
}

func TestSearchStripsHighlightMarkersAndHandlesNonContentHits(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"content":{"id":"1","type":"page","title":"The @@@hl@@@Release@@@endhl@@@ Notes"},"url":"/p"},
			{"title":"@@@hl@@@Dev@@@endhl@@@ Space","entityType":"space","url":"/spaces/DEV"}
		],"totalSize":2}`))
	})

	run, err := runSearchCmd(t, "release", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(run.stdout, "@@@hl@@@") || strings.Contains(run.stdout, "@@@endhl@@@") {
		t.Errorf("output = %q, want the highlight markers stripped", run.stdout)
	}
	if !strings.Contains(run.stdout, "The Release Notes") {
		t.Errorf("output = %q, want the un-marked page title", run.stdout)
	}
	// A space result carries no content object, so it falls back to the
	// top-level title and entityType, with no ID to show.
	if !strings.Contains(run.stdout, "**Dev Space** (space)") {
		t.Errorf("output = %q, want the space rendered from entityType with no ID", run.stdout)
	}
}

func TestSearchLeavesAbsoluteResultURLsAlone(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},` +
			`"url":"https://elsewhere.example/p/1"}],"totalSize":1}`))
	})

	run, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if !strings.Contains(run.stdout, "https://elsewhere.example/p/1") {
		t.Errorf("output = %q, want an already-absolute url left as-is", run.stdout)
	}
	if strings.Contains(run.stdout, testSite+"/https") {
		t.Errorf("output = %q, want no double-prefixed url", run.stdout)
	}
}

func TestSearchJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/p"}],` +
			`"totalSize":3}`))
	})

	run, err := runSearchCmd(t, "type = page", 1, "--format", "json")
	if err != nil {
		t.Fatalf("search error = %v", err)
	}

	var got struct {
		Results []struct {
			Title string `json:"title"`
			Type  string `json:"type"`
			ID    string `json:"id"`
			URL   string `json:"url"`
		} `json:"results"`
		Notice string `json:"notice"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	if len(got.Results) != 1 || got.Results[0].ID != "1" || got.Results[0].Type != "page" {
		t.Errorf("results = %+v, want the single page hit", got.Results)
	}
	if !strings.Contains(got.Notice, "2 more results") {
		t.Errorf("notice = %q, want it to report the 2 remaining results", got.Notice)
	}
}

func TestSearchEmptyResults(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "markdown", format: "md", want: "No results."},
		{name: "json keeps an empty array", format: "json", want: `"results": []`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"results":[],"totalSize":0}`))
			})

			run, err := runSearchCmd(t, "type = page", 20, "--format", tt.format)
			if err != nil {
				t.Fatalf("search error = %v", err)
			}
			if !strings.Contains(run.stdout, tt.want) {
				t.Errorf("output = %q, want it to contain %q", run.stdout, tt.want)
			}
		})
	}
}

func TestSearchRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an invalid --limit"))

	for _, limit := range []int{0, -1, maxLimit + 1} {
		if _, err := runSearchCmd(t, "type = page", limit); err == nil {
			t.Errorf("search error = nil for --limit %d, want an error", limit)
		}
	}
}

// TestSearchHonoursTheTimeoutFlag covers the root persistent flags reaching
// a request: their values are only final after parsing, so a constructor
// that read them while building the tree would leave every invocation on
// the default deadline.
func TestSearchHonoursTheTimeoutFlag(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an already-expired deadline"))

	_, err := runSearchCmd(t, "type = page", 20, "--timeout=1ns")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to carry context.DeadlineExceeded", err)
	}
}

func TestSearchUsesTheDefaultProfile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	var gotUser string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotUser, _, _ = r.BasicAuth()
		_, _ = w.Write([]byte(`{"results":[],"totalSize":0}`))
	})

	if _, err := runSearchCmd(t, "type = page", 20); err != nil {
		t.Fatalf("search error = %v", err)
	}
	if gotUser == "" {
		t.Error("search sent no credentials; it should fall back to the default profile")
	}
}
