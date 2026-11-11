package cmd

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/178inaba/atlio/internal/confluence"
	"github.com/178inaba/atlio/internal/sidecar"
)

const (
	// testCreatedID is the page the create stub reports it made.
	testCreatedID = "777"

	// testParentSpaceID is the space the parent page lives in, which the
	// create request has to carry.
	testParentSpaceID = "42"
)

// createdWebUI is the relative web link the create stub returns for a page
// with the given title.
func createdWebUI(title string) string {
	return "/spaces/DEV/pages/" + testCreatedID + "/" + strings.ReplaceAll(title, " ", "+")
}

// recordedRequest is one request a stub received.
type recordedRequest struct {
	method, path, query, body string
}

// createStub answers the parent GET and the create POST, and — so a create
// can be followed by an update — the update's own GET and PUT of the created
// page. It records every request it receives.
type createStub struct {
	// createResponse, when set, replaces the created page the POST answers
	// with; title is the title the request asked for.
	createResponse func(title string) string
	// beforeCreateResponse runs after the POST is received and before it is
	// answered.
	beforeCreateResponse func()

	mu       sync.Mutex
	requests []recordedRequest
}

func (s *createStub) handler(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(raw)})
		s.mu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/wiki/api/v2/pages/123456":
			_, _ = fmt.Fprintf(w, `{"id":"123456","status":"current","title":"Parent","spaceId":%q,`+
				`"version":{"number":3},"_links":{"webui":%q}}`, testParentSpaceID, testPageWebUI)
		case r.Method == http.MethodPost && r.URL.Path == "/wiki/api/v2/pages":
			var req confluence.CreatePageRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("Unmarshal(POST body) error = %v", err)
				return
			}
			if s.beforeCreateResponse != nil {
				s.beforeCreateResponse()
			}
			if s.createResponse != nil {
				_, _ = io.WriteString(w, s.createResponse(req.Title))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"current","title":%q,"spaceId":%q,`+
				`"version":{"number":1},"_links":{"webui":%q}}`,
				testCreatedID, req.Title, testParentSpaceID, createdWebUI(req.Title))
		case r.Method == http.MethodGet && r.URL.Path == "/wiki/api/v2/pages/"+testCreatedID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"current","title":"New Page","version":{"number":1}}`,
				testCreatedID)
		case r.Method == http.MethodPut && r.URL.Path == "/wiki/api/v2/pages/"+testCreatedID:
			var req confluence.UpdatePageRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("Unmarshal(PUT body) error = %v", err)
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"status":"current","title":%q,"version":{"number":%d},`+
				`"_links":{"webui":%q}}`, testCreatedID, req.Title, req.Version.Number, createdWebUI(req.Title))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// recorded returns a copy of the requests received so far.
func (s *createStub) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// writeBodyFile writes body to a fresh file and returns its path.
func writeBodyFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "new.xml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func runCreate(t *testing.T, parent, title, file string, extra ...string) (cliRun, error) {
	t.Helper()

	args := []string{"confluence", "create", "--parent", parent, "--title", title, "-f", file}
	return runCLI(t, append(args, extra...)...)
}

// createdURL is the URL a page created by the stub is reported at.
func createdURL(title string) string {
	return testWikiBase + createdWebUI(title)
}

// TestCreateSendsOnePublishedPageUnderTheParent pins the whole exchange: the
// parent is read for its space only, and the page is created from the file's
// bytes as they are.
func TestCreateSendsOnePublishedPageUnderTheParent(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	body := `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>a &amp; b</p>` +
		`</ac:rich-text-body></ac:structured-macro>`
	file := writeBodyFile(t, body)
	stub := &createStub{}
	startAPI(t, stub.handler(t))

	run, err := runCreate(t, testPageURL, "New Page", file)
	if err != nil {
		t.Fatalf("create error = %v", err)
	}

	reqs := stub.recorded()
	if len(reqs) != 2 {
		t.Fatalf("requests = %+v, want one GET and one POST", reqs)
	}
	if get := reqs[0]; get.method != http.MethodGet || get.path != "/wiki/api/v2/pages/123456" || get.query != "" {
		t.Errorf("first request = %s %s?%s, want a GET of the parent with no body-format",
			get.method, get.path, get.query)
	}
	post := reqs[1]
	if post.method != http.MethodPost || post.path != "/wiki/api/v2/pages" || post.query != "" {
		t.Errorf("second request = %s %s?%s, want a POST to /wiki/api/v2/pages with no query",
			post.method, post.path, post.query)
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(post.body), &fields); err != nil {
		t.Fatalf("Unmarshal(POST body) error = %v", err)
	}
	if _, ok := fields["subtype"]; ok {
		t.Errorf("payload = %s, want no subtype", post.body)
	}
	var sent confluence.CreatePageRequest
	if err := json.Unmarshal([]byte(post.body), &sent); err != nil {
		t.Fatalf("Unmarshal(POST body) error = %v", err)
	}
	want := confluence.NewCreatePageRequest(testParentSpaceID, "123456", "current", "New Page", body)
	if sent != want {
		t.Errorf("payload = %+v, want %+v", sent, want)
	}

	if got, err := os.ReadFile(file); err != nil || string(got) != body {
		t.Errorf("body file = %q (err %v), want it untouched", got, err)
	}

	if want := "Created:  New Page\nURL:      " + createdURL("New Page") + "\n"; run.stdout != want {
		t.Errorf("output = %q, want %q", run.stdout, want)
	}
}

// TestCreateWritesTheSidecarFromTheResponse covers what makes the file a
// checkout: the same fields `confluence read` records, taken from what the
// server created.
func TestCreateWritesTheSidecarFromTheResponse(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	file := writeBodyFile(t, "<p>hi</p>")
	startAPI(t, (&createStub{}).handler(t))

	if _, err := runCreate(t, testPageURL, "New Page", file); err != nil {
		t.Fatalf("create error = %v", err)
	}

	meta, err := sidecar.Load(file)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	if meta.PageID != testCreatedID || meta.Version != 1 || meta.Title != "New Page" ||
		meta.Status != "current" || meta.PageURL != createdURL("New Page") {
		t.Errorf("sidecar = %+v, want the created page as the server returned it", meta)
	}
	if live, known := meta.LiveDoc(); live || !known {
		t.Errorf("LiveDoc() = (%v, %v), want a settled \"not a live doc\"", live, known)
	}
}

// TestCreateThenUpdateWithoutARead covers the step this command exists to
// remove: the created file goes straight to `confluence update`.
func TestCreateThenUpdateWithoutARead(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	file := writeBodyFile(t, "<p>first</p>")
	stub := &createStub{}
	startAPI(t, stub.handler(t))

	if _, err := runCreate(t, testPageURL, "New Page", file); err != nil {
		t.Fatalf("create error = %v", err)
	}
	if err := os.WriteFile(file, []byte("<p>second</p>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := runUpdate(t, file); err != nil {
		t.Fatalf("update error = %v", err)
	}

	var put *recordedRequest
	for _, r := range stub.recorded() {
		if r.method == http.MethodPut {
			put = &r
		}
	}
	if put == nil {
		t.Fatal("no PUT was sent")
	}
	var sent confluence.UpdatePageRequest
	if err := json.Unmarshal([]byte(put.body), &sent); err != nil {
		t.Fatalf("Unmarshal(PUT body) error = %v", err)
	}
	if put.path != "/wiki/api/v2/pages/"+testCreatedID || sent.Version.Number != 2 || sent.Body.Value != "<p>second</p>" {
		t.Errorf("PUT %s = %+v, want the created page at version 2 with the edited body", put.path, sent)
	}
}

func TestCreateJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, (&createStub{}).handler(t))

	run, err := runCreate(t, testPageURL, "New Page", writeBodyFile(t, "<p>hi</p>"), "--format", "json")
	if err != nil {
		t.Fatalf("create error = %v", err)
	}

	var got createResult
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	want := createResult{PageID: testCreatedID, Title: "New Page", PageURL: createdURL("New Page")}
	if got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
}

// TestCreateAcceptsEveryParentForm pins the page-addressing parity the other
// commands have: a URL, a short link and a bare ID all name the same parent.
func TestCreateAcceptsEveryParentForm(t *testing.T) {
	for _, parent := range []string{
		testPageURL,
		// QOIB encodes 123456.
		"https://example.atlassian.net/wiki/x/QOIB",
		"123456",
	} {
		t.Run(parent, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			stub := &createStub{}
			startAPI(t, stub.handler(t))

			if _, err := runCreate(t, parent, "New Page", writeBodyFile(t, "<p>hi</p>")); err != nil {
				t.Fatalf("create error = %v", err)
			}
			reqs := stub.recorded()
			if len(reqs) != 2 {
				t.Fatalf("requests = %+v, want one GET and one POST", reqs)
			}
			var sent confluence.CreatePageRequest
			if err := json.Unmarshal([]byte(reqs[1].body), &sent); err != nil {
				t.Fatalf("Unmarshal(POST body) error = %v", err)
			}
			if sent.ParentID != "123456" {
				t.Errorf("parentId = %q, want 123456", sent.ParentID)
			}
		})
	}
}

// TestCreateResolvesTheProfileFromTheParentURL checks the selection through
// the site the URLs are built from: every profile reaches the same test
// server with the same credentials, so the site is what tells them apart.
func TestCreateResolvesTheProfileFromTheParentURL(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net")

	file := writeBodyFile(t, "<p>hi</p>")
	startAPI(t, (&createStub{}).handler(t))

	run, err := runCreate(t, "https://other.atlassian.net/wiki/spaces/OPS/pages/123456/Parent", "New Page", file)
	if err != nil {
		t.Fatalf("create error = %v", err)
	}

	want := "https://other.atlassian.net/wiki" + createdWebUI("New Page")
	if !strings.Contains(run.stdout, want) {
		t.Errorf("output = %q, want the page under the other site (%s)", run.stdout, want)
	}
	meta, err := sidecar.Load(file)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	if meta.PageURL != want {
		t.Errorf("sidecar page_url = %q, want %q", meta.PageURL, want)
	}
}

func TestCreateWithAnEmptyBody(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	stub := &createStub{}
	startAPI(t, stub.handler(t))

	if _, err := runCreate(t, testPageURL, "New Page", writeBodyFile(t, "")); err != nil {
		t.Fatalf("create error = %v", err)
	}
	reqs := stub.recorded()
	if len(reqs) != 2 {
		t.Fatalf("requests = %+v, want one GET and one POST", reqs)
	}
	var sent confluence.CreatePageRequest
	if err := json.Unmarshal([]byte(reqs[1].body), &sent); err != nil {
		t.Fatalf("Unmarshal(POST body) error = %v", err)
	}
	if sent.Body.Representation != "storage" || sent.Body.Value != "" {
		t.Errorf("body = %+v, want an empty storage body", sent.Body)
	}
}

// TestCreateRefusesBeforeSendingAnything covers every check a page that
// cannot be taken back has to pass before the first request.
func TestCreateRefusesBeforeSendingAnything(t *testing.T) {
	tests := []struct {
		name string
		// args builds the command line from a body file with no sidecar.
		args        func(file string) []string
		wantContain string
	}{
		{
			name: "invalid format",
			args: func(file string) []string {
				return []string{"--parent", "123456", "--title", "T", "-f", file, "--format", "bogus"}
			},
			wantContain: `invalid --format "bogus"`,
		},
		{
			name:        "empty title",
			args:        func(file string) []string { return []string{"--parent", "123456", "--title", "", "-f", file} },
			wantContain: "--title",
		},
		{
			name:        "missing parent",
			args:        func(file string) []string { return []string{"--title", "T", "-f", file} },
			wantContain: "parent",
		},
		{
			name:        "missing title",
			args:        func(file string) []string { return []string{"--parent", "123456", "-f", file} },
			wantContain: "title",
		},
		{
			name:        "missing file",
			args:        func(string) []string { return []string{"--parent", "123456", "--title", "T"} },
			wantContain: "file",
		},
		{
			name: "unreadable file",
			args: func(file string) []string {
				return []string{"--parent", "123456", "--title", "T", "-f", filepath.Join(filepath.Dir(file), "missing.xml")}
			},
			wantContain: "missing.xml",
		},
		{
			name:        "stdin",
			args:        func(string) []string { return []string{"--parent", "123456", "--title", "T", "-f", "-"} },
			wantContain: "sidecar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			startAPI(t, neverCalled(t, "before the create was validated"))

			args := append([]string{"confluence", "create"}, tt.args(writeBodyFile(t, "<p>hi</p>"))...)
			_, err := runCLI(t, args...)
			if err == nil {
				t.Fatalf("create error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantContain) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantContain)
			}
		})
	}
}

// TestCreateRefusesAFileThatIsAlreadyACheckout covers the refusal that keeps
// another page's sidecar from being replaced, and a second create on the same
// file from making a second page.
func TestCreateRefusesAFileThatIsAlreadyACheckout(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, neverCalled(t, "for a file that already has a sidecar"))

	file := seedReadPage(t, "<p>hi</p>", currentMeta())
	before, err := os.ReadFile(sidecar.Path(file))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	_, err = runCreate(t, testPageURL, "New Page", file)
	if err == nil {
		t.Fatal("create error = nil, want a refusal")
	}
	for _, want := range []string{sidecar.Path(file), "confluence update"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	after, err := os.ReadFile(sidecar.Path(file))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Errorf("sidecar = %q, want it left as %q", after, before)
	}
}

func TestCreateSurfacesAnAPIErrorAndWritesNoSidecar(t *testing.T) {
	for _, failing := range []string{http.MethodGet, http.MethodPost} {
		t.Run(failing, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			inner := (&createStub{}).handler(t)
			startAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != failing {
					inner(w, r)
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"errors":[{"status":400,"code":"BAD_REQUEST",`+
					`"title":"A page with this title already exists","detail":null}]}`)
			})

			file := writeBodyFile(t, "<p>hi</p>")
			_, err := runCreate(t, testPageURL, "New Page", file)
			if err == nil {
				t.Fatal("create error = nil, want the server's rejection")
			}
			if !strings.Contains(err.Error(), "A page with this title already exists") {
				t.Errorf("error = %q, want the server's message", err)
			}
			if _, err := os.Lstat(sidecar.Path(file)); !os.IsNotExist(err) {
				t.Errorf("Lstat(sidecar) = %v, want no sidecar", err)
			}
		})
	}
}

// assertCreatedButNoCheckout checks the error for a page that exists while
// the file is not a checkout of it: it has to say so and where the page is,
// and must not steer the caller to a read into the same file, which would
// remove whatever is at the sidecar's path.
func assertCreatedButNoCheckout(t *testing.T, err error, file string) {
	t.Helper()

	if err == nil {
		t.Fatal("create error = nil, want an error")
	}
	for _, want := range []string{"was created", createdURL("New Page"), "confluence read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "-o "+file) {
		t.Errorf("error = %q, want no read into %s", err, file)
	}
}

func TestCreateReportsTheCreatedPageWhenTheSidecarCannotBeWritten(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// No version: a sidecar built from it would be rejected by every later
	// `confluence update`, so none is written.
	stub := &createStub{createResponse: func(title string) string {
		return fmt.Sprintf(`{"id":%q,"status":"current","title":%q,"_links":{"webui":%q}}`,
			testCreatedID, title, createdWebUI(title))
	}}
	startAPI(t, stub.handler(t))

	file := writeBodyFile(t, "<p>hi</p>")
	_, err := runCreate(t, testPageURL, "New Page", file)
	assertCreatedButNoCheckout(t, err, file)
	if _, err := os.Lstat(sidecar.Path(file)); !os.IsNotExist(err) {
		t.Errorf("Lstat(sidecar) = %v, want no sidecar", err)
	}
}

// TestCreateLeavesAnEntryThatAppearedDuringTheRequest covers the window
// between the pre-flight check and the write: whatever appeared there is kept
// as it is.
func TestCreateLeavesAnEntryThatAppearedDuringTheRequest(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	file := writeBodyFile(t, "<p>hi</p>")
	appeared := []byte(`{"page_id":"999"}`)
	stub := &createStub{beforeCreateResponse: func() {
		if err := os.WriteFile(sidecar.Path(file), appeared, 0o644); err != nil {
			t.Errorf("WriteFile() error = %v", err)
		}
	}}
	startAPI(t, stub.handler(t))

	_, err := runCreate(t, testPageURL, "New Page", file)
	assertCreatedButNoCheckout(t, err, file)

	got, err := os.ReadFile(sidecar.Path(file))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(got, appeared) {
		t.Errorf("sidecar = %q, want it left as %q", got, appeared)
	}
}

// TestCreateReportsADeadlineThatExpiresDuringTheCreate covers the case the
// documented 124 check is about: the POST was sent, and whether the page
// exists is unknown, so nothing is written for it.
func TestCreateReportsADeadlineThatExpiresDuringTheCreate(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	inner := (&createStub{}).handler(t)
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			inner(w, r)
			return
		}
		// The server only notices the client giving up once the request
		// body has been consumed, so it is drained before the stall.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})

	file := writeBodyFile(t, "<p>hi</p>")
	_, err := runCreate(t, testPageURL, "New Page", file, "--timeout=200ms")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to carry context.DeadlineExceeded", err)
	}
	if _, err := os.Lstat(sidecar.Path(file)); !os.IsNotExist(err) {
		t.Errorf("Lstat(sidecar) = %v, want no sidecar", err)
	}
}

// TestCreatingCommandsDocumentTheCheckAfterATimeout pins the help text an
// agent reads before re-running a command that may already have created
// something.
func TestCreatingCommandsDocumentTheCheckAfterATimeout(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{
			args: []string{"confluence", "--help"},
			want: []string{"create"},
		},
		{
			args: []string{"confluence", "create", "--help"},
			want: []string{
				"--parent", "--title", "--file", "storage", "sidecar", "already has a sidecar",
				"confluence update", "124", "confluence children",
			},
		},
		{
			args: []string{"confluence", "comments", "create", "--help"},
			want: []string{"124", "confluence comments list"},
		},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			isolateConfig(t)

			run, err := runCLI(t, tt.args...)
			if err != nil {
				t.Fatalf("%v error = %v", tt.args, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(run.stdout, want) {
					t.Errorf("help = %q, want it to contain %q", run.stdout, want)
				}
			}
		})
	}
}
