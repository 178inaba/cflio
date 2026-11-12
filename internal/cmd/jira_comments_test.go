package cmd

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const testCommentsPath = "/rest/api/3/issue/KAN-1/comment"

// paragraphADF is a one-paragraph ADF document holding text.
func paragraphADF(text string) string {
	return `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
}

// mediaADF is an ADF document holding one image, which the converter cannot
// represent.
const mediaADF = `{"type":"doc","version":1,"content":[{"type":"mediaSingle","content":[` +
	`{"type":"media","attrs":{"id":"m","type":"file","collection":"c"}}]}]}`

// testComment renders one comment as Jira returns it.
func testComment(id, author, body, created, updated string) string {
	authorField := ""
	if author != "" {
		authorField = `"author":{"displayName":"` + author + `"},`
	}
	return fmt.Sprintf(`{"id":%q,%s"body":%s,"created":%q,"updated":%q}`, id, authorField, body, created, updated)
}

// numberedComments is n comments, numbered from zero, each saying its number.
func numberedComments(n int) []string {
	comments := make([]string, n)
	for i := range n {
		id := strconv.Itoa(i)
		comments[i] = testComment(id, "User "+id, paragraphADF("Comment "+id), "2026-01-01", "2026-01-01")
	}
	return comments
}

// serveComments pages stored the way Jira does — from startAt, at most
// maxResults and at most pageSize at a time — reporting total, and records
// each request's path and query.
func serveComments(t *testing.T, stored []string, total, pageSize int) *[]string {
	t.Helper()

	var requests []string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
		}
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		q := r.URL.Query()
		startAt, _ := strconv.Atoi(q.Get("startAt"))
		maxResults, _ := strconv.Atoi(q.Get("maxResults"))
		end := min(len(stored), startAt+min(maxResults, pageSize))
		var page []string
		if startAt < end {
			page = stored[startAt:end]
		}
		_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"comments":[%s]}`,
			startAt, maxResults, total, strings.Join(page, ","))
	})
	return &requests
}

func TestJiraCommentsListAcceptsAKeyAndAnIssueURL(t *testing.T) {
	for _, arg := range []string{"KAN-1", testSite + "/browse/KAN-1"} {
		t.Run(arg, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			requests := serveComments(t, numberedComments(1), 1, 100)

			if _, err := runCLI(t, "jira", "comments", "list", arg); err != nil {
				t.Fatalf("jira comments list error = %v", err)
			}

			want := []string{testCommentsPath + "?maxResults=25&orderBy=created&startAt=0"}
			if !slices.Equal(*requests, want) {
				t.Errorf("requests = %v, want %v", *requests, want)
			}
		})
	}
}

func TestJiraCommentsListPrintsEveryPageInOrder(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests := serveComments(t, numberedComments(3), 3, 2)

	run, err := runCLI(t, "jira", "comments", "list", "KAN-1")
	if err != nil {
		t.Fatalf("jira comments list error = %v", err)
	}

	want := []string{
		testCommentsPath + "?maxResults=25&orderBy=created&startAt=0",
		testCommentsPath + "?maxResults=23&orderBy=created&startAt=2",
	}
	if !slices.Equal(*requests, want) {
		t.Errorf("requests = %v, want %v", *requests, want)
	}
	wantOut := "- User 0 at 2026-01-01\n  Comment 0\n" +
		"- User 1 at 2026-01-01\n  Comment 1\n" +
		"- User 2 at 2026-01-01\n  Comment 2\n"
	if run.stdout != wantOut {
		t.Errorf("stdout =\n%s\nwant\n%s", run.stdout, wantOut)
	}
}

func TestJiraCommentsListStopsAtTheLimitAndSaysSo(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests := serveComments(t, numberedComments(5), 5, 2)

	run, err := runLimitCmd(t, "jira comments list", "KAN-1", 3)
	if err != nil {
		t.Fatalf("jira comments list error = %v", err)
	}

	if len(*requests) != 2 {
		t.Errorf("requests = %v, want 2", *requests)
	}
	if strings.Contains(run.stdout, "Comment 3") {
		t.Errorf("stdout = %q, want it to stop at the third comment", run.stdout)
	}
	if want := "\nShowing 3 of 5 comments; raise --limit to fetch the rest.\n"; !strings.HasSuffix(run.stdout, want) {
		t.Errorf("stdout = %q, want it to end with %q", run.stdout, want)
	}
}

// TestJiraCommentsListTrustsAnEmptyPageOverTotal covers a server whose total
// promises more than it then serves: the empty page is the answer, so the
// listing ends without asking again, and since raising --limit would fetch
// nothing more, it carries no notice.
func TestJiraCommentsListTrustsAnEmptyPageOverTotal(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests := serveComments(t, numberedComments(2), 10, 2)

	run, err := runCLI(t, "jira", "comments", "list", "KAN-1")
	if err != nil {
		t.Fatalf("jira comments list error = %v", err)
	}

	if len(*requests) != 2 {
		t.Errorf("requests = %v, want the first page and one empty page", *requests)
	}
	if !strings.Contains(run.stdout, "Comment 1") {
		t.Errorf("stdout = %q, want both comments it received", run.stdout)
	}
	if strings.Contains(run.stdout, "--limit") {
		t.Errorf("stdout = %q, want no limit notice", run.stdout)
	}
}

func TestJiraCommentsListJSON(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	const richBody = `{"type":"doc","version":1,"content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"Ask "},{"type":"mention","attrs":{"id":"acc-1","text":"@Ada"}}]},` +
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]}]}]}`
	serveComments(t, []string{
		testComment("10", "Grace", richBody, "2026-01-01T00:00:00.000+0000", "2026-01-02T00:00:00.000+0000"),
		testComment("11", "", paragraphADF("Unedited"), "2026-01-03T00:00:00.000+0000", "2026-01-03T00:00:00.000+0000"),
	}, 2, 100)

	run, err := runCLI(t, "jira", "comments", "list", "KAN-1", "--format", "json")
	if err != nil {
		t.Fatalf("jira comments list error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, run.stdout)
	}
	want := map[string]any{
		"comments": []any{
			map[string]any{
				"id":         "10",
				"author":     "Grace",
				"created_at": "2026-01-01T00:00:00.000+0000",
				"updated_at": "2026-01-02T00:00:00.000+0000",
				"body":       "Ask @Ada\n\n- first",
			},
			// No author, and updated_at left out where it equals created_at.
			map[string]any{
				"id":         "11",
				"created_at": "2026-01-03T00:00:00.000+0000",
				"body":       "Unedited",
			},
		},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("JSON =\n%v\nwant\n%v", got, want)
	}
}

func TestJiraCommentsListShowsAnAnonymousAuthorAndAnEdit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	serveComments(t, []string{testComment("10", "", paragraphADF("Hi"), "2026-01-01", "2026-01-02")}, 1, 100)

	run, err := runCLI(t, "jira", "comments", "list", "KAN-1")
	if err != nil {
		t.Fatalf("jira comments list error = %v", err)
	}

	if want := "- Anonymous at 2026-01-01 (updated 2026-01-02)\n  Hi\n"; run.stdout != want {
		t.Errorf("stdout = %q, want %q", run.stdout, want)
	}
}

func TestJiraCommentsListReportsLossesOnceForTheListing(t *testing.T) {
	stored := []string{
		testComment("10", "Ada", mediaADF, "2026-01-01", "2026-01-01"),
		testComment("11", "Ada", mediaADF, "2026-01-02", "2026-01-02"),
	}

	t.Run("md", func(t *testing.T) {
		isolateConfig(t)
		seedProfile(t, "example", testSite)
		serveComments(t, stored, 2, 100)

		run, err := runCLI(t, "jira", "comments", "list", "KAN-1")
		if err != nil {
			t.Fatalf("jira comments list error = %v", err)
		}
		if got := strings.Count(run.stdout, "Degraded:"); got != 1 {
			t.Errorf("stdout has %d Degraded lines, want 1:\n%s", got, run.stdout)
		}
		if !strings.HasSuffix(run.stdout, "\nDegraded: 2 (mediaSingle)\n") {
			t.Errorf("stdout = %q, want it to end with the Degraded line", run.stdout)
		}
	})

	t.Run("json", func(t *testing.T) {
		isolateConfig(t)
		seedProfile(t, "example", testSite)
		serveComments(t, stored, 2, 100)

		run, err := runCLI(t, "jira", "comments", "list", "KAN-1", "--format", "json")
		if err != nil {
			t.Fatalf("jira comments list error = %v", err)
		}
		var got struct {
			Unsupported      []string `json:"unsupported"`
			UnsupportedCount int      `json:"unsupported_count"`
		}
		if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, run.stdout)
		}
		if got.UnsupportedCount != 2 || !slices.Equal(got.Unsupported, []string{"mediaSingle"}) {
			t.Errorf("unsupported = %v (count %d), want [mediaSingle] (count 2)", got.Unsupported, got.UnsupportedCount)
		}
	})
}

func TestJiraCommentsListWithNoComments(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{format: "md", want: "No comments.\n"},
		{format: "json", want: "{\n  \"comments\": []\n}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			serveComments(t, nil, 0, 100)

			run, err := runCLI(t, "jira", "comments", "list", "KAN-1", "--format", tt.format)
			if err != nil {
				t.Fatalf("jira comments list error = %v", err)
			}
			if run.stdout != tt.want {
				t.Errorf("stdout = %q, want %q", run.stdout, tt.want)
			}
		})
	}
}

func TestJiraCommentsListRejectsAnInvalidLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, neverCalled(t, "despite an invalid --limit"))

	if _, err := runLimitCmd(t, "jira comments list", "KAN-1", 0); err == nil {
		t.Error("jira comments list error = nil for --limit=0, want an error")
	}
}
