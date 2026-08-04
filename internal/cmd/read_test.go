package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/sidecar"
)

// pageResponse renders a GET /pages/{id} body with the given storage value.
func pageResponse(t *testing.T, body, webui string) string {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"id":      "123456",
		"status":  "current",
		"title":   "Some Page",
		"spaceId": "789",
		"version": map[string]any{"number": 7},
		"body":    map[string]any{"storage": map[string]any{"representation": "storage", "value": body}},
		"_links":  map[string]any{"webui": webui},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(payload)
}

func runRead(t *testing.T, arg, outPath string) (string, error) {
	t.Helper()

	original := readOutputFlag
	readOutputFlag = outPath
	t.Cleanup(func() { readOutputFlag = original })

	cmd, out := newTestCommand(t)
	err := runReadPage(cmd, []string{arg})
	return out.String(), err
}

func TestReadWritesTheBodyByteForByte(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	// No trailing newline, entities left encoded, a macro intact: the file
	// has to match the API's bytes exactly or the round-trip is not lossless.
	body := `<p>a &amp; b &lt;c&gt;</p><ac:structured-macro ac:name="info"><ac:rich-text-body><p>note</p></ac:rich-text-body></ac:structured-macro>`

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("body-format"); got != "storage" {
			t.Errorf("body-format = %q, want storage", got)
		}
		_, _ = w.Write([]byte(pageResponse(t, body, "/spaces/DEV/pages/123456/Some+Page")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	output, err := runRead(t, testPageURL, path)
	if err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(written) != body {
		t.Errorf("file = %q, want it byte-identical to the API's body %q", written, body)
	}
	if strings.Contains(output, "&amp;") || strings.Contains(output, "structured-macro") {
		t.Errorf("output = %q, want the body kept off stdout", output)
	}
	for _, want := range []string{"Some Page", "7", path, sidecar.Path(path)} {
		if !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to mention %q", output, want)
		}
	}
}

func TestReadWritesTheSidecar(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", "/spaces/DEV/pages/123456/Some+Page")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, testPageURL, path); err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}

	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	want := sidecar.Meta{
		PageID:  "123456",
		Version: 7,
		Title:   "Some Page",
		Status:  "current",
		PageURL: testPageURL,
	}
	if meta != want {
		t.Errorf("sidecar = %+v, want %+v", meta, want)
	}
}

func TestReadByPageIDStillProducesAnUpdatableSidecar(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", "/spaces/DEV/pages/123456/Some+Page")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, "123456", path); err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}

	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	// The URL is what `update` resolves a profile from, so an ID-only read
	// still has to produce one.
	if meta.PageURL != testPageURL {
		t.Errorf("page_url = %q, want it built from the profile site and the API link", meta.PageURL)
	}
}

func TestReadFallsBackWhenTheAPIReturnsNoWebLink(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", "")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, "123456", path); err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}

	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	want := testSite + "/pages/viewpage.action?pageId=123456"
	if meta.PageURL != want {
		t.Errorf("page_url = %q, want the page-id fallback %q", meta.PageURL, want)
	}
}

func TestReadDefaultsTheOutputPathToThePageID(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", "/spaces/DEV/pages/123456/Some+Page")))
	})

	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := runRead(t, "123456", ""); err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "123456.xml")); err != nil {
		t.Errorf("Stat(123456.xml) error = %v, want the default output file", err)
	}
}

func TestReadJSONOutputCarriesMetadataNotTheBody(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "json")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>secret body</p>", "/spaces/DEV/pages/123456/Some+Page")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	output, err := runRead(t, testPageURL, path)
	if err != nil {
		t.Fatalf("runReadPage() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
	}
	if got["title"] != "Some Page" || got["page_id"] != "123456" {
		t.Errorf("output = %v, want the page metadata", got)
	}
	if strings.Contains(output, "secret body") {
		t.Errorf("output = %q, want the body kept off stdout", output)
	}
}

func TestReadRejectsUnrecognizedArguments(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called for an unparseable argument")
	})

	if _, err := runRead(t, "https://example.atlassian.net/wiki/x/AbCdEf", ""); err == nil {
		t.Fatal("runReadPage() error = nil, want an error for a short link")
	}
}

func TestReadFromAnUnregisteredSiteNamesTheHost(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called for an unregistered site")
	})

	_, err := runRead(t, "https://unknown.atlassian.net/wiki/spaces/DEV/pages/1/T", "")
	if err == nil {
		t.Fatal("runReadPage() error = nil, want an error")
	}
	for _, want := range []string{"unknown.atlassian.net", "example", "cflio auth login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestReadWritesNothingWhenTheAPIFails(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"title":"Not Found"}]}`))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, testPageURL, path); err == nil {
		t.Fatal("runReadPage() error = nil, want an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) = %v, want no file written on failure", path, err)
	}
}
