package cmd

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

const testSearchPath = "/rest/api/3/search/jql"

func runJiraSearchCmd(t *testing.T, jql string, limit int, extra ...string) (cliRun, error) {
	t.Helper()
	return runLimitCmd(t, "jira search", jql, limit, extra...)
}

// searchIssue renders one issue of a search response. An empty assignee
// renders as null, the way Jira sends an unassigned issue.
func searchIssue(key, summary, assignee string) string {
	assigneeJSON := "null"
	if assignee != "" {
		assigneeJSON = `{"displayName":"` + assignee + `"}`
	}
	return `{"id":"1","key":"` + key + `","fields":{"summary":"` + summary + `",` +
		`"issuetype":{"name":"Bug"},"status":{"name":"In Progress"},"assignee":` + assigneeJSON + `,` +
		`"updated":"2025-01-03T03:04:05.000+0000"}}`
}

// serveSearch answers each search request with the next of pages in turn,
// failing the test on anything but a GET to the search path, and records the
// query of every request.
func serveSearch(t *testing.T, pages ...string) *[]url.Values {
	t.Helper()

	var queries []url.Values
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != testSearchPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, testSearchPath)
		}
		queries = append(queries, r.URL.Query())
		if len(queries) > len(pages) {
			t.Errorf("request %d, want at most %d", len(queries), len(pages))
			return
		}
		_, _ = io.WriteString(w, pages[len(queries)-1])
	})
	return &queries
}

func TestJiraSearchSendsTheQueryWithTheListedFields(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	queries := serveSearch(t, `{"issues":[`+searchIssue("KAN-1", "Login fails", "Ada Lovelace")+`],"isLast":true}`)

	run, err := runJiraSearchCmd(t, "project = KAN", 20)
	if err != nil {
		t.Fatalf("jira search error = %v", err)
	}

	if len(*queries) != 1 {
		t.Fatalf("requests = %d, want 1", len(*queries))
	}
	q := (*queries)[0]
	if got := q.Get("jql"); got != "project = KAN" {
		t.Errorf("jql = %q, want it passed as typed", got)
	}
	if got := q.Get("fields"); got != "summary,issuetype,status,assignee,updated" {
		t.Errorf("fields = %q, want summary,issuetype,status,assignee,updated", got)
	}
	want := "- **KAN-1** Login fails (Bug, In Progress, Ada Lovelace, updated 2025-01-03T03:04:05.000+0000)\n" +
		"  " + testSite + "/browse/KAN-1\n"
	if run.stdout != want {
		t.Errorf("output = %q, want %q", run.stdout, want)
	}
}

func TestJiraSearchFollowsTheNextPageToken(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	queries := serveSearch(t,
		`{"issues":[`+searchIssue("KAN-1", "First", "")+`],"nextPageToken":"page-2","isLast":false}`,
		// The last page as the schema has it: the token present and null.
		`{"issues":[`+searchIssue("KAN-2", "Second", "")+`],"nextPageToken":null,"isLast":true}`,
	)

	run, err := runJiraSearchCmd(t, "project = KAN", 20)
	if err != nil {
		t.Fatalf("jira search error = %v", err)
	}

	if len(*queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(*queries))
	}
	if (*queries)[0].Has("nextPageToken") {
		t.Errorf("first request nextPageToken = %q, want none", (*queries)[0].Get("nextPageToken"))
	}
	if got := (*queries)[1].Get("nextPageToken"); got != "page-2" {
		t.Errorf("second request nextPageToken = %q, want page-2", got)
	}
	for _, want := range []string{"**KAN-1** First", "**KAN-2** Second"} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
}

func TestJiraSearchReportsTruncationOnlyWhenMoreMatched(t *testing.T) {
	const notice = "More issues available; raise --limit to fetch them."

	tests := []struct {
		name       string
		matches    int
		wantNotice bool
	}{
		{name: "more matches than the limit", matches: 3, wantNotice: true},
		{name: "exactly the limit", matches: 2, wantNotice: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			issues := make([]string, 0, tt.matches)
			for n := 1; n <= tt.matches; n++ {
				issues = append(issues, searchIssue(fmt.Sprintf("KAN-%d", n), "S", ""))
			}
			serveSearch(t, `{"issues":[`+strings.Join(issues, ",")+`]}`)

			run, err := runJiraSearchCmd(t, "project = KAN", 2)
			if err != nil {
				t.Fatalf("jira search error = %v", err)
			}

			if got := strings.Count(run.stdout, "/browse/"); got != 2 {
				t.Errorf("output lists %d issues, want 2:\n%s", got, run.stdout)
			}
			if strings.Contains(run.stdout, "KAN-3") {
				t.Errorf("output = %q, want the issue past --limit left out", run.stdout)
			}
			if got := strings.Contains(run.stdout, notice); got != tt.wantNotice {
				t.Errorf("notice printed = %t, want %t:\n%s", got, tt.wantNotice, run.stdout)
			}
		})
	}
}

func TestJiraSearchJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveSearch(t, `{"issues":[`+
		searchIssue("KAN-1", "Assigned", "Ada Lovelace")+","+
		searchIssue("KAN-2", "Unassigned", "")+`]}`)

	run, err := runJiraSearchCmd(t, "project = KAN", 20, "--format", "json")
	if err != nil {
		t.Fatalf("jira search error = %v", err)
	}

	var got struct {
		Issues []map[string]string `json:"issues"`
		Notice *string             `json:"notice"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, run.stdout)
	}
	want := []map[string]string{
		{
			"key": "KAN-1", "summary": "Assigned", "type": "Bug", "status": "In Progress", "assignee": "Ada Lovelace",
			"updated_at": "2025-01-03T03:04:05.000+0000", "url": testSite + "/browse/KAN-1",
		},
		{
			"key": "KAN-2", "summary": "Unassigned", "type": "Bug", "status": "In Progress",
			"updated_at": "2025-01-03T03:04:05.000+0000", "url": testSite + "/browse/KAN-2",
		},
	}
	if !slices.EqualFunc(got.Issues, want, maps.Equal) {
		t.Errorf("issues = %v, want %v", got.Issues, want)
	}
	if got.Notice != nil {
		t.Errorf("notice = %q, want none", *got.Notice)
	}
}

func TestJiraSearchReportsJiraErrors(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	const message = "Unbounded JQL queries are not allowed here. Please add a search restriction to your query."
	startAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errorMessages":["`+message+`"],"errors":{}}`)
	})

	_, err := runJiraSearchCmd(t, "order by created desc", 20)
	if err == nil {
		t.Fatal("jira search error = nil, want an error")
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("error = %q, want it to contain %q", err, message)
	}
	if code, _ := describeFailure(err, defaultTimeout); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestJiraSearchRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an invalid --limit"))

	for _, limit := range []int{0, -1, maxLimit + 1} {
		if _, err := runJiraSearchCmd(t, "project = KAN", limit); err == nil {
			t.Errorf("jira search error = nil for --limit %d, want an error", limit)
		}
	}
}
