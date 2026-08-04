package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func runCommentsCmd(t *testing.T, arg string, limit int) (string, error) {
	t.Helper()

	original := commentsLimitFlag
	commentsLimitFlag = limit
	t.Cleanup(func() { commentsLimitFlag = original })

	cmd, out := newTestCommand(t)
	err := runComments(cmd, []string{arg})
	return out.String(), err
}

// commentsAPI routes the four calls comments makes: the two root listings
// and the replies of each root.
func commentsAPI(t *testing.T, footer, inline string, replies map[string]string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/pages/123456/footer-comments"):
			_, _ = w.Write([]byte(footer))
		case strings.HasSuffix(path, "/pages/123456/inline-comments"):
			_, _ = w.Write([]byte(inline))
		case strings.HasSuffix(path, "/children"):
			segments := strings.Split(path, "/")
			id := segments[len(segments)-2]
			body, ok := replies[id]
			if !ok {
				body = `{"results":[],"_links":{}}`
			}
			_, _ = w.Write([]byte(body))
		default:
			t.Errorf("unexpected request to %s", path)
		}
	}
}

const emptyComments = `{"results":[],"_links":{}}`

func TestCommentsShowsBothSectionsWithRepliesAndInlineMetadata(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"createdAt":"2026-01-01T10:00:00Z","authorId":"acc-author"},` +
		`"body":{"storage":{"value":"<p>Looks good to me</p>"}}}],"_links":{}}`
	inline := `{"results":[{"id":"i1","version":{"createdAt":"2026-01-02T10:00:00Z","authorId":"acc-reviewer"},` +
		`"body":{"storage":{"value":"<p>Should this be &quot;shall&quot;?</p>"}},` +
		`"resolutionStatus":"open","properties":{"inlineOriginalSelection":"the system should"}}],"_links":{}}`
	replies := map[string]string{
		"i1": `{"results":[{"id":"i2","parentCommentId":"i1",` +
			`"version":{"createdAt":"2026-01-02T11:00:00Z","authorId":"acc-author"},` +
			`"body":{"storage":{"value":"<p>Good catch, fixed</p>"}}}],"_links":{}}`,
	}
	startAPI(t, commentsAPI(t, footer, inline, replies))

	output, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("runComments() error = %v", err)
	}

	for _, want := range []string{
		"## Footer comments",
		"## Inline comments",
		"acc-author",
		"2026-01-01T10:00:00Z",
		"Looks good to me",
		// The inline comment's anchor text and resolution state.
		"the system should",
		"[open]",
		// Storage XHTML rendered as text, entities decoded.
		`Should this be "shall"?`,
		// Replies come from a separate endpoint; without them the answer
		// to the question above would be missing entirely.
		"Good catch, fixed",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	}
	if strings.Contains(output, "<p>") {
		t.Errorf("output = %q, want the storage markup stripped", output)
	}
}

func TestCommentsIndentsRepliesUnderTheirParent(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},` +
		`"body":{"storage":{"value":"<p>question</p>"}}}],"_links":{}}`
	replies := map[string]string{
		"f1": `{"results":[{"id":"f2","parentCommentId":"f1","version":{"authorId":"acc-2"},` +
			`"body":{"storage":{"value":"<p>answer</p>"}}}],"_links":{}}`,
	}
	startAPI(t, commentsAPI(t, footer, emptyComments, replies))

	output, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("runComments() error = %v", err)
	}
	if !strings.Contains(output, "\n  - acc-2") {
		t.Errorf("output = %q, want the reply indented under its parent", output)
	}
}

func TestCommentsRequestsRepliesOnlyForRoots(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>a</p>"}}},` +
		`{"id":"f2","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>b</p>"}}}],"_links":{}}`

	var replyCalls []string
	inner := commentsAPI(t, footer, emptyComments, nil)
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/children") {
			replyCalls = append(replyCalls, r.URL.Path)
		}
		inner(w, r)
	})

	if _, err := runCommentsCmd(t, testPageURL, 25); err != nil {
		t.Fatalf("runComments() error = %v", err)
	}
	if len(replyCalls) != 2 {
		t.Errorf("reply requests = %v, want one per root comment", replyCalls)
	}
	for _, call := range replyCalls {
		if !strings.Contains(call, "/footer-comments/") {
			t.Errorf("reply request %q, want it under the footer-comments family", call)
		}
	}
}

func TestCommentsWithNone(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, commentsAPI(t, emptyComments, emptyComments, nil))

	output, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("runComments() error = %v", err)
	}
	if strings.Count(output, "None.") != 2 {
		t.Errorf("output = %q, want both sections to report none", output)
	}
}

func TestCommentsReportsTruncation(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>a</p>"}}},` +
		`{"id":"f2","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>b</p>"}}}],"_links":{}}`
	startAPI(t, commentsAPI(t, footer, emptyComments, nil))

	output, err := runCommentsCmd(t, testPageURL, 1)
	if err != nil {
		t.Fatalf("runComments() error = %v", err)
	}
	if !strings.Contains(output, "--limit") {
		t.Errorf("output = %q, want a truncation notice pointing at --limit", output)
	}
}

func TestCommentsJSONOutput(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "json")
	seedProfile(t, "example", testSite)

	inline := `{"results":[{"id":"i1","version":{"createdAt":"2026-01-02T10:00:00Z","authorId":"acc-1"},` +
		`"body":{"storage":{"value":"<p>note</p>"}},"resolutionStatus":"resolved",` +
		`"properties":{"inlineOriginalSelection":"anchor text"}}],"_links":{}}`
	replies := map[string]string{
		"i1": `{"results":[{"id":"i2","version":{"authorId":"acc-2"},` +
			`"body":{"storage":{"value":"<p>reply</p>"}}}],"_links":{}}`,
	}
	startAPI(t, commentsAPI(t, emptyComments, inline, replies))

	output, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("runComments() error = %v", err)
	}

	var got struct {
		FooterComments struct {
			Comments []commentItem `json:"comments"`
		} `json:"footer_comments"`
		InlineComments struct {
			Comments []commentItem `json:"comments"`
		} `json:"inline_comments"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
	}
	if len(got.FooterComments.Comments) != 0 {
		t.Errorf("footer comments = %+v, want none", got.FooterComments.Comments)
	}
	if len(got.InlineComments.Comments) != 1 {
		t.Fatalf("inline comments = %+v, want one", got.InlineComments.Comments)
	}

	comment := got.InlineComments.Comments[0]
	if comment.Author != "acc-1" || comment.Body != "note" {
		t.Errorf("comment = %+v, want the author id and stripped body", comment)
	}
	if comment.Highlight != "anchor text" || comment.Status != "resolved" {
		t.Errorf("comment = %+v, want the inline anchor text and resolution status", comment)
	}
	if len(comment.Replies) != 1 || comment.Replies[0].Body != "reply" {
		t.Errorf("replies = %+v, want the nested reply", comment.Replies)
	}
}

func TestCommentsRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an invalid --limit")
	})

	if _, err := runCommentsCmd(t, testPageURL, maxLimit+1); err == nil {
		t.Error("runComments() error = nil for an oversized --limit, want an error")
	}
}
