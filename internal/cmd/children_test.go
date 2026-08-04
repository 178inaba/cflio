package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func runChildrenCmd(t *testing.T, arg string, limit int) (string, error) {
	t.Helper()

	original := childrenLimitFlag
	childrenLimitFlag = limit
	t.Cleanup(func() { childrenLimitFlag = original })

	cmd, out := newTestCommand(t)
	err := runChildren(cmd, []string{arg})
	return out.String(), err
}

// childrenAPI answers the direct-children listing with childrenJSON and the
// space lookup with a fixed key.
func childrenAPI(t *testing.T, childrenJSON string) (*int, http.HandlerFunc) {
	t.Helper()

	spaceLookups := 0
	return &spaceLookups, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/direct-children"):
			_, _ = w.Write([]byte(childrenJSON))
		case strings.Contains(r.URL.Path, "/spaces/"):
			spaceLookups++
			_, _ = w.Write([]byte(`{"id":"789","key":"DEV"}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}
}

func TestChildrenListsOnlyPagesWithRoundTrippableURLs(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	lookups, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"Child One","status":"current","spaceId":"789"},
		{"id":"12","type":"whiteboard","title":"A Whiteboard","spaceId":"789"},
		{"id":"13","type":"page","title":"Child Two","status":"archived","spaceId":"789"}
	],"_links":{}}`)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}

	if strings.Contains(output, "A Whiteboard") {
		t.Errorf("output = %q, want non-page children filtered out", output)
	}
	for _, want := range []string{
		"Child One", "ID 11", "current",
		"Child Two", "ID 13", "archived",
		testSite + "/spaces/DEV/pages/11",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	}
	// One lookup covers every child: they all live in the parent's space.
	if *lookups != 1 {
		t.Errorf("space lookups = %d, want 1 for a single space", *lookups)
	}
}

func TestChildrenReportsTruncationWithoutACount(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	_, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"One","status":"current","spaceId":"789"},
		{"id":"12","type":"page","title":"Two","status":"current","spaceId":"789"}
	],"_links":{}}`)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 1)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}
	if !strings.Contains(output, "--limit") {
		t.Errorf("output = %q, want a truncation notice pointing at --limit", output)
	}
	// v2 pages by cursor and reports no total, so no count is claimed.
	if strings.Contains(output, "1 more") {
		t.Errorf("output = %q, want no invented remaining count", output)
	}
	if strings.Contains(output, "Two") {
		t.Errorf("output = %q, want only the first child at --limit 1", output)
	}
}

func TestChildrenAcceptsABarePageID(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	var gotPath string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/direct-children") {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"results":[],"_links":{}}`))
			return
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
	})

	if _, err := runChildrenCmd(t, "123456", 100); err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}
	if gotPath != "/wiki/api/v2/pages/123456/direct-children" {
		t.Errorf("path = %q, want the page id from the bare argument", gotPath)
	}
}

func TestChildrenJSONOutput(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "json")
	seedProfile(t, "example", testSite)

	_, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"Child One","status":"current","spaceId":"789"}
	],"_links":{}}`)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}

	var got struct {
		ChildPages []struct {
			Title  string `json:"title"`
			ID     string `json:"id"`
			Status string `json:"status"`
			URL    string `json:"url"`
		} `json:"child_pages"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
	}
	if len(got.ChildPages) != 1 || got.ChildPages[0].ID != "11" {
		t.Fatalf("child_pages = %+v, want the single child", got.ChildPages)
	}
	if got.ChildPages[0].URL != testSite+"/spaces/DEV/pages/11" {
		t.Errorf("url = %q, want the space-key form", got.ChildPages[0].URL)
	}
}

func TestChildrenWithNoChildren(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	_, handler := childrenAPI(t, `{"results":[],"_links":{}}`)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}
	if !strings.Contains(output, "No child pages.") {
		t.Errorf("output = %q, want it to say there are none", output)
	}
}

func TestChildrenRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an invalid --limit")
	})

	if _, err := runChildrenCmd(t, testPageURL, 0); err == nil {
		t.Error("runChildren() error = nil for --limit 0, want an error")
	}
}
