package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func runChildrenCmd(t *testing.T, arg string, limit int, extra ...string) (string, error) {
	t.Helper()
	return runLimitCmd(t, "children", arg, limit, extra...)
}

// childrenAPI answers the direct-children listing with childrenJSON and the
// parent page lookup with a page whose web link is parentWebUI, counting the
// parent lookups.
//
// Both paths are matched exactly. A looser match silently answers requests
// the command should not be making at all — that is how the command shipped
// asking for /api/v2/spaces/ with no ID, and how its tests kept passing.
func childrenAPI(t *testing.T, childrenJSON, parentWebUI string) (*int, http.HandlerFunc) {
	t.Helper()

	// The page testPageURL and a bare "123456" both refer to.
	const parentPath = "/wiki/api/v2/pages/123456"

	parentLookups := 0
	return &parentLookups, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case parentPath + "/direct-children":
			_, _ = w.Write([]byte(childrenJSON))
		case parentPath:
			parentLookups++
			_, _ = w.Write([]byte(pageResponse(t, "", parentWebUI)))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}
}

func TestChildrenListsOnlyPagesWithSpaceKeyURLs(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	lookups, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"Child One","status":"current"},
		{"id":"12","type":"whiteboard","title":"A Whiteboard"},
		{"id":"13","type":"page","title":"Child Two","status":"archived"}
	],"_links":{}}`, testPageWebUI)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}

	if strings.Contains(output, "A Whiteboard") {
		t.Errorf("output = %q, want non-page children filtered out", output)
	}
	// Pinning the URL exactly is what catches the empty space key. Feeding
	// both URL forms back through pageref.Parse is TestChildPageURL's job.
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
		t.Errorf("parent lookups = %d, want 1 for the whole listing", *lookups)
	}
}

func TestChildrenReportsTruncationWithoutACount(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	_, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"One","status":"current"},
		{"id":"12","type":"page","title":"Two","status":"current"}
	],"_links":{}}`, testPageWebUI)
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
	seedProfile(t, "example", testSite)

	_, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"Child One","status":"current"}
	],"_links":{}}`, testPageWebUI)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100, "--format", "json")
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
	seedProfile(t, "example", testSite)

	lookups, handler := childrenAPI(t, `{"results":[],"_links":{}}`, testPageWebUI)
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}
	if !strings.Contains(output, "No child pages.") {
		t.Errorf("output = %q, want it to say there are none", output)
	}
	// With no children there is no URL to build, so the space key nobody
	// needs is not worth a request.
	if *lookups != 0 {
		t.Errorf("parent lookups = %d, want 0 for a leaf page", *lookups)
	}
}

func TestChildrenFallsBackToThePageIDFormWithoutASpaceKey(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// The real API always returns a web link for a page, so this degraded
	// path is only ever exercised here. It has to still produce a URL that
	// reaches the child: the empty space key it replaces was a 404.
	_, handler := childrenAPI(t, `{"results":[
		{"id":"11","type":"page","title":"Child One","status":"current"}
	],"_links":{}}`, "")
	startAPI(t, handler)

	output, err := runChildrenCmd(t, testPageURL, 100)
	if err != nil {
		t.Fatalf("runChildren() error = %v", err)
	}
	if want := testSite + "/pages/viewpage.action?pageId=11"; !strings.Contains(output, want) {
		t.Errorf("output = %q, want it to contain the page-id form %q", output, want)
	}
}

func TestChildrenRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an invalid --limit")
	})

	if _, err := runChildrenCmd(t, testPageURL, 0); err == nil {
		t.Error("runChildren() error = nil for --limit 0, want an error")
	}
}
