package jira

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/atlassian"
)

// newTestClient starts an httptest server with the given handler and
// returns a Client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := New(srv.URL, "a@example.com", "api-token")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestBrowseURLIsUnderTheSite(t *testing.T) {
	for _, site := range []string{"https://example.atlassian.net", "https://example.atlassian.net/"} {
		client, err := New(site, "a@example.com", "t")
		if err != nil {
			t.Fatalf("New(%q) error = %v", site, err)
		}
		if got, want := client.BrowseURL("KAN-1"), "https://example.atlassian.net/browse/KAN-1"; got != want {
			t.Errorf("New(%q).BrowseURL() = %q, want %q", site, got, want)
		}
	}
}

func TestCurrentUserAsksJira(t *testing.T) {
	var gotMethod, gotPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = fmt.Fprint(w, `{"accountId":"acc-1","emailAddress":"a@example.com","displayName":"Ada Lovelace"}`)
	})

	user, err := client.CurrentUser(t.Context())
	if err != nil {
		t.Fatalf("CurrentUser() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/rest/api/3/myself" {
		t.Errorf("request = %s %s, want GET /rest/api/3/myself", gotMethod, gotPath)
	}
	want := User{AccountID: "acc-1", Email: "a@example.com", DisplayName: "Ada Lovelace"}
	if user != want {
		t.Errorf("CurrentUser() = %+v, want %+v", user, want)
	}
}

func TestGetIssueRequestsEveryFieldWithItsName(t *testing.T) {
	var gotMethod, gotPath, gotFields, gotExpand string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotFields, gotExpand = r.URL.Query().Get("fields"), r.URL.Query().Get("expand")
		_, _ = fmt.Fprint(w, `{"key":"KAN-1","fields":{"summary":"S"},"names":{"summary":"Summary"}}`)
	})

	issue, err := client.GetIssue(t.Context(), "KAN-1")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/rest/api/3/issue/KAN-1" {
		t.Errorf("request = %s %s, want GET /rest/api/3/issue/KAN-1", gotMethod, gotPath)
	}
	if gotFields != "*all,-comment,-worklog" || gotExpand != "names" {
		t.Errorf("query fields=%q expand=%q, want fields=*all,-comment,-worklog expand=names", gotFields, gotExpand)
	}
	if issue.Key != "KAN-1" || issue.Fields.Summary != "S" || issue.Names["summary"] != "Summary" {
		t.Errorf("GetIssue() = %+v, want the key, summary and names decoded", issue)
	}
}

func TestAttachmentsAsksForTheAttachmentFieldOnly(t *testing.T) {
	var gotMethod, gotPath, gotFields string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotFields = r.Method, r.URL.Path, r.URL.Query().Get("fields")
		_, _ = fmt.Fprint(w, `{"key":"KAN-1","fields":{"attachment":[
			{"id":"10001","filename":"screen.png","mimeType":"image/png","size":1234,"content":"https://example.atlassian.net/rest/api/3/attachment/content/10001"},
			{"id":"10002","filename":"spec.pdf","mimeType":"application/pdf","size":9}
		]}}`)
	})

	got, err := client.Attachments(t.Context(), "KAN-1")
	if err != nil {
		t.Fatalf("Attachments() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/rest/api/3/issue/KAN-1" || gotFields != "attachment" {
		t.Errorf("request = %s %s fields=%q, want GET /rest/api/3/issue/KAN-1 fields=attachment", gotMethod, gotPath, gotFields)
	}
	want := []Attachment{
		{ID: "10001", Filename: "screen.png", MimeType: "image/png", Size: 1234},
		{ID: "10002", Filename: "spec.pdf", MimeType: "application/pdf", Size: 9},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Attachments() = %+v, want %+v", got, want)
	}
}

func TestDownloadAttachmentFetchesTheContentEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = fmt.Fprint(w, "file bytes")
	})

	var buf strings.Builder
	n, err := client.DownloadAttachment(t.Context(), "10001", &buf)
	if err != nil {
		t.Fatalf("DownloadAttachment() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/rest/api/3/attachment/content/10001" {
		t.Errorf("request = %s %s, want GET /rest/api/3/attachment/content/10001", gotMethod, gotPath)
	}
	if buf.String() != "file bytes" || n != int64(len("file bytes")) {
		t.Errorf("DownloadAttachment() wrote %q (%d bytes), want the response body", buf.String(), n)
	}
}

func TestGetIssueDecodesTheMetadata(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"key":"KAN-2","fields":{
			"summary":"Fix it",
			"issuetype":{"name":"Bug"},
			"status":{"name":"In Progress"},
			"priority":{"name":"High"},
			"assignee":{"displayName":"Ada"},
			"reporter":null,
			"labels":["a","b"],
			"parent":{"key":"KAN-0","fields":{"summary":"Epic"}},
			"subtasks":[{"key":"KAN-3","fields":{"summary":"Sub","status":{"name":"Done"}}}],
			"issuelinks":[
				{"type":{"inward":"is blocked by","outward":"blocks"},"outwardIssue":{"key":"KAN-4","fields":{"summary":"Out","status":{"name":"Open"}}}},
				{"type":{"inward":"is blocked by","outward":"blocks"},"inwardIssue":{"key":"KAN-5","fields":{"summary":"In","status":{"name":"Done"}}}}
			],
			"created":"2025-01-02T03:04:05.000+0000",
			"updated":"2025-01-03T03:04:05.000+0000",
			"attachment":[{"filename":"a.png","mimeType":"image/png","size":123}]
		}}`)
	})

	issue, err := client.GetIssue(t.Context(), "KAN-2")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	f := issue.Fields
	if f.IssueType.Name != "Bug" || f.Status.Name != "In Progress" || f.Priority == nil || f.Priority.Name != "High" {
		t.Errorf("type/status/priority = %+v %+v %+v", f.IssueType, f.Status, f.Priority)
	}
	if f.Assignee == nil || f.Assignee.DisplayName != "Ada" || f.Reporter != nil {
		t.Errorf("assignee/reporter = %+v %+v, want Ada and nil", f.Assignee, f.Reporter)
	}
	if !slices.Equal(f.Labels, []string{"a", "b"}) {
		t.Errorf("labels = %v", f.Labels)
	}
	if f.Parent == nil || f.Parent.Key != "KAN-0" || f.Parent.Fields.Summary != "Epic" {
		t.Errorf("parent = %+v", f.Parent)
	}
	if len(f.Subtasks) != 1 || f.Subtasks[0].Fields.Status.Name != "Done" {
		t.Errorf("subtasks = %+v", f.Subtasks)
	}
	if len(f.IssueLinks) != 2 || f.IssueLinks[0].OutwardIssue == nil || f.IssueLinks[1].InwardIssue == nil {
		t.Fatalf("issuelinks = %+v", f.IssueLinks)
	}
	if got := f.IssueLinks[1].InwardIssue.Fields.Status.Name; got != "Done" {
		t.Errorf("inward issue status = %q, want Done", got)
	}
	if f.Created != "2025-01-02T03:04:05.000+0000" || f.Updated != "2025-01-03T03:04:05.000+0000" {
		t.Errorf("created/updated = %q %q", f.Created, f.Updated)
	}
	if want := []Attachment{{Filename: "a.png", MimeType: "image/png", Size: 123}}; !slices.Equal(f.Attachments, want) {
		t.Errorf("attachments = %+v, want %+v", f.Attachments, want)
	}
}

// TestGetIssueKeepsADFValuesVerbatim pins the bytes the default read mode
// writes: the value has to be what the API sent, whitespace and escapes
// included, so that a later command can send it back unchanged.
func TestGetIssueKeepsADFValuesVerbatim(t *testing.T) {
	const description = `{"type" : "doc", "version":1, "content":[{"type":"paragraph","content":[{"type":"text","text":"caf\u00e9 \/ café"}]}]}`
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"key":"KAN-1","fields":{"description": `+description+`}}`)
	})

	issue, err := client.GetIssue(t.Context(), "KAN-1")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	fields := issue.ADFFields()
	if len(fields) != 1 {
		t.Fatalf("ADFFields() = %d fields, want 1", len(fields))
	}
	if got := string(fields[0].Value); got != description {
		t.Errorf("description bytes =\n%s\nwant\n%s", got, description)
	}
}

func TestADFFieldsSelectsDocumentsInFieldOrder(t *testing.T) {
	const doc = `{"type":"doc","version":1,"content":[]}`
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"key":"KAN-1","fields":{
			"customfield_10010":`+doc+`,
			"summary":"not a document",
			"customfield_10000":{"type":"option","value":"x"},
			"customfield_9999":`+doc+`,
			"customfield_10001":null,
			"customfield_10002":[`+doc+`],
			"environment":`+doc+`,
			"description":`+doc+`
		}}`)
	})

	issue, err := client.GetIssue(t.Context(), "KAN-1")
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}

	var ids []string
	for _, f := range issue.ADFFields() {
		ids = append(ids, f.ID)
	}
	// customfield_9999 sorts before customfield_10010 by number, after it as
	// a string: the order is by field ID as a number would read it.
	if want := []string{"description", "environment", "customfield_9999", "customfield_10010"}; !slices.Equal(ids, want) {
		t.Errorf("ADFFields() IDs = %v, want %v", ids, want)
	}
}

func TestAPIErrorSurfacesJiraMessages(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantContains []string
	}{
		{
			name:         "missing issue",
			status:       http.StatusNotFound,
			body:         `{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`,
			wantContains: []string{"404", "Issue does not exist or you do not have permission to see it."},
		},
		{
			name:         "field errors in field order",
			status:       http.StatusBadRequest,
			body:         `{"errorMessages":["Top"],"errors":{"zeta":"last","alpha":"first"}}`,
			wantContains: []string{"400", "Top; alpha: first; zeta: last"},
		},
		{
			name:         "unparseable body falls back to the raw text",
			status:       http.StatusBadGateway,
			body:         "<html>gateway said no</html>",
			wantContains: []string{"502", "gateway said no"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			})

			_, err := client.GetIssue(t.Context(), "KAN-1")
			if err == nil {
				t.Fatal("GetIssue() error = nil, want an error")
			}
			apiErr, ok := errors.AsType[*atlassian.APIError](err)
			if !ok {
				t.Fatalf("GetIssue() error = %v, want an *APIError", err)
			}
			if apiErr.Status != tt.status {
				t.Errorf("Status = %d, want %d", apiErr.Status, tt.status)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

// searchPage renders a search response holding the issues KAN-<from> to
// KAN-<to>, with next as its nextPageToken when non-empty.
func searchPage(from, to int, next string) string {
	issues := make([]string, 0, to-from+1)
	for n := from; n <= to; n++ {
		issues = append(issues, fmt.Sprintf(`{"id":"%d","key":"KAN-%d","fields":{"summary":"S%d"}}`, 10000+n, n, n))
	}
	page := `{"issues":[` + strings.Join(issues, ",") + `]`
	if next != "" {
		page += `,"nextPageToken":"` + next + `"`
	}
	return page + `}`
}

func TestSearchSizesPagesToTheLimit(t *testing.T) {
	type request struct{ jql, fields, maxResults, token string }
	var got []request
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("request = %s %s, want GET /rest/api/3/search/jql", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		got = append(got, request{q.Get("jql"), q.Get("fields"), q.Get("maxResults"), q.Get("nextPageToken")})
		// The first page comes back full, so the second asks only for what
		// is left plus the one issue that tells a cut-short listing apart.
		if q.Get("nextPageToken") == "" {
			_, _ = fmt.Fprint(w, searchPage(1, 100, "page-2"))
			return
		}
		_, _ = fmt.Fprint(w, searchPage(101, 151, ""))
	})

	issues, hasMore, err := client.Search(t.Context(), "project = KAN", 150)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	const fields = "summary,issuetype,status,assignee,updated"
	want := []request{
		{"project = KAN", fields, "100", ""},
		{"project = KAN", fields, "51", "page-2"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("requests = %+v, want %+v", got, want)
	}
	if len(issues) != 150 || !hasMore {
		t.Fatalf("Search() = %d issues, hasMore %t; want 150 issues and hasMore", len(issues), hasMore)
	}
	if issues[0].Key != "KAN-1" || issues[0].Fields.Summary != "S1" || issues[149].Key != "KAN-150" {
		t.Errorf("Search() issues run %s..%s, want KAN-1..KAN-150 decoded in order", issues[0].Key, issues[149].Key)
	}
}

// A page that ends exactly at the limit with a token still attached says
// nothing about whether more issues match: only an issue past the limit
// does, so the token is followed and an empty last page reports none.
func TestSearchReportsMoreOnlyWhenAnIssuePastTheLimitArrives(t *testing.T) {
	var maxResults []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		maxResults = append(maxResults, r.URL.Query().Get("maxResults"))
		if r.URL.Query().Get("nextPageToken") == "" {
			// Jira may return fewer issues than asked for.
			_, _ = fmt.Fprint(w, searchPage(1, 2, "page-2"))
			return
		}
		_, _ = fmt.Fprint(w, `{"issues":[]}`)
	})

	issues, hasMore, err := client.Search(t.Context(), "project = KAN", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if want := []string{"3", "1"}; !slices.Equal(maxResults, want) {
		t.Errorf("maxResults per request = %v, want %v", maxResults, want)
	}
	if len(issues) != 2 || hasMore {
		t.Errorf("Search() = %d issues, hasMore %t; want 2 issues and no more", len(issues), hasMore)
	}
}

// commentsServer serves stored comments the way Jira pages them: from startAt,
// at most maxResults and at most pageSize at a time, reporting total.
type commentsServer struct {
	stored   int
	total    int
	pageSize int
	requests []string
}

func (s *commentsServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.requests = append(s.requests, r.URL.Path+"?"+r.URL.RawQuery)
	startAt, _ := strconv.Atoi(q.Get("startAt"))
	maxResults, _ := strconv.Atoi(q.Get("maxResults"))

	var comments []string
	for i := startAt; i < s.stored && len(comments) < min(maxResults, s.pageSize); i++ {
		comments = append(comments, fmt.Sprintf(`{"id":"%d","body":{"type":"doc"},"created":"c","updated":"c"}`, i))
	}
	_, _ = fmt.Fprintf(w, `{"startAt":%d,"maxResults":%d,"total":%d,"comments":[%s]}`,
		startAt, maxResults, s.total, strings.Join(comments, ","))
}

func commentIDs(comments []Comment) []string {
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestCommentsFollowsTheOffsetPagination(t *testing.T) {
	const path = "/rest/api/3/issue/KAN-1/comment"
	tests := []struct {
		name         string
		server       commentsServer
		limit        int
		wantIDs      []string
		wantTotal    int
		wantRequests []string
	}{
		{
			name:      "every page until total is reached",
			server:    commentsServer{stored: 3, total: 3, pageSize: 2},
			limit:     25,
			wantIDs:   []string{"0", "1", "2"},
			wantTotal: 3,
			wantRequests: []string{
				path + "?maxResults=25&orderBy=created&startAt=0",
				path + "?maxResults=23&orderBy=created&startAt=2",
			},
		},
		{
			name:      "stops at the limit",
			server:    commentsServer{stored: 5, total: 5, pageSize: 2},
			limit:     3,
			wantIDs:   []string{"0", "1", "2"},
			wantTotal: 5,
			wantRequests: []string{
				path + "?maxResults=3&orderBy=created&startAt=0",
				path + "?maxResults=1&orderBy=created&startAt=2",
			},
		},
		{
			name:         "asks for no more than the page cap",
			server:       commentsServer{stored: 1, total: 1, pageSize: 1000},
			limit:        1000,
			wantIDs:      []string{"0"},
			wantTotal:    1,
			wantRequests: []string{path + "?maxResults=100&orderBy=created&startAt=0"},
		},
		{
			name:      "an empty page ends an overstated total",
			server:    commentsServer{stored: 2, total: 10, pageSize: 2},
			limit:     25,
			wantIDs:   []string{"0", "1"},
			wantTotal: 10,
			wantRequests: []string{
				path + "?maxResults=25&orderBy=created&startAt=0",
				path + "?maxResults=23&orderBy=created&startAt=2",
			},
		},
		{
			// A missing total says nothing about how many remain, so it
			// must not end the listing after the first page.
			name:      "a missing total does not end the listing",
			server:    commentsServer{stored: 3, total: 0, pageSize: 2},
			limit:     25,
			wantIDs:   []string{"0", "1", "2"},
			wantTotal: 0,
			wantRequests: []string{
				path + "?maxResults=25&orderBy=created&startAt=0",
				path + "?maxResults=23&orderBy=created&startAt=2",
				path + "?maxResults=22&orderBy=created&startAt=3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, tt.server.handle)

			comments, total, err := client.Comments(t.Context(), "KAN-1", tt.limit)
			if err != nil {
				t.Fatalf("Comments() error = %v", err)
			}
			if got := commentIDs(comments); !slices.Equal(got, tt.wantIDs) {
				t.Errorf("comment IDs = %v, want %v", got, tt.wantIDs)
			}
			if total != tt.wantTotal {
				t.Errorf("total = %d, want %d", total, tt.wantTotal)
			}
			if !slices.Equal(tt.server.requests, tt.wantRequests) {
				t.Errorf("requests =\n%s\nwant\n%s", strings.Join(tt.server.requests, "\n"), strings.Join(tt.wantRequests, "\n"))
			}
		})
	}
}

func TestCommentsKeepsTheAPIFields(t *testing.T) {
	const body = `{"type":"doc","version":1,"content":[]}`
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"total":2,"comments":[
			{"id":"10","author":{"displayName":"Ada"},"body":%s,"created":"2026-01-01T00:00:00.000+0000","updated":"2026-01-02T00:00:00.000+0000"},
			{"id":"11","body":%s,"created":"c","updated":"c"}]}`, body, body)
	})

	comments, _, err := client.Comments(t.Context(), "KAN-1", 25)
	if err != nil {
		t.Fatalf("Comments() error = %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}
	first := comments[0]
	if first.Author == nil || first.Author.DisplayName != "Ada" ||
		first.Created != "2026-01-01T00:00:00.000+0000" || first.Updated != "2026-01-02T00:00:00.000+0000" {
		t.Errorf("first comment = %+v", first)
	}
	if string(first.Body) != body {
		t.Errorf("Body = %s, want the API's bytes %s", first.Body, body)
	}
	if comments[1].Author != nil {
		t.Errorf("Author of a comment without one = %+v, want nil", comments[1].Author)
	}
}
