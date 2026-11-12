package cmd

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func runAttachmentsListCmd(t *testing.T, arg string, limit int, extra ...string) (cliRun, error) {
	t.Helper()
	return runLimitCmd(t, "confluence attachments list", arg, limit, extra...)
}

func TestAttachmentsListShowsFilenameMediaTypeAndSize(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := attachmentsAPI(t,
		attachment{title: "screenshot.png", mediaType: "image/png", size: 46982},
		attachment{title: "spec.pdf", mediaType: "application/pdf", size: 3841203},
	)
	startAPI(t, handler)

	run, err := runCLI(t, "confluence", "attachments", "list", testPageURL)
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

	run, err := runCLI(t, "confluence", "attachments", "list", testPageURL, "--format", "json")
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

	run, err := runCLI(t, "confluence", "attachments", "list", testPageURL)
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

	if _, err := runCLI(t, "confluence", "attachments", "list", "123456"); err != nil {
		t.Fatalf("attachments list error = %v", err)
	}
	if gotPath != "/wiki/api/v2/pages/123456/attachments" {
		t.Errorf("path = %q, want the page id from the bare argument", gotPath)
	}
}

func TestAttachmentsListRejectsAnOutOfRangeLimit(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, neverCalled(t, "despite an invalid --limit"))

	if _, err := runAttachmentsListCmd(t, testPageURL, 0); err == nil {
		t.Error("attachments list error = nil for --limit 0, want an error")
	}
}

// runDownload runs `confluence attachments download` with --pattern and -o set.
func runDownload(t *testing.T, dir, pattern string, extra ...string) (cliRun, error) {
	t.Helper()

	args := append([]string{"confluence", "attachments", "download", testPageURL, "--pattern", pattern, "-o", dir}, extra...)
	return runCLI(t, args...)
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

	if _, err := runCLI(t, "confluence", "attachments", "download", testPageURL, "--pattern", "*.png"); err != nil {
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

	startAPI(t, neverCalled(t, "despite a missing --pattern"))

	_, err := runCLI(t, "confluence", "attachments", "download", testPageURL)
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

const uploadPath = "/wiki/rest/api/content/123456/child/attachment"

// uploadPart is one part of the multipart body the server received.
type uploadPart struct {
	name, filename, content string
}

// uploadRequest is what the server saw of one upload.
type uploadRequest struct {
	method, path, user, token string
	parts                     []uploadPart
}

// uploadAPI answers every upload with a result per file part it received,
// at version 1 under POST and 2 under PUT, and records the requests.
func uploadAPI(t *testing.T) (*[]uploadRequest, http.HandlerFunc) {
	t.Helper()

	var requests []uploadRequest
	return &requests, func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		req := uploadRequest{
			method: r.Method, path: r.URL.Path, user: user,
			token: r.Header.Get("X-Atlassian-Token"),
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader() error = %v", err)
			return
		}
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("NextPart() error = %v", err)
				return
			}
			content, _ := io.ReadAll(part)
			req.parts = append(req.parts, uploadPart{name: part.FormName(), filename: part.FileName(), content: string(content)})
		}
		requests = append(requests, req)

		version := 1
		if r.Method == http.MethodPut {
			version = 2
		}
		results := []map[string]any{}
		for _, p := range req.parts {
			if p.name != "file" {
				continue
			}
			results = append(results, map[string]any{
				"title":      p.filename,
				"extensions": map[string]any{"mediaType": "image/png", "fileSize": len(p.content)},
				"version":    map[string]any{"number": version},
			})
		}
		payload, err := json.Marshal(map[string]any{"results": results})
		if err != nil {
			t.Errorf("Marshal() error = %v", err)
			return
		}
		_, _ = w.Write(payload)
	}
}

func TestAttachmentsUploadPostsTheFileUnderItsBaseName(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests, handler := uploadAPI(t)
	startAPI(t, handler)

	file := writeTempFile(t, "out/a.png", string(pngBytes))

	if _, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file); err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}

	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
	got := (*requests)[0]
	if got.method != http.MethodPost || got.path != uploadPath {
		t.Errorf("request = %s %s, want POST %s", got.method, got.path, uploadPath)
	}
	if got.user != "a@example.com" {
		t.Errorf("Basic auth user = %q, want the profile's email", got.user)
	}
	if got.token != "nocheck" {
		t.Errorf("X-Atlassian-Token = %q, want nocheck", got.token)
	}
	want := []uploadPart{
		{name: "file", filename: "a.png", content: string(pngBytes)},
		{name: "minorEdit", content: "true"},
	}
	if !slices.Equal(got.parts, want) {
		t.Errorf("parts = %q, want %q", got.parts, want)
	}
}

func TestAttachmentsUploadWithReplaceIsAPut(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests, handler := uploadAPI(t)
	startAPI(t, handler)

	file := writeTempFile(t, "a.png", string(pngBytes))

	if _, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file, "--replace"); err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}

	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(*requests))
	}
	got := (*requests)[0]
	if got.method != http.MethodPut || got.path != uploadPath {
		t.Errorf("request = %s %s, want PUT %s", got.method, got.path, uploadPath)
	}
	want := []uploadPart{
		{name: "file", filename: "a.png", content: string(pngBytes)},
		{name: "minorEdit", content: "true"},
	}
	if got.token != "nocheck" || !slices.Equal(got.parts, want) {
		t.Errorf("token %q parts %q, want nocheck and %q — nothing but the method differs", got.token, got.parts, want)
	}
}

func TestAttachmentsUploadSendsEveryFileInOneRequest(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests, handler := uploadAPI(t)
	startAPI(t, handler)

	a := writeTempFile(t, "dir/a.png", string(pngBytes))
	b := writeTempFile(t, "b.png", "b")

	if _, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, a, b); err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}

	if len(*requests) != 1 {
		t.Fatalf("requests = %d, want both files in 1", len(*requests))
	}
	want := []uploadPart{
		{name: "file", filename: "a.png", content: string(pngBytes)},
		{name: "minorEdit", content: "true"},
		{name: "file", filename: "b.png", content: "b"},
		{name: "minorEdit", content: "true"},
	}
	if got := (*requests)[0].parts; !slices.Equal(got, want) {
		t.Errorf("parts = %q, want %q", got, want)
	}
}

func TestAttachmentsUploadListsWhatTheServerStored(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := uploadAPI(t)
	startAPI(t, handler)

	file := writeTempFile(t, "a.png", string(pngBytes))

	res, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file, "--replace")
	if err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}
	want := fmt.Sprintf("- **a.png** (image/png, %d bytes, version 2)\n", len(pngBytes))
	if res.stdout != want {
		t.Errorf("stdout = %q, want %q", res.stdout, want)
	}
}

func TestAttachmentsUploadJSONOutput(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	_, handler := uploadAPI(t)
	startAPI(t, handler)

	file := writeTempFile(t, "a.png", string(pngBytes))

	res, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file, "--format", "json")
	if err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}

	var got struct {
		Uploaded []map[string]any `json:"uploaded_attachments"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", res.stdout, err)
	}
	want := map[string]any{
		"filename": "a.png", "media_type": "image/png",
		"file_size": float64(len(pngBytes)), "version": float64(1),
	}
	if len(got.Uploaded) != 1 || !maps.Equal(got.Uploaded[0], want) {
		t.Errorf("uploaded_attachments = %v, want [%v]", got.Uploaded, want)
	}
}

func TestAttachmentsUploadAcceptsABarePageID(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests, handler := uploadAPI(t)
	startAPI(t, handler)

	file := writeTempFile(t, "a.png", string(pngBytes))

	if _, err := runCLI(t, "confluence", "attachments", "upload", "123456", file); err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}
	if len(*requests) != 1 || (*requests)[0].path != uploadPath {
		t.Errorf("requests = %+v, want one to %s", *requests, uploadPath)
	}
}

func TestAttachmentsUploadAcceptsASpaceOverviewURL(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	requests, handler := uploadAPI(t)
	startAPI(t, withSpaceLookup(t, handler))

	file := writeTempFile(t, "a.png", string(pngBytes))

	if _, err := runCLI(t, "confluence", "attachments", "upload", testSpaceOverviewURL, file); err != nil {
		t.Fatalf("attachments upload error = %v", err)
	}
	if len(*requests) != 1 || (*requests)[0].path != uploadPath {
		t.Errorf("requests = %+v, want one to the homepage's %s", *requests, uploadPath)
	}
}

func TestAttachmentsUploadPointsAtReplaceWhenThePostIsRefused(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"statusCode":400,"message":"com.atlassian.confluence.api.service.exceptions.BadRequestException: `+
			`Cannot add a new attachment with same file name as an existing attachment: a.png"}`)
	})

	file := writeTempFile(t, "a.png", string(pngBytes))

	_, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file)
	if err == nil {
		t.Fatal("attachments upload error = nil, want the refusal")
	}
	if !strings.Contains(err.Error(), "same file name as an existing attachment: a.png") {
		t.Errorf("error = %q, want the server's message", err)
	}
	if !strings.Contains(err.Error(), "--replace") {
		t.Errorf("error = %q, want it to point at --replace", err)
	}
	if code, _ := describeFailure(err, time.Minute); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestAttachmentsUploadChecksEveryFileBeforeSending(t *testing.T) {
	// Each in a directory of its own, so no path is a prefix of another and a
	// problem cannot be found in the message by way of a different one.
	dir := t.TempDir()
	valid := writeTempFile(t, "a.png", string(pngBytes))
	missing := filepath.Join(t.TempDir(), "missing.png")
	sameName := writeTempFile(t, "other/a.png", "x")

	isMissing := missing
	isDir := dir + " is a directory"
	collides := valid + ", " + sameName + " would all be attached as a.png"

	tests := []struct {
		name  string
		files []string
		// want lists one fragment per problem, each of which must appear
		// exactly once.
		want []string
	}{
		{name: "a missing file", files: []string{missing}, want: []string{isMissing}},
		{name: "a directory", files: []string{dir}, want: []string{isDir}},
		{name: "two files with the same base name", files: []string{valid, sameName}, want: []string{collides}},
		{
			name:  "one file given twice",
			files: []string{valid, valid},
			want:  []string{valid + ", " + valid + " would all be attached as a.png"},
		},
		{name: "a valid file beside a missing one", files: []string{valid, missing}, want: []string{isMissing}},
		{
			name:  "every problem at once",
			files: []string{missing, dir, valid, sameName},
			want:  []string{isMissing, isDir, collides},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			startAPI(t, neverCalled(t, "despite a file that cannot be uploaded"))

			args := append([]string{"confluence", "attachments", "upload", testPageURL}, tt.files...)
			_, err := runCLI(t, args...)
			if err == nil {
				t.Fatal("attachments upload error = nil, want the files rejected")
			}
			for _, fragment := range tt.want {
				if n := strings.Count(err.Error(), fragment); n != 1 {
					t.Errorf("error = %q, want %q in it once (got %d)", err, fragment, n)
				}
			}
		})
	}
}

func TestAttachmentsUploadRejectsAFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a mode of 0 does not deny reading on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, neverCalled(t, "despite a file that cannot be read"))

	unreadable := writeTempFile(t, "a.png", string(pngBytes))
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	_, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, unreadable)
	if err == nil {
		t.Fatal("attachments upload error = nil, want the unreadable file rejected")
	}
	if !strings.Contains(err.Error(), "open "+unreadable) {
		t.Errorf("error = %q, want it to name %s", err, unreadable)
	}
}

func TestAttachmentsUploadRequiresAFile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	startAPI(t, neverCalled(t, "with no file to upload"))

	if _, err := runCLI(t, "confluence", "attachments", "upload", testPageURL); err == nil {
		t.Fatal("attachments upload error = nil, want a usage error")
	}
}

// TestAttachmentsUploadReportsADeadlineThatExpiresMidUpload is the upload's
// side of exit code 124: the deadline lands while the body is still being
// sent, where the pipe the body streams through fails too, and that failure
// must not stand in for the deadline describeFailure reads.
func TestAttachmentsUploadReportsADeadlineThatExpiresMidUpload(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// Released by the test: a server that has not read the body to its end
	// never sees the client go away, so r.Context() would not end it.
	release := make(chan struct{})
	var served atomic.Bool
	startAPI(t, func(_ http.ResponseWriter, _ *http.Request) {
		served.Store(true)
		<-release
	})
	defer close(release)

	// Large enough that the body cannot all sit in the socket buffers while
	// the server stalls without reading it.
	file := writeTempFile(t, "big.bin", string(make([]byte, 16<<20)))

	_, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, file, "--timeout=300ms")

	if !served.Load() {
		t.Fatal("the request never reached the server, so the upload path was not exercised")
	}
	if code, _ := describeFailure(err, 300*time.Millisecond); code != timeoutExitCode {
		t.Errorf("exit code = %d for %v, want %d", code, err, timeoutExitCode)
	}
	// Cut short mid-body, the upload cannot have landed, so there is nothing
	// to check before running it again.
	if err != nil && strings.Contains(err.Error(), "attachments list") {
		t.Errorf("error = %q, want no may-have-landed note for a body that was not all sent", err)
	}
}

// TestAttachmentsUploadSaysWhenTheUploadMayHaveLanded covers the outcomes of
// the upload: when Confluence may have stored the files, the error names the
// check to make before uploading them again.
func TestAttachmentsUploadSaysWhenTheUploadMayHaveLanded(t *testing.T) {
	for _, fc := range creatingFailures {
		t.Run(fc.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			fail, release := failCreating(fc)
			defer release()
			startAPI(t, fail)

			_, err := runCLI(t, "confluence", "attachments", "upload", testPageURL,
				creatingBody(t, fc), fc.timeoutFlag())

			assertCreatingFailure(t, err, fc, uploadCheckHint)
			// --replace answers a 400 refusing a name the page already has,
			// not a request whose outcome is unknown.
			if fc.wantHint && strings.Contains(err.Error(), "--replace") {
				t.Errorf("error = %v, want no --replace hint", err)
			}
		})
	}
}

func TestAttachmentsListAcceptsASpaceOverviewURL(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	_, handler := attachmentsAPI(t, attachment{title: "logo.png", mediaType: "image/png", size: 3})
	startAPI(t, withSpaceLookup(t, handler))

	run, err := runAttachmentsListCmd(t, testSpaceOverviewURL, 100)
	if err != nil {
		t.Fatalf("attachments list error = %v", err)
	}
	if !strings.Contains(run.stdout, "logo.png") {
		t.Errorf("output = %q, want the homepage's attachments", run.stdout)
	}
}

func TestAttachmentsDownloadAcceptsASpaceOverviewURL(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	downloads, handler := attachmentsAPI(t, attachment{title: "logo.png", mediaType: "image/png", size: len(pngBytes)})
	startAPI(t, withSpaceLookup(t, handler))

	if _, err := runCLI(t, "confluence", "attachments", "download", testSpaceOverviewURL,
		"--pattern", "*.png", "-o", t.TempDir()); err != nil {
		t.Fatalf("attachments download error = %v", err)
	}
	if *downloads != 1 {
		t.Errorf("downloads = %d, want the homepage's one attachment", *downloads)
	}
}
