package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func runCommentsCmd(t *testing.T, arg string, limit int, extra ...string) (cflioRun, error) {
	t.Helper()
	return runLimitCmd(t, "comments list", arg, limit, extra...)
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

	run, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("comments error = %v", err)
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
		// Storage XHTML rendered as Markdown, entities decoded.
		`Should this be "shall"?`,
		// Replies come from a separate endpoint; without them the answer
		// to the question above would be missing entirely.
		"Good catch, fixed",
	} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
	if strings.Contains(run.stdout, "<p>") {
		t.Errorf("output = %q, want the storage markup converted", run.stdout)
	}
}

// TestCommentsRendersBodiesAsMarkdown pins the structure a comment body keeps
// now that it goes through the same converter as `read --markdown`: a code
// macro stays a code block and a table stays a table, both of which the old
// flattening squashed to one line per block.
func TestCommentsRendersBodiesAsMarkdown(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// Written as plain markup and escaped by strconv.Quote rather than by
	// hand like the fixtures above: a code macro and a mention carry enough
	// quoted attributes that hand-escaping them stops being readable.
	body := `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter>` +
		`<ac:plain-text-body><![CDATA[println(hello)]]></ac:plain-text-body></ac:structured-macro>` +
		`<table><tbody><tr><th>Key</th><th>Value</th></tr><tr><td>a</td><td>1</td></tr></tbody></table>` +
		`<p>ping <ac:link><ri:user ri:account-id="acc-9"/></ac:link></p>`
	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},"body":{"storage":{"value":` +
		strconv.Quote(body) + `}}}],"_links":{}}`
	startAPI(t, commentsAPI(t, footer, emptyComments, nil))

	run, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("comments error = %v", err)
	}

	// Every line is indented under the comment header, which is what makes the
	// body a continuation of its bullet rather than a sibling block.
	for _, want := range []string{
		"  ```go\n  println(hello)\n  ```",
		"  | Key | Value |\n  | --- | --- |\n  | a | 1 |",
		"  ping @acc-9",
	} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
}

func TestCommentsIndentsRepliesUnderTheirParent(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},` +
		`"body":{"storage":{"value":"<p>question</p>"}}}],"_links":{}}`
	replies := map[string]string{
		"f1": `{"results":[{"id":"f2","parentCommentId":"f1","version":{"authorId":"acc-2"},` +
			`"body":{"storage":{"value":"<p>answer</p>"}}}],"_links":{}}`,
	}
	startAPI(t, commentsAPI(t, footer, emptyComments, replies))

	run, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("comments error = %v", err)
	}
	if !strings.Contains(run.stdout, "\n  - acc-2") {
		t.Errorf("output = %q, want the reply indented under its parent", run.stdout)
	}
}

func TestCommentsRequestsRepliesOnlyForRoots(t *testing.T) {
	isolateConfig(t)
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
		t.Fatalf("comments error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, commentsAPI(t, emptyComments, emptyComments, nil))

	run, err := runCommentsCmd(t, testPageURL, 25)
	if err != nil {
		t.Fatalf("comments error = %v", err)
	}
	if strings.Count(run.stdout, "None.") != 2 {
		t.Errorf("output = %q, want both sections to report none", run.stdout)
	}
}

func TestCommentsReportsTruncation(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	footer := `{"results":[{"id":"f1","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>a</p>"}}},` +
		`{"id":"f2","version":{"authorId":"acc-1"},"body":{"storage":{"value":"<p>b</p>"}}}],"_links":{}}`
	startAPI(t, commentsAPI(t, footer, emptyComments, nil))

	run, err := runCommentsCmd(t, testPageURL, 1)
	if err != nil {
		t.Fatalf("comments error = %v", err)
	}
	if !strings.Contains(run.stdout, "--limit") {
		t.Errorf("output = %q, want a truncation notice pointing at --limit", run.stdout)
	}
}

func TestCommentsJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	inline := `{"results":[{"id":"i1","version":{"createdAt":"2026-01-02T10:00:00Z","authorId":"acc-1"},` +
		`"body":{"storage":{"value":"<p>note</p>"}},"resolutionStatus":"resolved",` +
		`"properties":{"inlineOriginalSelection":"anchor text"}}],"_links":{}}`
	replies := map[string]string{
		"i1": `{"results":[{"id":"i2","version":{"authorId":"acc-2"},` +
			`"body":{"storage":{"value":"<p>reply</p>"}}}],"_links":{}}`,
	}
	startAPI(t, commentsAPI(t, emptyComments, inline, replies))

	run, err := runCommentsCmd(t, testPageURL, 25, "--format", "json")
	if err != nil {
		t.Fatalf("comments error = %v", err)
	}

	var got struct {
		FooterComments struct {
			Comments []commentItem `json:"comments"`
		} `json:"footer_comments"`
		InlineComments struct {
			Comments []commentItem `json:"comments"`
		} `json:"inline_comments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	if len(got.FooterComments.Comments) != 0 {
		t.Errorf("footer comments = %+v, want none", got.FooterComments.Comments)
	}
	if len(got.InlineComments.Comments) != 1 {
		t.Fatalf("inline comments = %+v, want one", got.InlineComments.Comments)
	}

	comment := got.InlineComments.Comments[0]
	if comment.Author != "acc-1" || comment.Body != "note" {
		t.Errorf("comment = %+v, want the author id and converted body", comment)
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
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an invalid --limit"))

	if _, err := runCommentsCmd(t, testPageURL, maxLimit+1); err == nil {
		t.Error("comments error = nil for an oversized --limit, want an error")
	}
}

// createCommentAPI answers the one POST `comments create` makes, recording
// the raw request body so a test can assert on what actually travelled
// rather than on what a decoder made of it.
func createCommentAPI(t *testing.T, raw *string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/footer-comments") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			return
		}
		*raw = string(body)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":"c9","_links":{"webui":"`+testPageWebUI+`?focusedCommentId=c9"}}`)
	}
}

// writeCommentFile writes a comment body to a temporary file and returns its
// path.
func writeCommentFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "note.xml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// commentBodySent returns the body value out of a recorded request payload.
func commentBodySent(t *testing.T, raw string) string {
	t.Helper()

	var sent struct {
		PageID string `json:"pageId"`
		Body   struct {
			Representation string `json:"representation"`
			Value          string `json:"value"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &sent); err != nil {
		t.Fatalf("Unmarshal(payload) error = %v; payload = %q", err, raw)
	}
	if sent.PageID != "123456" {
		t.Errorf("pageId = %q, want the page the command addressed", sent.PageID)
	}
	if sent.Body.Representation != "storage" {
		t.Errorf("representation = %q, want storage", sent.Body.Representation)
	}
	return sent.Body.Value
}

// TestCommentsCreateSendsTheFileUnchanged covers the reason this command
// exists: a note written as storage XHTML has to reach Confluence as the
// markup it is, not as text describing it.
func TestCommentsCreateSendsTheFileUnchanged(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	body := `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>a &amp; b</p>` +
		`</ac:rich-text-body></ac:structured-macro>`
	var raw string
	startAPI(t, createCommentAPI(t, &raw))

	run, err := runCflio(t, "comments", "create", testPageURL, "-f", writeCommentFile(t, body))
	if err != nil {
		t.Fatalf("comments create error = %v", err)
	}

	if got := commentBodySent(t, raw); got != body {
		t.Errorf("body = %q, want it byte-identical to %q", got, body)
	}
	for _, want := range []string{"c9", "123456", testPageURL} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
}

func TestCommentsCreateReadsTheBodyFromStdin(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	body := "<p>from stdin</p>"
	var raw string
	startAPI(t, createCommentAPI(t, &raw))

	if _, err := runCflioWithStdin(t, body, "comments", "create", testPageURL, "-f", "-"); err != nil {
		t.Fatalf("comments create error = %v", err)
	}
	if got := commentBodySent(t, raw); got != body {
		t.Errorf("body = %q, want the bytes read from stdin (%q)", got, body)
	}
}

// TestCommentsCreateAcceptsABarePageID pins the page-addressing parity the
// other commands have: an id names the same page a URL does.
func TestCommentsCreateAcceptsABarePageID(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	var raw string
	startAPI(t, createCommentAPI(t, &raw))

	run, err := runCflio(t, "comments", "create", "123456", "-f", writeCommentFile(t, "<p>note</p>"))
	if err != nil {
		t.Fatalf("comments create error = %v", err)
	}
	if got := commentBodySent(t, raw); got != "<p>note</p>" {
		t.Errorf("body = %q, want the file's contents", got)
	}
	if !strings.Contains(run.stdout, "c9") {
		t.Errorf("output = %q, want the created comment id", run.stdout)
	}
}

func TestCommentsCreateJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	var raw string
	startAPI(t, createCommentAPI(t, &raw))

	run, err := runCflio(t, "comments", "create", testPageURL,
		"-f", writeCommentFile(t, "<p>note</p>"), "--format", "json")
	if err != nil {
		t.Fatalf("comments create error = %v", err)
	}

	var got commentResult
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	if got.CommentID != "c9" || got.PageID != "123456" {
		t.Errorf("result = %+v, want the created comment and its page", got)
	}
	if !strings.HasPrefix(got.CommentURL, testPageURL) {
		t.Errorf("comment url = %q, want it under the page URL", got.CommentURL)
	}
}

// TestCommentsCreateSendsNothingForAnUnreadableFile pins that the read comes
// first: a comment cannot be taken back, so a missing file has to fail before
// anything is posted.
func TestCommentsCreateSendsNothingForAnUnreadableFile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an unreadable body file"))

	missing := filepath.Join(t.TempDir(), "missing.xml")
	if _, err := runCflio(t, "comments", "create", testPageURL, "-f", missing); err == nil {
		t.Error("comments create error = nil for a missing file, want an error")
	}
}
