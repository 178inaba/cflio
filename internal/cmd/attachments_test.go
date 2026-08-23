package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is a real PNG signature plus a byte that is not valid UTF-8 on its
// own, so a download that decoded or re-encoded the body would corrupt it
// visibly rather than passing by luck.
var pngBytes = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0x00, 0x42}

// attachment is one entry of the listing a test serves.
type attachment struct {
	title     string
	mediaType string
	size      int
}

// attachmentsAPI answers the listing with the given attachments and every
// download with pngBytes, counting the downloads.
//
// Both paths are matched exactly, so a request the command should not be
// making at all fails the test rather than being quietly answered. The
// download path is the one the client builds from `downloadLink`, which is why
// the listing hands out links in the API's own shape.
func attachmentsAPI(t *testing.T, attachments ...attachment) (*int, http.HandlerFunc) {
	t.Helper()

	const (
		listPath     = "/wiki/api/v2/pages/123456/attachments"
		downloadBase = "/wiki/rest/api/content/123456/child/attachment/"
	)

	downloads := 0
	return &downloads, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == listPath:
			results := make([]map[string]any, 0, len(attachments))
			for i, a := range attachments {
				id := fmt.Sprintf("att%d", i+1)
				results = append(results, map[string]any{
					"id":           id,
					"title":        a.title,
					"mediaType":    a.mediaType,
					"fileSize":     a.size,
					"downloadLink": "/rest/api/content/123456/child/attachment/" + id + "/download",
				})
			}
			payload, err := json.Marshal(map[string]any{"results": results, "_links": map[string]any{}})
			if err != nil {
				t.Errorf("Marshal() error = %v", err)
				return
			}
			_, _ = w.Write(payload)
		case strings.HasPrefix(r.URL.Path, downloadBase) && strings.HasSuffix(r.URL.Path, "/download"):
			downloads++
			_, _ = w.Write(pngBytes)
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
	}
}

func runAttachmentsListCmd(t *testing.T, arg string, limit int, extra ...string) (cflioRun, error) {
	t.Helper()
	return runLimitCmd(t, "attachments list", arg, limit, extra...)
}

func TestAttachmentsListShowsFilenameMediaTypeAndSize(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t,
		attachment{title: "screenshot.png", mediaType: "image/png", size: 46982},
		attachment{title: "spec.pdf", mediaType: "application/pdf", size: 3841203},
	)
	startAPI(t, handler)

	run, err := runCflio(t, "attachments", "list", testPageURL)
	if err != nil {
		t.Fatalf("attachments list error = %v", err)
	}

	for _, want := range []string{
		"screenshot.png", "image/png", "46982",
		"spec.pdf", "application/pdf", "3841203",
	} {
		if !strings.Contains(run.stdout, want) {
			t.Errorf("output = %q, want it to contain %q", run.stdout, want)
		}
	}
	// The API's attachment id addresses the download and disambiguates
	// nothing in the listing, so it is not shown.
	if strings.Contains(run.stdout, "att1") {
		t.Errorf("output = %q, want the attachment id left out", run.stdout)
	}
}

func TestAttachmentsListJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t, attachment{title: "screenshot.png", mediaType: "image/png", size: 46982})
	startAPI(t, handler)

	run, err := runCflio(t, "attachments", "list", testPageURL, "--format", "json")
	if err != nil {
		t.Fatalf("attachments list error = %v", err)
	}

	var got struct {
		Attachments []struct {
			Filename  string `json:"filename"`
			MediaType string `json:"media_type"`
			FileSize  int64  `json:"file_size"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want the single attachment", got.Attachments)
	}
	if a := got.Attachments[0]; a.Filename != "screenshot.png" || a.MediaType != "image/png" || a.FileSize != 46982 {
		t.Errorf("attachment = %+v, want screenshot.png/image/png/46982", a)
	}
}

func TestAttachmentsListWithNoAttachments(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t)
	startAPI(t, handler)

	run, err := runCflio(t, "attachments", "list", testPageURL)
	if err != nil {
		t.Fatalf("attachments list error = %v, want a page with no attachments to succeed", err)
	}
	if !strings.Contains(run.stdout, "No attachments.") {
		t.Errorf("output = %q, want it to say there are none", run.stdout)
	}
}

func TestAttachmentsListReportsTruncationWithoutACount(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t,
		attachment{title: "one.png", mediaType: "image/png", size: 1},
		attachment{title: "two.png", mediaType: "image/png", size: 2},
	)
	startAPI(t, handler)

	run, err := runAttachmentsListCmd(t, testPageURL, 1)
	if err != nil {
		t.Fatalf("attachments list error = %v", err)
	}
	if !strings.Contains(run.stdout, "--limit") {
		t.Errorf("output = %q, want a truncation notice pointing at --limit", run.stdout)
	}
	// v2 pages by cursor and reports no total, so no count is claimed.
	if strings.Contains(run.stdout, "1 more") {
		t.Errorf("output = %q, want no invented remaining count", run.stdout)
	}
	if strings.Contains(run.stdout, "two.png") {
		t.Errorf("output = %q, want only the first attachment at --limit 1", run.stdout)
	}
}

func TestAttachmentsListAcceptsABarePageID(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	var gotPath string
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"results":[],"_links":{}}`))
	})

	if _, err := runCflio(t, "attachments", "list", "123456"); err != nil {
		t.Fatalf("attachments list error = %v", err)
	}
	if gotPath != "/wiki/api/v2/pages/123456/attachments" {
		t.Errorf("path = %q, want the page id from the bare argument", gotPath)
	}
}

func TestAttachmentsListRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite an invalid --limit")
	})

	if _, err := runAttachmentsListCmd(t, testPageURL, 0); err == nil {
		t.Error("attachments list error = nil for --limit 0, want an error")
	}
}

// runDownload runs `attachments download` with --pattern and -o set.
func runDownload(t *testing.T, dir, pattern string, extra ...string) (cflioRun, error) {
	t.Helper()

	args := append([]string{"attachments", "download", testPageURL, "--pattern", pattern, "-o", dir}, extra...)
	return runCflio(t, args...)
}

func TestAttachmentsDownloadWritesMatchingFilesUnchanged(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	downloads, handler := attachmentsAPI(t,
		attachment{title: "screenshot.png", mediaType: "image/png", size: len(pngBytes)},
		attachment{title: "diagram.png", mediaType: "image/png", size: len(pngBytes)},
		attachment{title: "spec.pdf", mediaType: "application/pdf", size: 9},
	)
	startAPI(t, handler)

	// A directory that does not exist yet, so `-o ./assets` works without the
	// caller having to make it first.
	dir := filepath.Join(t.TempDir(), "assets")
	run, err := runDownload(t, dir, "*.png")
	if err != nil {
		t.Fatalf("attachments download error = %v", err)
	}

	for _, name := range []string{"screenshot.png", "diagram.png"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		if !bytes.Equal(got, pngBytes) {
			t.Errorf("%s = %v, want the served bytes verbatim %v", name, got, pngBytes)
		}
		if !strings.Contains(run.stdout, name) {
			t.Errorf("output = %q, want it to name %s", run.stdout, name)
		}
	}
	// The PDF matched nothing, so it must not have been fetched: the whole
	// point of the required --pattern is not pulling what was not asked for.
	if _, err := os.Stat(filepath.Join(dir, "spec.pdf")); !os.IsNotExist(err) {
		t.Errorf("Stat(spec.pdf) error = %v, want the non-matching attachment left alone", err)
	}
	if *downloads != 2 {
		t.Errorf("downloads = %d, want 2 (only the matches)", *downloads)
	}
}

func TestAttachmentsDownloadJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t, attachment{title: "screenshot.png", mediaType: "image/png", size: len(pngBytes)})
	startAPI(t, handler)

	dir := filepath.Join(t.TempDir(), "assets")
	run, err := runDownload(t, dir, "*.png", "--format", "json")
	if err != nil {
		t.Fatalf("attachments download error = %v", err)
	}

	var got struct {
		Downloaded []struct {
			Filename string `json:"filename"`
			Path     string `json:"path"`
			Bytes    int64  `json:"bytes"`
		} `json:"downloaded_attachments"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, run.stdout)
	}
	if len(got.Downloaded) != 1 {
		t.Fatalf("downloaded_attachments = %+v, want the single file", got.Downloaded)
	}
	want := filepath.Join(dir, "screenshot.png")
	if d := got.Downloaded[0]; d.Filename != "screenshot.png" || d.Path != want || d.Bytes != int64(len(pngBytes)) {
		t.Errorf("downloaded = %+v, want screenshot.png at %s with %d bytes", d, want, len(pngBytes))
	}
}

func TestAttachmentsDownloadDefaultsToTheWorkingDirectory(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t, attachment{title: "screenshot.png", mediaType: "image/png", size: len(pngBytes)})
	startAPI(t, handler)

	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := runCflio(t, "attachments", "download", testPageURL, "--pattern", "*.png"); err != nil {
		t.Fatalf("attachments download error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshot.png")); err != nil {
		t.Errorf("Stat(screenshot.png) error = %v, want the file in the working directory", err)
	}
}

func TestAttachmentsDownloadRefusesToOverwriteBeforeFetchingAnything(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	downloads, handler := attachmentsAPI(t,
		attachment{title: "diagram.png", mediaType: "image/png", size: len(pngBytes)},
		attachment{title: "screenshot.png", mediaType: "image/png", size: len(pngBytes)},
	)
	startAPI(t, handler)

	dir := t.TempDir()
	existing := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := runDownload(t, dir, "*.png"); err == nil {
		t.Fatal("attachments download error = nil, want a refusal to overwrite")
	} else if !strings.Contains(err.Error(), "screenshot.png") {
		t.Errorf("error = %q, want it to name the colliding file", err)
	}
	got, readErr := os.ReadFile(existing)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(got) != "mine" {
		t.Errorf("screenshot.png = %q, want it untouched", got)
	}
	// The collision is found before any transfer, so the attachment that
	// would have succeeded is not written either — a half-applied download is
	// worse than none.
	if _, err := os.Stat(filepath.Join(dir, "diagram.png")); !os.IsNotExist(err) {
		t.Errorf("Stat(diagram.png) error = %v, want nothing written at all", err)
	}
	if *downloads != 0 {
		t.Errorf("downloads = %d, want 0 requests once a collision is known", *downloads)
	}
}

func TestAttachmentsDownloadWithNoMatchFails(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t, attachment{title: "spec.pdf", mediaType: "application/pdf", size: 9})
	startAPI(t, handler)

	dir := t.TempDir()
	if _, err := runDownload(t, dir, "*.png"); err == nil {
		t.Fatal("attachments download error = nil, want a non-matching pattern to fail")
	} else if !strings.Contains(err.Error(), "*.png") {
		t.Errorf("error = %q, want it to name the pattern", err)
	}
}

func TestAttachmentsDownloadOnAPageWithNoAttachmentsFails(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t)
	startAPI(t, handler)

	if _, err := runDownload(t, t.TempDir(), "*"); err == nil {
		t.Fatal("attachments download error = nil, want nothing to match on a page with no attachments")
	}
}

func TestAttachmentsDownloadRequiresThePatternFlag(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite a missing --pattern")
	})

	_, err := runCflio(t, "attachments", "download", testPageURL)
	if err == nil {
		t.Fatal("attachments download error = nil, want the missing flag reported")
	}
	if !strings.Contains(err.Error(), "pattern") {
		t.Errorf("error = %q, want it to name --pattern", err)
	}
}

func TestAttachmentsDownloadRejectsAMalformedPattern(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t, attachment{title: "screenshot.png", mediaType: "image/png", size: 1})
	startAPI(t, handler)

	// An unterminated character class. Left unchecked this looks exactly like
	// a pattern that matched nothing, which is a different problem.
	if _, err := runDownload(t, t.TempDir(), "[png"); err == nil {
		t.Fatal("attachments download error = nil, want a malformed pattern reported")
	} else if !strings.Contains(err.Error(), "[png") {
		t.Errorf("error = %q, want it to name the bad pattern", err)
	}
}

func TestAttachmentsDownloadRejectsAFilenameThatEscapesTheOutputDirectory(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	// The title is server data. A path in it would put the write outside -o.
	downloads, handler := attachmentsAPI(t, attachment{title: "../escaped", mediaType: "text/plain", size: 1})
	startAPI(t, handler)

	dir := filepath.Join(t.TempDir(), "assets")
	// The pattern has to actually match, or the run ends at "no attachment
	// matches" and never reaches the guard this test is about. path.Match's *
	// does not cross a slash, so "*escaped*" would not match "../escaped" —
	// which is also why a title like this is unreachable via a plain "*".
	_, err := runDownload(t, dir, "*/escaped")
	if err == nil {
		t.Fatal("attachments download error = nil, want a filename outside -o refused")
	}
	if !strings.Contains(err.Error(), "not a plain filename") {
		t.Fatalf("error = %q, want the path guard to reject it (not a no-match)", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "..", "escaped")); !os.IsNotExist(statErr) {
		t.Errorf("Stat(../escaped) error = %v, want nothing written outside -o", statErr)
	}
	if *downloads != 0 {
		t.Errorf("downloads = %d, want the guard to refuse before any transfer", *downloads)
	}
}
