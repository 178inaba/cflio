package sidecar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathIsDerivedFromTheBodyFile(t *testing.T) {
	if got, want := Path("page.xml"), "page.xml.meta.json"; got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if got, want := Path("/tmp/dir/page.xml"), "/tmp/dir/page.xml.meta.json"; got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func testMeta() Meta {
	return Meta{
		PageID:  "123456",
		Version: 7,
		Title:   "Some Page",
		Status:  "current",
		PageURL: "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Some+Page",
	}
}

func TestWriteThenLoadRoundTrips(t *testing.T) {
	body := filepath.Join(t.TempDir(), "page.xml")
	want := testMeta()

	if err := Write(body, want); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Load(body)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestWriteRecordsEveryRequiredField(t *testing.T) {
	body := filepath.Join(t.TempDir(), "page.xml")
	if err := Write(body, testMeta()); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	raw, err := os.ReadFile(Path(body))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"page_id", "version", "title", "status", "page_url"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("sidecar is missing the required field %q; got %v", key, fields)
		}
	}
}

func TestLoadMissingSidecarPointsAtAReadThatProducesOne(t *testing.T) {
	body := filepath.Join(t.TempDir(), "page.xml")
	if err := os.WriteFile(body, []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(body)
	if err == nil {
		t.Fatal("Load() error = nil, want an error for a missing sidecar")
	}
	// Writing to a page that was never read is structurally impossible, so
	// the error has to point at the way out. Naming --markdown is what makes
	// that way out reachable: a plain "run `cflio read`" is satisfied by
	// re-running the Markdown read that produced this sidecar-less file, and
	// the caller lands right back here.
	for _, want := range []string{"cflio read", "--markdown", Path(body)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error = %q, want it to mention %q", err, want)
		}
	}
}

func TestLoadRejectsAnIncompleteSidecar(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "corrupt json", content: "{not json", want: "parse"},
		{name: "missing page id", content: `{"version":7,"title":"T","status":"current","page_url":"https://x/y"}`, want: "page_id"},
		{name: "missing version", content: `{"page_id":"1","title":"T","status":"current","page_url":"https://x/y"}`, want: "version"},
		{name: "missing title", content: `{"page_id":"1","version":7,"status":"current","page_url":"https://x/y"}`, want: "title"},
		{name: "missing status", content: `{"page_id":"1","version":7,"title":"T","page_url":"https://x/y"}`, want: "status"},
		{name: "missing page url", content: `{"page_id":"1","version":7,"title":"T","status":"current"}`, want: "page_url"},
		// A host-less page_url would resolve to the default profile, which
		// is exactly the fallback that is not supposed to exist.
		{
			name:    "page url without a host",
			content: `{"page_id":"1","version":7,"title":"T","status":"current","page_url":"just-a-title"}`,
			want:    "page_url",
		},
		{
			name:    "unparseable page url",
			content: `{"page_id":"1","version":7,"title":"T","status":"current","page_url":"://nope"}`,
			want:    "page_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := filepath.Join(t.TempDir(), "page.xml")
			if err := os.WriteFile(Path(body), []byte(tt.content), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, err := Load(body)
			if err == nil {
				t.Fatalf("Load() error = nil, want an error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	body := filepath.Join(t.TempDir(), "page.xml")

	// Removing when there is nothing to remove is the normal first-read
	// case, so it must not be an error.
	if err := Remove(body); err != nil {
		t.Fatalf("Remove() on a missing sidecar error = %v, want nil", err)
	}

	if err := Write(body, testMeta()); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := Remove(body); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(Path(body)); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) = %v, want the sidecar gone", Path(body), err)
	}
}

func TestWriteOverwritesAnExistingSidecar(t *testing.T) {
	// `update` rewrites the version in place so the next edit-update cycle
	// works without a fresh read.
	body := filepath.Join(t.TempDir(), "page.xml")
	if err := Write(body, testMeta()); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	updated := testMeta()
	updated.Version = 8
	if err := Write(body, updated); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	got, err := Load(body)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Version != 8 {
		t.Errorf("version = %d, want 8", got.Version)
	}
}
