package cmd

import (
	"encoding/json"
	"fmt"
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
		"version": map[string]any{"number": 7},
		"body":    map[string]any{"storage": map[string]any{"representation": "storage", "value": body}},
		"_links":  map[string]any{"webui": webui},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(payload)
}

// runRead runs `read`, leaving --output off when outPath is empty so the
// default naming is exercised.
func runRead(t *testing.T, arg, outPath string, extra ...string) (string, error) {
	t.Helper()

	args := []string{"read"}
	if outPath != "" {
		args = append(args, "--output", outPath)
	}
	args = append(args, extra...)
	return runCflio(t, append(args, arg)...)
}

func TestReadWritesTheBodyByteForByte(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// No trailing newline, entities left encoded, a macro intact: the file
	// has to match the API's bytes exactly or the round-trip is not lossless.
	body := `<p>a &amp; b &lt;c&gt;</p><ac:structured-macro ac:name="info"><ac:rich-text-body><p>note</p></ac:rich-text-body></ac:structured-macro>`

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("body-format"); got != "storage" {
			t.Errorf("body-format = %q, want storage", got)
		}
		_, _ = w.Write([]byte(pageResponse(t, body, testPageWebUI)))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	output, err := runRead(t, testPageURL, path)
	if err != nil {
		t.Fatalf("read error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", testPageWebUI)))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, testPageURL, path); err != nil {
		t.Fatalf("read error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", testPageWebUI)))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, "123456", path); err != nil {
		t.Fatalf("read error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", "")))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, "123456", path); err != nil {
		t.Fatalf("read error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", testPageWebUI)))
	})

	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := runRead(t, "123456", ""); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "123456.xml")); err != nil {
		t.Errorf("Stat(123456.xml) error = %v, want the default output file", err)
	}
}

func TestReadJSONOutputCarriesMetadataNotTheBody(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>secret body</p>", testPageWebUI)))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	output, err := runRead(t, testPageURL, path, "--format", "json")
	if err != nil {
		t.Fatalf("read error = %v", err)
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
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called for an unparseable argument")
	})

	if _, err := runRead(t, "https://example.atlassian.net/wiki/x/AbCdEf", ""); err == nil {
		t.Fatal("read error = nil, want an error for a short link")
	}
}

func TestReadFromAnUnregisteredSiteNamesTheHost(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the API was called for an unregistered site")
	})

	_, err := runRead(t, "https://unknown.atlassian.net/wiki/spaces/DEV/pages/1/T", "")
	if err == nil {
		t.Fatal("read error = nil, want an error")
	}
	for _, want := range []string{"unknown.atlassian.net", "example", "cflio auth login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestReadDropsAStaleSidecarBeforeWritingTheBody(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// A sidecar from a previous read of a different page. If it survived a
	// failed read of page 123456, the file would hold one page's body next
	// to another page's metadata, and `update` would write it to the wrong
	// page.
	path := filepath.Join(t.TempDir(), "page.xml")
	stale := sidecar.Meta{
		PageID:  "999",
		Version: 3,
		Title:   "Some Other Page",
		Status:  "current",
		PageURL: testSite + "/spaces/DEV/pages/999/Other",
	}
	if err := sidecar.Write(path, stale); err != nil {
		t.Fatalf("sidecar.Write() error = %v", err)
	}

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>new</p>", testPageWebUI)))
	})

	if _, err := runRead(t, "123456", path); err != nil {
		t.Fatalf("read error = %v", err)
	}

	meta, err := sidecar.Load(path)
	if err != nil {
		t.Fatalf("sidecar.Load() error = %v", err)
	}
	if meta.PageID != "123456" {
		t.Errorf("sidecar page_id = %q, want the newly read page", meta.PageID)
	}
}

func TestReadMarkdownConvertsTheBodyAndWritesNoSidecar(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	body := `<h1>Release notes</h1><p>Ping <ac:link><ri:user ri:account-id="acc-123"/></ac:link>.</p>`

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		// The conversion is local: the request is unchanged.
		if got := r.URL.Query().Get("body-format"); got != "storage" {
			t.Errorf("body-format = %q, want storage", got)
		}
		_, _ = w.Write([]byte(pageResponse(t, body, testPageWebUI)))
	})

	path := filepath.Join(t.TempDir(), "page.md")
	output, err := runRead(t, testPageURL, path, "--markdown")
	if err != nil {
		t.Fatalf("read error = %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "# Release notes\n\nPing @acc-123.\n"
	if string(written) != want {
		t.Errorf("file = %q, want the converted Markdown %q", written, want)
	}

	// No sidecar means `update` refuses the file, which is the only thing
	// keeping a converted body from being written back over the page.
	if _, err := os.Stat(sidecar.Path(path)); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) = %v, want no sidecar in Markdown mode", sidecar.Path(path), err)
	}
	if strings.Contains(output, "Sidecar:") {
		t.Errorf("output = %q, want no sidecar line", output)
	}
	if !strings.Contains(output, fmt.Sprintf("%d bytes", len(written))) {
		t.Errorf("output = %q, want it to count the %d converted bytes", output, len(written))
	}
}

func TestReadMarkdownDefaultsTheOutputPathToAMarkdownFile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", testPageWebUI)))
	})

	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := runRead(t, "123456", "", "--markdown"); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "123456.md")); err != nil {
		t.Errorf("Stat(123456.md) error = %v, want the default output file", err)
	}
	// The extensions differ so reading a page both ways leaves two files
	// rather than one overwriting the other.
	if _, err := os.Stat(filepath.Join(dir, "123456.xml")); !os.IsNotExist(err) {
		t.Errorf("Stat(123456.xml) = %v, want the storage default left untouched", err)
	}
}

func TestReadMarkdownDropsAStaleSidecar(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	// Converting over a file that was previously read in storage mode must
	// not leave the old sidecar behind: `update` would then send Markdown to
	// the page it names.
	path := filepath.Join(t.TempDir(), "page.md")
	stale := sidecar.Meta{
		PageID:  "123456",
		Version: 7,
		Title:   "Some Page",
		Status:  "current",
		PageURL: testPageURL,
	}
	if err := sidecar.Write(path, stale); err != nil {
		t.Fatalf("sidecar.Write() error = %v", err)
	}

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>hi</p>", testPageWebUI)))
	})

	if _, err := runRead(t, "123456", path, "--markdown"); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if _, err := os.Stat(sidecar.Path(path)); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) = %v, want the stale sidecar removed", sidecar.Path(path), err)
	}
}

func TestReadMarkdownReportsWhatItCouldNotConvert(t *testing.T) {
	body := `<p>text</p>` +
		`<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">A-1</ac:parameter></ac:structured-macro>` +
		`<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">A-2</ac:parameter></ac:structured-macro>` +
		`<ac:adf-extension><ac:adf-node type="panel"/></ac:adf-extension>`

	t.Run("text output", func(t *testing.T) {
		isolateConfig(t)
		seedProfile(t, "example", testSite)

		startAPI(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(pageResponse(t, body, testPageWebUI)))
		})

		output, err := runRead(t, "123456", filepath.Join(t.TempDir(), "page.md"), "--markdown")
		if err != nil {
			t.Fatalf("read error = %v", err)
		}
		// The reader decides whether to re-read in storage mode from this
		// line, before opening the file.
		if want := "Degraded: 3 (adf-extension, jira)"; !strings.Contains(output, want) {
			t.Errorf("output = %q, want it to contain %q", output, want)
		}
	})

	t.Run("json output", func(t *testing.T) {
		isolateConfig(t)
		seedProfile(t, "example", testSite)

		startAPI(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(pageResponse(t, body, testPageWebUI)))
		})

		output, err := runRead(t, "123456", filepath.Join(t.TempDir(), "page.md"),
			"--markdown", "--format", "json")
		if err != nil {
			t.Fatalf("read error = %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
		}
		if got["unsupported_count"] != float64(3) {
			t.Errorf("unsupported_count = %v, want 3", got["unsupported_count"])
		}
		if fmt.Sprint(got["unsupported"]) != "[adf-extension jira]" {
			t.Errorf("unsupported = %v, want the distinct names sorted", got["unsupported"])
		}
		if _, ok := got["sidecar_path"]; ok {
			t.Errorf("output = %v, want no sidecar_path in Markdown mode", got)
		}
	})
}

func TestReadMarkdownReportsNothingWhenTheConversionIsClean(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pageResponse(t, "<p>plain</p>", testPageWebUI)))
	})

	output, err := runRead(t, "123456", filepath.Join(t.TempDir(), "page.md"),
		"--markdown", "--format", "json")
	if err != nil {
		t.Fatalf("read error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("Unmarshal(output) error = %v; output = %q", err, output)
	}
	for _, key := range []string{"unsupported", "unsupported_count"} {
		if _, ok := got[key]; ok {
			t.Errorf("output = %v, want %q omitted for a faithful conversion", got, key)
		}
	}
}

func TestReadWritesNothingWhenTheAPIFails(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	startAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"title":"Not Found"}]}`))
	})

	path := filepath.Join(t.TempDir(), "page.xml")
	if _, err := runRead(t, testPageURL, path); err == nil {
		t.Fatal("read error = nil, want an error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat(%s) = %v, want no file written on failure", path, err)
	}
}
