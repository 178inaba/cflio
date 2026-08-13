package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func runSearchCmd(t *testing.T, cql string, limit int, extra ...string) (string, error) {
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

	output, err := runSearchCmd(t, cql, 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if gotCQL != cql {
		t.Errorf("cql = %q, want it passed through unchanged as %q", gotCQL, cql)
	}
	for _, want := range []string{"Release Notes", "page", "ID 1", testSite + "/spaces/DEV/pages/1/Release+Notes"} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	}
}

func TestSearchReportsHowManyResultsWereLeftOut(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/x"}],` +
			`"totalSize":5}`))
	})

	output, err := runSearchCmd(t, "type = page", 1)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if !strings.Contains(output, "4 more results") {
		t.Errorf("output = %q, want a notice saying 4 results remain (5 total - 1 shown)", output)
	}
	if noticeIdx, itemIdx := strings.Index(output, "more results"), strings.Index(output, "**A**"); noticeIdx < itemIdx {
		t.Errorf("output = %q, want the notice after the results", output)
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

	output, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(output, "more results") {
		t.Errorf("output = %q, want no notice when fewer than --limit results came back", output)
	}
}

func TestSearchOmitsTheNoticeWhenEverythingFits(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/x"}],` +
			`"totalSize":1}`))
	})

	output, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(output, "more results") {
		t.Errorf("output = %q, want no truncation notice", output)
	}
}

func TestSearchStripsHighlightMarkersAndHandlesNonContentHits(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"content":{"id":"1","type":"page","title":"The @@@hl@@@Release@@@endhl@@@ Notes"},"url":"/p"},
			{"title":"@@@hl@@@Dev@@@endhl@@@ Space","entityType":"space","url":"/spaces/DEV"}
		],"totalSize":2}`))
	})

	output, err := runSearchCmd(t, "release", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if strings.Contains(output, "@@@hl@@@") || strings.Contains(output, "@@@endhl@@@") {
		t.Errorf("output = %q, want the highlight markers stripped", output)
	}
	if !strings.Contains(output, "The Release Notes") {
		t.Errorf("output = %q, want the un-marked page title", output)
	}
	// A space result carries no content object, so it falls back to the
	// top-level title and entityType, with no ID to show.
	if !strings.Contains(output, "**Dev Space** (space)") {
		t.Errorf("output = %q, want the space rendered from entityType with no ID", output)
	}
}

func TestSearchLeavesAbsoluteResultURLsAlone(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},` +
			`"url":"https://elsewhere.example/p/1"}],"totalSize":1}`))
	})

	output, err := runSearchCmd(t, "type = page", 20)
	if err != nil {
		t.Fatalf("search error = %v", err)
	}
	if !strings.Contains(output, "https://elsewhere.example/p/1") {
		t.Errorf("output = %q, want an already-absolute url left as-is", output)
	}
	if strings.Contains(output, testSite+"/https") {
		t.Errorf("output = %q, want no double-prefixed url", output)
	}
}

func TestSearchJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"content":{"id":"1","type":"page","title":"A"},"url":"/p"}],` +
			`"totalSize":3}`))
	})

	output, err := runSearchCmd(t, "type = page", 1, "--format", "json")
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
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
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

			startAPI(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"results":[],"totalSize":0}`))
			})

			output, err := runSearchCmd(t, "type = page", 20, "--format", tt.format)
			if err != nil {
				t.Fatalf("search error = %v", err)
			}
			if !strings.Contains(output, tt.want) {
				t.Errorf("output = %q, want it to contain %q", output, tt.want)
			}
		})
	}
}

func TestSearchRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an invalid --limit")
	})

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

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an already-expired deadline")
	})

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
