package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// newTestClient starts an httptest server with the given handler and
// returns a Client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := New(srv.URL+"/wiki", "a@example.com", "api-token")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestNewRejectsRelativeSiteURL(t *testing.T) {
	for _, site := range []string{"example.atlassian.net/wiki", "/wiki", ""} {
		if _, err := New(site, "a@example.com", "t"); err == nil {
			t.Errorf("New(%q) error = nil, want an error for a non-absolute site url", site)
		}
	}
}

func TestRequestsUseBasicAuthAndAcceptJSON(t *testing.T) {
	var gotAuth, gotAccept string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = fmt.Fprint(w, `{"accountId":"acc-1"}`)
	})

	if _, err := client.CurrentUser(t.Context()); err != nil {
		t.Fatalf("CurrentUser() error = %v", err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("a@example.com:api-token"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
}

func TestGetPageRequestsStorageFormatOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name       string
		withBody   bool
		wantFormat string
	}{
		{name: "with body", withBody: true, wantFormat: "storage"},
		{name: "without body", withBody: false, wantFormat: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotFormat string
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotFormat = r.URL.Query().Get("body-format")
				_, _ = fmt.Fprint(w, `{"id":"123","status":"current","title":"T","version":{"number":7}}`)
			})

			page, err := client.GetPage(t.Context(), "123", tt.withBody)
			if err != nil {
				t.Fatalf("GetPage() error = %v", err)
			}
			if gotPath != "/wiki/api/v2/pages/123" {
				t.Errorf("path = %q, want /wiki/api/v2/pages/123", gotPath)
			}
			if gotFormat != tt.wantFormat {
				t.Errorf("body-format = %q, want %q", gotFormat, tt.wantFormat)
			}
			if page.Version.Number != 7 {
				t.Errorf("version = %d, want 7", page.Version.Number)
			}
		})
	}
}

func TestGetPageReturnsTheStorageBodyVerbatim(t *testing.T) {
	// Angle brackets, ampersands and a Confluence macro: the characters a
	// converting client would mangle.
	body := `<p>a &amp; b &lt;c&gt;</p><ac:structured-macro ac:name="info"><ac:rich-text-body><p>hi</p></ac:rich-text-body></ac:structured-macro>`

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		payload, err := json.Marshal(map[string]any{
			"id":     "123",
			"body":   map[string]any{"storage": map[string]any{"representation": "storage", "value": body}},
			"_links": map[string]any{"webui": "/spaces/DEV/pages/123/Title"},
		})
		if err != nil {
			t.Errorf("Marshal() error = %v", err)
			return
		}
		_, _ = w.Write(payload)
	})

	page, err := client.GetPage(t.Context(), "123", true)
	if err != nil {
		t.Fatalf("GetPage() error = %v", err)
	}
	if page.Body.Storage.Value != body {
		t.Errorf("body = %q, want it byte-identical to %q", page.Body.Storage.Value, body)
	}
	if page.Links.WebUI != "/spaces/DEV/pages/123/Title" {
		t.Errorf("webui = %q, want the link from the response", page.Links.WebUI)
	}
}

func TestUpdatePageSendsTheRequiredFieldsUnescaped(t *testing.T) {
	body := `<p>a & b <c></p>`

	var gotMethod, gotPath, gotRaw string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			return
		}
		gotRaw = string(raw)
		_, _ = fmt.Fprint(w, `{"id":"123","version":{"number":8}}`)
	})

	req := NewUpdatePageRequest("123", "current", "Title", body, 8, "Updated via cflio")
	page, err := client.UpdatePage(t.Context(), req)
	if err != nil {
		t.Fatalf("UpdatePage() error = %v", err)
	}

	if gotMethod != http.MethodPut || gotPath != "/wiki/api/v2/pages/123" {
		t.Errorf("request = %s %s, want PUT /wiki/api/v2/pages/123", gotMethod, gotPath)
	}
	if !strings.Contains(gotRaw, body) {
		t.Errorf("payload = %s, want the body inline and unescaped (no \\u003c sequences)", gotRaw)
	}

	var sent UpdatePageRequest
	if err := json.Unmarshal([]byte(gotRaw), &sent); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v", err)
	}
	want := req
	if sent != want {
		t.Errorf("payload = %+v, want %+v", sent, want)
	}
	if page.Version.Number != 8 {
		t.Errorf("returned version = %d, want the server's 8", page.Version.Number)
	}
}

func TestChildPagesFiltersNonPagesAndCountsPagesTowardTheLimit(t *testing.T) {
	// Two whiteboards are interleaved with the pages, so a client that
	// counted raw children would return fewer pages than asked for.
	page1 := `{"results":[
		{"id":"1","type":"page","title":"P1","status":"current"},
		{"id":"2","type":"whiteboard","title":"W1"},
		{"id":"3","type":"page","title":"P2","status":"current"}
	],"_links":{"next":"/wiki/api/v2/pages/10/direct-children?cursor=CURSOR2&limit=3"}}`
	page2 := `{"results":[
		{"id":"4","type":"folder","title":"F1"},
		{"id":"5","type":"page","title":"P3","status":"archived"}
	],"_links":{}}`

	var gotCursors []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		gotCursors = append(gotCursors, cursor)
		if cursor == "" {
			_, _ = fmt.Fprint(w, page1)
			return
		}
		_, _ = fmt.Fprint(w, page2)
	})

	children, hasMore, err := client.ChildPages(t.Context(), "10", 10)
	if err != nil {
		t.Fatalf("ChildPages() error = %v", err)
	}
	if hasMore {
		t.Error("hasMore = true, want false once the cursor runs out")
	}

	var titles []string
	for _, child := range children {
		titles = append(titles, child.Title)
	}
	if want := []string{"P1", "P2", "P3"}; !slices.Equal(titles, want) {
		t.Errorf("titles = %v, want %v (whiteboards and folders filtered out)", titles, want)
	}
	if want := []string{"", "CURSOR2"}; !slices.Equal(gotCursors, want) {
		t.Errorf("cursors = %v, want %v", gotCursors, want)
	}
}

func TestPaginationReportsMoreWithoutOverfilling(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "3" {
			t.Errorf("limit = %q, want 3 (one past the caller's limit of 2)", got)
		}
		_, _ = fmt.Fprint(w, `{"results":[
			{"id":"1","type":"page","title":"P1"},
			{"id":"2","type":"page","title":"P2"},
			{"id":"3","type":"page","title":"P3"}
		],"_links":{}}`)
	})

	children, hasMore, err := client.ChildPages(t.Context(), "10", 2)
	if err != nil {
		t.Fatalf("ChildPages() error = %v", err)
	}
	if len(children) != 2 {
		t.Errorf("len(children) = %d, want 2 (the limit)", len(children))
	}
	if !hasMore {
		t.Error("hasMore = false, want true when the server had another item")
	}
}

func TestPaginationCapsPerRequestLimitAtTheAPIMaximum(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			t.Errorf("Atoi(limit) error = %v", err)
		}
		if got > maxPageSize {
			t.Errorf("limit = %d, want at most %d", got, maxPageSize)
		}
		_, _ = fmt.Fprint(w, `{"results":[],"_links":{}}`)
	})

	if _, _, err := client.ChildPages(t.Context(), "10", 5000); err != nil {
		t.Fatalf("ChildPages() error = %v", err)
	}
}

func TestPageCommentsRequestsStorageAndChronologicalOrder(t *testing.T) {
	var gotPath string
	var gotQuery map[string]string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = map[string]string{
			"body-format": r.URL.Query().Get("body-format"),
			"sort":        r.URL.Query().Get("sort"),
		}
		_, _ = fmt.Fprint(w, `{"results":[{"id":"c1","version":{"createdAt":"2026-01-01T00:00:00Z","authorId":"acc-1"},`+
			`"body":{"storage":{"value":"<p>hi</p>"}},"resolutionStatus":"open",`+
			`"properties":{"inlineOriginalSelection":"the highlighted words"}}],"_links":{}}`)
	})

	comments, _, err := client.PageComments(t.Context(), InlineComments, "123", 25)
	if err != nil {
		t.Fatalf("PageComments() error = %v", err)
	}

	if gotPath != "/wiki/api/v2/pages/123/inline-comments" {
		t.Errorf("path = %q, want /wiki/api/v2/pages/123/inline-comments", gotPath)
	}
	// view is not an accepted body-format on the comment endpoints, so the
	// client must ask for storage and render locally.
	if gotQuery["body-format"] != "storage" {
		t.Errorf("body-format = %q, want storage", gotQuery["body-format"])
	}
	if gotQuery["sort"] != "created-date" {
		t.Errorf("sort = %q, want created-date", gotQuery["sort"])
	}
	if len(comments) != 1 || comments[0].Properties.InlineOriginalSelection != "the highlighted words" {
		t.Errorf("comments = %+v, want the highlighted selection preserved", comments)
	}
	if comments[0].ResolutionStatus != "open" {
		t.Errorf("resolutionStatus = %q, want open", comments[0].ResolutionStatus)
	}
}

func TestCommentRepliesUsesTheChildrenEndpoint(t *testing.T) {
	var gotPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = fmt.Fprint(w, `{"results":[{"id":"c2","parentCommentId":"c1"}],"_links":{}}`)
	})

	replies, _, err := client.CommentReplies(t.Context(), FooterComments, "c1", 25)
	if err != nil {
		t.Fatalf("CommentReplies() error = %v", err)
	}
	if gotPath != "/wiki/api/v2/footer-comments/c1/children" {
		t.Errorf("path = %q, want /wiki/api/v2/footer-comments/c1/children", gotPath)
	}
	if len(replies) != 1 || replies[0].ParentCommentID != "c1" {
		t.Errorf("replies = %+v, want one reply pointing at c1", replies)
	}
}

func TestSearchPassesCQLThroughUnchangedAndReportsTheTotal(t *testing.T) {
	cql := `type = page and space = "DEV" and text ~ "hello world"`

	var gotCQL, gotPath string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCQL = r.URL.Path, r.URL.Query().Get("cql")
		_, _ = fmt.Fprint(w, `{"results":[{"content":{"id":"1","type":"page","title":"P"},"url":"/spaces/DEV/pages/1/P"}],`+
			`"totalSize":42}`)
	})

	results, total, err := client.Search(t.Context(), cql, 1)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotPath != "/wiki/rest/api/search" {
		t.Errorf("path = %q, want /wiki/rest/api/search", gotPath)
	}
	if gotCQL != cql {
		t.Errorf("cql = %q, want it passed through unchanged as %q", gotCQL, cql)
	}
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
	if len(results) != 1 || results[0].Content.ID != "1" {
		t.Errorf("results = %+v, want the single hit", results)
	}
}

func TestSearchPagesByOffset(t *testing.T) {
	var gotStarts []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotStarts = append(gotStarts, r.URL.Query().Get("start"))
		if r.URL.Query().Get("start") == "0" {
			_, _ = fmt.Fprint(w, `{"results":[{"title":"a"},{"title":"b"}],"totalSize":3}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"results":[{"title":"c"}],"totalSize":3}`)
	})

	results, _, err := client.Search(t.Context(), "type = page", 3)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 3 {
		t.Errorf("len(results) = %d, want 3 across two pages", len(results))
	}
	if want := []string{"0", "2"}; !slices.Equal(gotStarts, want) {
		t.Errorf("start offsets = %v, want %v", gotStarts, want)
	}
}

func TestSearchStopsWhenAPageComesBackEmpty(t *testing.T) {
	calls := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 5 {
			t.Fatal("Search() kept paging past an empty page")
		}
		_, _ = fmt.Fprint(w, `{"results":[],"totalSize":100}`)
	})

	results, _, err := client.Search(t.Context(), "type = page", 20)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1", calls)
	}
}

// A query that matched fewer pages than were asked for is the normal case
// when resolving a set of titles, so it must not cost a page of paging just
// to discover there is nothing more.
func TestSearchStopsOnceItHasEveryReportedMatch(t *testing.T) {
	calls := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{"results":[{"content":{"id":"1","type":"page","title":"P"}}],"totalSize":1}`)
	})

	results, total, err := client.Search(t.Context(), "type = page", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1 — the total says there is nothing more to fetch", calls)
	}
	if len(results) != 1 || total != 1 {
		t.Errorf("results = %d, total = %d, want the single reported match", len(results), total)
	}
}

func TestUsersLooksEveryAccountUpInOneRequest(t *testing.T) {
	var gotPath string
	var gotIDs []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotIDs = r.URL.Path, r.URL.Query()["accountId"]
		_, _ = fmt.Fprint(w, `{"results":[{"accountId":"acc-1","displayName":"Ada Lovelace"},`+
			`{"accountId":"acc-2","displayName":"Alan Turing"}]}`)
	})

	users, err := client.Users(t.Context(), []string{"acc-1", "acc-2"})
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if gotPath != "/wiki/rest/api/user/bulk" {
		t.Errorf("path = %q, want /wiki/rest/api/user/bulk", gotPath)
	}
	if want := []string{"acc-1", "acc-2"}; !slices.Equal(gotIDs, want) {
		t.Errorf("accountId params = %v, want each id passed once as %v", gotIDs, want)
	}
	if len(users) != 2 || users[0].DisplayName != "Ada Lovelace" || users[1].AccountID != "acc-2" {
		t.Errorf("users = %+v, want both accounts with their display names", users)
	}
}

// The endpoint silently truncates past its documented ceiling, so a body
// mentioning more people than that has to be split rather than half-resolved.
func TestUsersChunksPastTheBulkCeiling(t *testing.T) {
	ids := make([]string, maxBulkUsers+1)
	for i := range ids {
		ids[i] = "acc-" + strconv.Itoa(i)
	}

	var batches [][]string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		batch := r.URL.Query()["accountId"]
		batches = append(batches, batch)

		results := make([]string, 0, len(batch))
		for _, id := range batch {
			results = append(results, `{"accountId":"`+id+`","displayName":"name of `+id+`"}`)
		}
		_, _ = fmt.Fprint(w, `{"results":[`+strings.Join(results, ",")+`]}`)
	})

	users, err := client.Users(t.Context(), ids)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if len(batches) != 2 {
		t.Fatalf("requests = %d, want the ids split into 2", len(batches))
	}
	if len(batches[0]) != maxBulkUsers || len(batches[1]) != 1 {
		t.Errorf("batch sizes = %d and %d, want %d and 1", len(batches[0]), len(batches[1]), maxBulkUsers)
	}
	if len(users) != len(ids) {
		t.Errorf("len(users) = %d, want every account across both batches (%d)", len(users), len(ids))
	}
}

func TestUsersMakesNoRequestForNoAccounts(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("Users() sent a request for an empty account list")
	})

	users, err := client.Users(t.Context(), nil)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if len(users) != 0 {
		t.Errorf("users = %+v, want none", users)
	}
}

func TestPagesByTitleQueriesOneSpaceWithTheTitlesORed(t *testing.T) {
	var gotCQL string
	var gotLimit string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCQL, gotLimit = r.URL.Query().Get("cql"), r.URL.Query().Get("limit")
		_, _ = fmt.Fprint(w, `{"results":[{"content":{"id":"1","type":"page","title":"Runbook"}},`+
			`{"content":{"id":"2","type":"page","title":"Onboarding"}}],"totalSize":2}`)
	})

	matches, err := client.PagesByTitle(t.Context(), "DEV", []string{"Runbook", "Onboarding"})
	if err != nil {
		t.Fatalf("PagesByTitle() error = %v", err)
	}

	want := `type = page and space = "DEV" and (title = "Runbook" or title = "Onboarding")`
	if gotCQL != want {
		t.Errorf("cql = %q, want %q", gotCQL, want)
	}
	// One title resolves to one page, so asking for exactly as many results
	// as titles is what keeps the lookup to a single request.
	if gotLimit != "2" {
		t.Errorf("limit = %q, want 2 — one per title", gotLimit)
	}
	wantMatches := []PageMatch{{ID: "1", Title: "Runbook"}, {ID: "2", Title: "Onboarding"}}
	if !slices.Equal(matches, wantMatches) {
		t.Errorf("matches = %+v, want %+v", matches, wantMatches)
	}
}

// A title is user-supplied text that reaches the query as a CQL string
// literal, so a quote in it has to be escaped rather than close the literal
// early and turn the rest of the title into syntax.
func TestPagesByTitleEscapesTitlesAndSpaceKeys(t *testing.T) {
	var gotCQL string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCQL = r.URL.Query().Get("cql")
		_, _ = fmt.Fprint(w, `{"results":[],"totalSize":0}`)
	})

	if _, err := client.PagesByTitle(t.Context(), `~a"b`, []string{`The "Big" Release`, `C:\temp`}); err != nil {
		t.Fatalf("PagesByTitle() error = %v", err)
	}

	want := `type = page and space = "~a\"b" and (title = "The \"Big\" Release" or title = "C:\\temp")`
	if gotCQL != want {
		t.Errorf("cql = %q, want %q", gotCQL, want)
	}
}

func TestPagesByTitleSkipsResultsCarryingNoContent(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"results":[{"title":"Runbook","url":"/x"},`+
			`{"content":{"id":"1","type":"page","title":"Runbook"}}],"totalSize":2}`)
	})

	matches, err := client.PagesByTitle(t.Context(), "DEV", []string{"Runbook", "Onboarding"})
	if err != nil {
		t.Fatalf("PagesByTitle() error = %v", err)
	}
	if want := []PageMatch{{ID: "1", Title: "Runbook"}}; !slices.Equal(matches, want) {
		t.Errorf("matches = %+v, want only the hit that names a page %+v", matches, want)
	}
}

func TestPagesByTitleChunksLongTitleLists(t *testing.T) {
	titles := make([]string, maxTitlesPerQuery+1)
	for i := range titles {
		titles[i] = "Page " + strconv.Itoa(i)
	}

	queries := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		queries++
		_, _ = fmt.Fprint(w, `{"results":[],"totalSize":0}`)
	})

	if _, err := client.PagesByTitle(t.Context(), "DEV", titles); err != nil {
		t.Fatalf("PagesByTitle() error = %v", err)
	}
	if queries != 2 {
		t.Errorf("requests = %d, want the titles split into 2 queries", queries)
	}
}

func TestPagesByTitleMakesNoRequestForNoTitles(t *testing.T) {
	client := newTestClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("PagesByTitle() sent a request for an empty title list")
	})

	matches, err := client.PagesByTitle(t.Context(), "DEV", nil)
	if err != nil {
		t.Fatalf("PagesByTitle() error = %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches = %+v, want none", matches)
	}
}

func TestAPIErrorSurfacesStatusAndMessages(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantContains []string
	}{
		{
			name:         "v2 error envelope",
			status:       http.StatusBadRequest,
			body:         `{"errors":[{"title":"Bad Request","detail":"Version must be incremented"}]}`,
			wantContains: []string{"400", "Bad Request", "Version must be incremented"},
		},
		{
			name:         "v1 error envelope",
			status:       http.StatusUnauthorized,
			body:         `{"statusCode":401,"message":"Current user not permitted"}`,
			wantContains: []string{"401", "Current user not permitted"},
		},
		{
			name:         "unparseable body falls back to the raw text",
			status:       http.StatusConflict,
			body:         "<html>gateway said no</html>",
			wantContains: []string{"409", "gateway said no"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			})

			_, err := client.GetPage(t.Context(), "123", true)
			if err == nil {
				t.Fatal("GetPage() error = nil, want an error")
			}

			apiErr, ok := errors.AsType[*APIError](err)
			if !ok {
				t.Fatalf("GetPage() error = %v, want an *APIError", err)
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

func TestAPIErrorTruncatesLongRawBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "ascii", body: strings.Repeat("x", 5000)},
		// Multi-byte runes must not be cut in half: an invalid UTF-8 tail
		// would render as a replacement character in the user's terminal.
		{name: "multibyte", body: strings.Repeat("あ", 5000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, tt.body)
			})

			_, err := client.GetPage(t.Context(), "123", true)
			if err == nil {
				t.Fatal("GetPage() error = nil, want an error")
			}
			if len(err.Error()) > maxRawBodyInError+200 {
				t.Errorf("error length = %d, want the raw body truncated", len(err.Error()))
			}
			if !utf8.ValidString(err.Error()) {
				t.Errorf("error = %q, want valid UTF-8 after truncation", err.Error())
			}
		})
	}
}

func TestRequestsHonorContextCancellation(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := client.GetPage(ctx, "123", true); err == nil {
		t.Fatal("GetPage() error = nil, want the context error")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("GetPage() error = %v, want it to wrap context.Canceled", err)
	}
}

func TestNextCursor(t *testing.T) {
	tests := []struct {
		name string
		next string
		want string
	}{
		{name: "extracts the cursor", next: "/wiki/api/v2/pages/1/children?cursor=abc&limit=25", want: "abc"},
		{name: "absent link", next: "", want: ""},
		{name: "link without a cursor", next: "/wiki/api/v2/pages/1/children?limit=25", want: ""},
		{name: "unparseable link", next: "://nope", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCursor(tt.next); got != tt.want {
				t.Errorf("nextCursor(%q) = %q, want %q", tt.next, got, tt.want)
			}
		})
	}
}
