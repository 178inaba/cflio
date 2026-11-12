package cmd

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/atlassian"
	"github.com/178inaba/atlio/internal/jira"
)

const (
	jiraIssuePath       = "/rest/api/3/issue/KAN-1"
	jiraContentBasePath = "/rest/api/3/attachment/content/"
)

// jiraAttachment is one entry of the attachment field a test serves. The ID
// is derived from its position, so a test names the content path it expects
// as jiraContentBasePath + "1000<n>".
type jiraAttachment struct {
	filename string
	mimeType string
	size     int
}

// jiraAttachmentsAPI answers the issue with the given attachments and every
// content request with pngBytes, recording the content paths requested.
//
// The issue path is matched exactly and must carry fields=attachment, and
// anything but a GET fails the test: nothing atlio sends to Jira changes
// anything in it.
func jiraAttachmentsAPI(t *testing.T, attachments ...jiraAttachment) (*[]string, http.HandlerFunc) {
	t.Helper()

	var content []string
	return &content, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %s, want GET", r.Method)
		}
		switch {
		case r.URL.Path == jiraIssuePath:
			if got := r.URL.Query().Get("fields"); got != "attachment" {
				t.Errorf("fields = %q, want attachment", got)
			}
			items := make([]map[string]any, 0, len(attachments))
			for i, a := range attachments {
				id := fmt.Sprintf("1000%d", i+1)
				items = append(items, map[string]any{
					"id":       id,
					"filename": a.filename,
					"mimeType": a.mimeType,
					"size":     a.size,
					"content":  testSite + jiraContentBasePath + id,
				})
			}
			payload, err := json.Marshal(map[string]any{
				"key":    "KAN-1",
				"fields": map[string]any{"attachment": items},
			})
			if err != nil {
				t.Errorf("Marshal() error = %v", err)
				return
			}
			_, _ = w.Write(payload)
		case strings.HasPrefix(r.URL.Path, jiraContentBasePath):
			content = append(content, r.URL.Path)
			_, _ = w.Write(pngBytes)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}
}

func runJiraDownload(t *testing.T, dir, pattern string, extra ...string) (cliRun, error) {
	t.Helper()

	args := append([]string{"jira", "attachments", "download", "KAN-1", "--pattern", pattern, "-o", dir}, extra...)
	return runCLI(t, args...)
}

func TestJiraAttachmentsListShowsFilenameMediaTypeAndSize(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := jiraAttachmentsAPI(t,
		jiraAttachment{filename: "screen.png", mimeType: "image/png", size: 1234},
		jiraAttachment{filename: "spec.pdf", mimeType: "application/pdf", size: 9},
	)
	startAPI(t, handler)

	run, err := runCLI(t, "jira", "attachments", "list", "KAN-1")
	if err != nil {
		t.Fatalf("jira attachments list error = %v", err)
	}

	want := "- **screen.png** (image/png, 1234 bytes)\n- **spec.pdf** (application/pdf, 9 bytes)\n"
	if run.stdout != want {
		t.Errorf("output = %q, want %q", run.stdout, want)
	}
}

func TestJiraAttachmentsListJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := jiraAttachmentsAPI(t, jiraAttachment{filename: "screen.png", mimeType: "image/png", size: 1234})
	startAPI(t, handler)

	run, err := runCLI(t, "jira", "attachments", "list", "KAN-1", "--format", "json")
	if err != nil {
		t.Fatalf("jira attachments list error = %v", err)
	}

	var got struct {
		Attachments []attachmentItem `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", run.stdout, err)
	}
	want := []attachmentItem{{Filename: "screen.png", MediaType: "image/png", FileSize: 1234}}
	if !slices.Equal(got.Attachments, want) {
		t.Errorf("attachments = %+v, want %+v", got.Attachments, want)
	}
}

func TestJiraAttachmentsDownloadWritesOnlyTheMatchingFiles(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	content, handler := jiraAttachmentsAPI(t,
		jiraAttachment{filename: "screen.png", mimeType: "image/png", size: len(pngBytes)},
		jiraAttachment{filename: "spec.pdf", mimeType: "application/pdf", size: 9},
	)
	startAPI(t, handler)

	dir := filepath.Join(t.TempDir(), "assets")
	run, err := runJiraDownload(t, dir, "*.png", "--format", "json")
	if err != nil {
		t.Fatalf("jira attachments download error = %v", err)
	}

	if want := []string{jiraContentBasePath + "10001"}; !slices.Equal(*content, want) {
		t.Errorf("content requests = %v, want %v (the PNG only)", *content, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatalf("ReadFile(screen.png) error = %v", err)
	}
	if !bytes.Equal(got, pngBytes) {
		t.Errorf("screen.png = %v, want the served bytes verbatim %v", got, pngBytes)
	}
	if _, err := os.Stat(filepath.Join(dir, "spec.pdf")); !os.IsNotExist(err) {
		t.Errorf("Stat(spec.pdf) error = %v, want the non-matching attachment left alone", err)
	}

	var out struct {
		Items []downloadedItem `json:"downloaded_attachments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &out); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", run.stdout, err)
	}
	want := []downloadedItem{{Filename: "screen.png", Path: filepath.Join(dir, "screen.png"), Bytes: int64(len(pngBytes))}}
	if !slices.Equal(out.Items, want) {
		t.Errorf("downloaded = %+v, want %+v", out.Items, want)
	}
}

// hostTransport answers each request from the handler registered for its
// host, without a network. Two httptest servers would both be 127.0.0.1, and
// net/http copies the Authorization header on a redirect between them, so a
// redirect to "another host" has to be another hostname to test anything.
type hostTransport struct {
	handlers map[string]http.Handler
	seen     []*http.Request
}

func (h *hostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	h.seen = append(h.seen, req)
	handler, ok := h.handlers[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no handler for host %s", req.URL.Host)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func TestJiraAttachmentsDownloadKeepsCredentialsOffTheMediaHost(t *testing.T) {
	const (
		siteHost  = "example.atlassian.net"
		mediaHost = "api.media.atlassian.com"
	)

	isolateConfig(t)
	seedProfile(t, "example", testSite)

	transport := &hostTransport{handlers: map[string]http.Handler{
		siteHost: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == jiraIssuePath {
				_, _ = fmt.Fprint(w, `{"key":"KAN-1","fields":{"attachment":[{"id":"10001","filename":"screen.png","mimeType":"image/png","size":11}]}}`)
				return
			}
			w.Header().Set("Location", "https://"+mediaHost+"/file/0f0e/binary?token=signed&client=c&dl=true&name=screen.png")
			w.WriteHeader(http.StatusSeeOther)
		}),
		mediaHost: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(pngBytes)
		}),
	}}
	original := jiraFactory
	jiraFactory = func(site, email, token string) (*jira.Client, error) {
		return jira.New(site, email, token, atlassian.WithHTTPClient(&http.Client{Transport: transport}))
	}
	t.Cleanup(func() { jiraFactory = original })

	dir := t.TempDir()
	if _, err := runJiraDownload(t, dir, "*.png"); err != nil {
		t.Fatalf("jira attachments download error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatalf("ReadFile(screen.png) error = %v", err)
	}
	if !bytes.Equal(got, pngBytes) {
		t.Errorf("screen.png = %v, want the media host's bytes %v", got, pngBytes)
	}

	var hosts []string
	for _, req := range transport.seen {
		hosts = append(hosts, req.URL.Host+req.URL.Path)
		if req.Method != http.MethodGet {
			t.Errorf("request to %s%s method = %s, want GET", req.URL.Host, req.URL.Path, req.Method)
		}
		auth := req.Header.Get("Authorization")
		if req.URL.Host == siteHost && auth == "" {
			t.Errorf("request to %s%s carries no Authorization, want the site's credentials", req.URL.Host, req.URL.Path)
		}
		if req.URL.Host == mediaHost && auth != "" {
			t.Errorf("request to the media host carries Authorization %q, want none", auth)
		}
	}
	want := []string{siteHost + jiraIssuePath, siteHost + jiraContentBasePath + "10001", mediaHost + "/file/0f0e/binary"}
	if !slices.Equal(hosts, want) {
		t.Errorf("requests = %v, want %v", hosts, want)
	}
}

func TestJiraAttachmentsDownloadWithNoMatchNamesTheAttachmentCount(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	content, handler := jiraAttachmentsAPI(t,
		jiraAttachment{filename: "spec.pdf", mimeType: "application/pdf", size: 9},
		jiraAttachment{filename: "notes.txt", mimeType: "text/plain", size: 3},
	)
	startAPI(t, handler)

	_, err := runJiraDownload(t, t.TempDir(), "*.png")
	if err == nil {
		t.Fatal("jira attachments download error = nil, want a non-matching pattern to fail")
	}
	if want := `no attachment matches "*.png" (2 on the issue)`; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if len(*content) != 0 {
		t.Errorf("content requests = %v, want none", *content)
	}
}

func TestJiraAttachmentsDownloadRequiresThePatternFlag(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, neverCalled(t, "despite a missing --pattern"))

	_, err := runCLI(t, "jira", "attachments", "download", "KAN-1")
	if err == nil {
		t.Fatal("jira attachments download error = nil, want the missing flag reported")
	}
	if !strings.Contains(err.Error(), `required flag(s) "pattern" not set`) {
		t.Errorf("error = %q, want cobra's usage error naming --pattern", err)
	}
}

func TestJiraAttachmentsDownloadRefusesToOverwriteBeforeFetchingAnything(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	content, handler := jiraAttachmentsAPI(t,
		jiraAttachment{filename: "diagram.png", mimeType: "image/png", size: len(pngBytes)},
		jiraAttachment{filename: "screen.png", mimeType: "image/png", size: len(pngBytes)},
	)
	startAPI(t, handler)

	dir := t.TempDir()
	existing := filepath.Join(dir, "screen.png")
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := runJiraDownload(t, dir, "*.png"); err == nil {
		t.Fatal("jira attachments download error = nil, want a refusal to overwrite")
	} else if !strings.Contains(err.Error(), existing) {
		t.Errorf("error = %q, want it to name %s", err, existing)
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "mine" {
		t.Errorf("screen.png = %q (error %v), want it untouched", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "diagram.png")); !os.IsNotExist(err) {
		t.Errorf("Stat(diagram.png) error = %v, want nothing written at all", err)
	}
	if len(*content) != 0 {
		t.Errorf("content requests = %v, want none once a collision is known", *content)
	}
}

func TestJiraAttachmentsDownloadRefusesFilenamesThatCollideIgnoringCase(t *testing.T) {
	tests := []struct {
		name      string
		filenames []string
		// wantHint is the way out the error offers, which differs: no glob
		// can pick one of two identical filenames.
		wantHint string
	}{
		{
			name:      "the same filename twice",
			filenames: []string{"screen.png", "screen.png"},
			wantHint:  "cannot be told apart by --pattern",
		},
		{
			// One file on a case-insensitive filesystem, where the second
			// write would fail only after the first had landed.
			name:      "filenames differing only in case",
			filenames: []string{"a.png", "A.png"},
			wantHint:  "narrow --pattern to pick one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			attachments := []jiraAttachment{{filename: "other.png", mimeType: "image/png", size: 1}}
			for _, f := range tt.filenames {
				attachments = append(attachments, jiraAttachment{filename: f, mimeType: "image/png", size: 1})
			}
			content, handler := jiraAttachmentsAPI(t, attachments...)
			startAPI(t, handler)

			dir := filepath.Join(t.TempDir(), "assets")
			_, err := runJiraDownload(t, dir, "*.png")
			if err == nil {
				t.Fatal("jira attachments download error = nil, want the colliding filenames refused")
			}
			for _, f := range tt.filenames {
				if !strings.Contains(err.Error(), f) {
					t.Errorf("error = %q, want it to name %s", err, f)
				}
			}
			if !strings.Contains(err.Error(), tt.wantHint) {
				t.Errorf("error = %q, want it to say %q", err, tt.wantHint)
			}
			if len(*content) != 0 {
				t.Errorf("content requests = %v, want none", *content)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("Stat(-o) error = %v, want nothing written, not even the directory", err)
			}
		})
	}
}

func TestJiraAttachmentsDownloadRejectsAFilenameThatEscapesTheOutputDirectory(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	content, handler := jiraAttachmentsAPI(t, jiraAttachment{filename: "../escaped", mimeType: "text/plain", size: 1})
	startAPI(t, handler)

	dir := filepath.Join(t.TempDir(), "assets")
	// path.Match's * does not cross a slash, so the pattern spells it out.
	_, err := runJiraDownload(t, dir, "*/escaped")
	if err == nil || !strings.Contains(err.Error(), "not a plain filename") {
		t.Fatalf("error = %v, want the path guard to refuse the filename", err)
	}
	if len(*content) != 0 {
		t.Errorf("content requests = %v, want none", *content)
	}
}
