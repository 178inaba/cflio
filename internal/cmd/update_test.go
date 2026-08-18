package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/sidecar"
)

const testPageURL = testSite + testPageWebUI

// seedReadPage writes a body file and its sidecar, as `cflio read` would.
func seedReadPage(t *testing.T, body string, meta sidecar.Meta) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "page.xml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := sidecar.Write(path, meta); err != nil {
		t.Fatalf("sidecar.Write() error = %v", err)
	}
	return path
}

func currentMeta() sidecar.Meta {
	return sidecar.Meta{
		PageID:  "123456",
		Version: 7,
		Title:   "Some Page",
		Status:  "current",
		PageURL: testPageURL,
	}
}

// updateStub answers the pre-flight GET with serverVersion and records the
// PUT payload it receives, if any.
type updateStub struct {
	serverVersion int
	serverStatus  string
	puts          []confluence.UpdatePageRequest
}

func (s *updateStub) handler(t *testing.T) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			status := s.serverStatus
			if status == "" {
				status = "current"
			}
			_, _ = fmt.Fprintf(w, `{"id":"123456","status":%q,"title":"Some Page","version":{"number":%d}}`,
				status, s.serverVersion)
			return
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read PUT body: %v", err)
			return
		}
		var req confluence.UpdatePageRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("Unmarshal(PUT body) error = %v", err)
			return
		}
		s.puts = append(s.puts, req)

		_, _ = fmt.Fprintf(w, `{"id":"123456","status":"current","title":"Some Page","version":{"number":%d}}`,
			req.Version.Number)
	}
}

func runUpdate(t *testing.T, path string, extra ...string) (cflioRun, error) {
	t.Helper()

	args := []string{"update", "-f", path}
	return runCflio(t, append(args, extra...)...)
}

func TestUpdateSendsTheFileBackUnchanged(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// A body with a macro and characters JSON escaping would mangle.
	body := `<p>a &amp; b</p><ac:structured-macro ac:name="info"><ac:rich-text-body><p>note</p></ac:rich-text-body></ac:structured-macro>`
	path := seedReadPage(t, body, currentMeta())

	stub := &updateStub{serverVersion: 7}
	startAPI(t, stub.handler(t))

	run, err := runUpdate(t, path)
	if err != nil {
		t.Fatalf("update error = %v", err)
	}

	if len(stub.puts) != 1 {
		t.Fatalf("PUT requests = %d, want 1", len(stub.puts))
	}
	put := stub.puts[0]
	if put.Body.Value != body {
		t.Errorf("PUT body = %q, want the file's bytes unchanged (%q)", put.Body.Value, body)
	}
	if put.Body.Representation != "storage" {
		t.Errorf("representation = %q, want storage", put.Body.Representation)
	}
	if put.Version.Number != 8 {
		t.Errorf("version = %d, want the sidecar's 7 plus one", put.Version.Number)
	}
	if put.Version.Message != "Updated via cflio" {
		t.Errorf("version message = %q, want the default", put.Version.Message)
	}
	if put.Title != "Some Page" || put.Status != "current" || put.ID != "123456" {
		t.Errorf("PUT = %+v, want the sidecar's id, title and status", put)
	}
	if strings.Contains(run.stdout, body) {
		t.Errorf("output = %q, want the body kept off stdout", run.stdout)
	}
}

func TestUpdateUsesTheMessageFlag(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	path := seedReadPage(t, "<p>hi</p>", currentMeta())

	stub := &updateStub{serverVersion: 7}
	startAPI(t, stub.handler(t))

	if _, err := runUpdate(t, path, "--message", "Fix the typo"); err != nil {
		t.Fatalf("update error = %v", err)
	}
	if got := stub.puts[0].Version.Message; got != "Fix the typo" {
		t.Errorf("version message = %q, want the --message value", got)
	}
}

// An explicitly blank --message falls back to the default rather than
// recording an empty line in the page's history.
func TestUpdateWithAnEmptyMessageFallsBackToTheDefault(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	path := seedReadPage(t, "<p>hi</p>", currentMeta())

	stub := &updateStub{serverVersion: 7}
	startAPI(t, stub.handler(t))

	if _, err := runUpdate(t, path, "--message="); err != nil {
		t.Fatalf("update error = %v", err)
	}
	if got := stub.puts[0].Version.Message; got != defaultVersionMessage {
		t.Errorf("version message = %q, want the default %q", got, defaultVersionMessage)
	}
}

func TestUpdateRefusesWhenTheServerVersionMoved(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	path := seedReadPage(t, "<p>edited</p>", currentMeta())

	stub := &updateStub{serverVersion: 9}
	startAPI(t, stub.handler(t))

	_, err := runUpdate(t, path)
	if err == nil {
		t.Fatal("update error = nil, want a version-conflict error")
	}
	if len(stub.puts) != 0 {
		t.Errorf("PUT requests = %d, want none once the versions disagree", len(stub.puts))
	}
	for _, want := range []string{"7", "9", "cflio read"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}

	// The sidecar must survive so the caller can re-read over it.
	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	if meta.Version != 7 {
		t.Errorf("sidecar version = %d, want it untouched at 7", meta.Version)
	}
}

func TestUpdateTwiceWithoutReReading(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	path := seedReadPage(t, "<p>first</p>", currentMeta())

	stub := &updateStub{serverVersion: 7}
	startAPI(t, stub.handler(t))

	if _, err := runUpdate(t, path); err != nil {
		t.Fatalf("first update error = %v", err)
	}

	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	if meta.Version != 8 {
		t.Fatalf("sidecar version = %d, want it advanced to the server's 8", meta.Version)
	}

	// Edit again; the server has moved on to the version we just wrote.
	if err := os.WriteFile(path, []byte("<p>second</p>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stub.serverVersion = 8

	if _, err := runUpdate(t, path); err != nil {
		t.Fatalf("second update error = %v", err)
	}
	if len(stub.puts) != 2 {
		t.Fatalf("PUT requests = %d, want 2", len(stub.puts))
	}
	if got := stub.puts[1]; got.Version.Number != 9 || got.Body.Value != "<p>second</p>" {
		t.Errorf("second PUT = %+v, want version 9 with the edited body", got)
	}
}

func TestUpdateRefusesNonCurrentPages(t *testing.T) {
	tests := []struct {
		name          string
		sidecarStatus string
		serverStatus  string
		wantContains  string
	}{
		{
			name:          "sidecar recorded a non-current status",
			sidecarStatus: "archived",
			wantContains:  "archived",
		},
		{
			name:          "page was archived after it was read",
			sidecarStatus: "current",
			serverStatus:  "archived",
			wantContains:  "archived",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			meta := currentMeta()
			meta.Status = tt.sidecarStatus
			path := seedReadPage(t, "<p>hi</p>", meta)

			stub := &updateStub{serverVersion: 7, serverStatus: tt.serverStatus}
			startAPI(t, stub.handler(t))

			_, err := runUpdate(t, path)
			if err == nil {
				t.Fatal("update error = nil, want an error")
			}
			if len(stub.puts) != 0 {
				t.Errorf("PUT requests = %d, want none", len(stub.puts))
			}
			if !strings.Contains(err.Error(), tt.wantContains) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantContains)
			}
		})
	}
}

func TestUpdateWithoutASidecarPointsAtRead(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	path := filepath.Join(t.TempDir(), "page.xml")
	if err := os.WriteFile(path, []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called without a sidecar")
	})

	_, err := runUpdate(t, path)
	if err == nil {
		t.Fatal("update error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "cflio read") {
		t.Errorf("error = %q, want it to point at `cflio read`", err)
	}
}

func TestUpdateResolvesTheProfileFromTheSidecarURL(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	meta := currentMeta()
	meta.PageURL = "https://other.atlassian.net/wiki/spaces/OPS/pages/123456/Other"
	path := seedReadPage(t, "<p>hi</p>", meta)

	var gotToken string
	stub := &updateStub{serverVersion: 7}
	inner := stub.handler(t)
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, token, _ := r.BasicAuth()
		gotToken = token
		inner(w, r)
	})

	if _, err := runUpdate(t, path); err != nil {
		t.Fatalf("update error = %v", err)
	}
	if gotToken == "" {
		t.Fatal("no credentials were sent")
	}
}

func TestUpdateRejectsAConflictingProfileFlag(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	path := seedReadPage(t, "<p>hi</p>", currentMeta())
	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called despite a profile/site conflict")
	})

	_, err := runUpdate(t, path, "--profile", "other")
	if err == nil {
		t.Fatal("update error = nil, want a conflict error")
	}
	for _, want := range []string{"other", "example.atlassian.net"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestUpdateFromAnUnregisteredSiteNamesTheHost(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	meta := currentMeta()
	meta.PageURL = "https://unknown.atlassian.net/wiki/spaces/DEV/pages/123456/T"
	path := seedReadPage(t, "<p>hi</p>", meta)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called for an unregistered site")
	})

	_, err := runUpdate(t, path)
	if err == nil {
		t.Fatal("update error = nil, want an error")
	}
	for _, want := range []string{"unknown.atlassian.net", "example", "cflio auth login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
