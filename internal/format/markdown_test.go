package format

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// updateGolden rewrites the expected files from the converter's own output.
// Run `go test ./internal/format -update` after an intentional change, then
// read the diff before committing it.
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata")

// TestToMarkdownMatchesTheGoldenFiles pins the conversion contract against
// hand-written storage fragments. Golden files rather than inline expectations
// because the expected Markdown contains code fences, and a backtick cannot
// appear in a Go raw string literal.
func TestToMarkdownMatchesTheGoldenFiles(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.xml"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no testdata fixtures found")
	}

	for _, input := range inputs {
		t.Run(strings.TrimSuffix(filepath.Base(input), ".xml"), func(t *testing.T) {
			storage, err := os.ReadFile(input)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", input, err)
			}

			checkGolden(t, strings.TrimSuffix(input, ".xml")+".md",
				ToMarkdown(string(storage), Options{}).Markdown)
		})
	}
}

// TestToMarkdownMatchesTheResolvedGoldenFile pins the other half of the
// conversion contract: the same fixture the unresolved golden above covers,
// rendered with the resolution the command layer supplies. Keeping both as
// golden files is what makes a change to either rendering visible in review.
func TestToMarkdownMatchesTheResolvedGoldenFile(t *testing.T) {
	const input = "testdata/references.xml"

	storage, err := os.ReadFile(input)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", input, err)
	}

	opts := Options{
		UserNames: map[string]string{"557058:1a2b-3c4d": "Ada Lovelace"},
		PageURLs: map[PageRef]string{
			{SpaceKey: "DEV", Title: "Deployment Runbook"}: "https://example.atlassian.net/wiki/spaces/DEV/pages/123456",
			// No space key: the same-space form storage writes, which the
			// command layer resolves against the page's own space but has to
			// key by exactly what the body carries.
			{Title: "Other Page"}: "https://example.atlassian.net/wiki/spaces/DEV/pages/789012",
		},
	}

	checkGolden(t, "testdata/references.resolved.md", ToMarkdown(string(storage), opts).Markdown)
}

// checkGolden compares got against the golden file, or rewrites it under
// -update.
func checkGolden(t *testing.T, golden, got string) {
	t.Helper()

	if *updateGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", golden, err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", golden, err)
	}
	if got != string(want) {
		t.Errorf("conversion =\n%s\nwant %s to hold\n%s", got, golden, want)
	}
}

func TestToMarkdownResolvesReferencesWhenSupplied(t *testing.T) {
	const pageURL = "https://example.atlassian.net/wiki/spaces/DEV/pages/123456/Runbook"

	opts := Options{
		UserNames: map[string]string{"557058:1a2b": "Ada Lovelace"},
		PageURLs:  map[PageRef]string{{SpaceKey: "DEV", Title: "Runbook"}: pageURL},
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a known account id becomes the display name",
			in:   `<p>ping <ac:link><ri:user ri:account-id="557058:1a2b"/></ac:link></p>`,
			want: "ping @Ada Lovelace\n",
		},
		{
			name: "an unknown account id keeps the identifier",
			in:   `<p>ping <ac:link><ri:user ri:account-id="557058:9z9z"/></ac:link></p>`,
			want: "ping @557058:9z9z\n",
		},
		{
			name: "a resolved page becomes a link",
			in:   `<p>see <ac:link><ri:page ri:space-key="DEV" ri:content-title="Runbook"/></ac:link></p>`,
			want: "see [Runbook](" + pageURL + ")\n",
		},
		{
			name: "an unresolved page stays plain text rather than a fabricated URL",
			in:   `<p>see <ac:link><ri:page ri:space-key="OPS" ri:content-title="Runbook"/></ac:link></p>`,
			want: "see Runbook\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToMarkdown(tt.in, opts).Markdown; got != tt.want {
				t.Errorf("ToMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// References feeds the command layer's lookups, so what it collects has to
// be exactly what link() would render — no reference missed, and nothing
// collected that could only turn into a request whose answer is unusable.
func TestReferencesCollectsWhatLinkRenders(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Refs
	}{
		{
			name: "a body with no references needs nothing resolved",
			in:   "<p>plain</p>",
			want: Refs{},
		},
		{
			name: "mentions and page links are collected",
			in: `<p><ac:link><ri:user ri:account-id="acc-1"/></ac:link></p>` +
				`<p><ac:link><ri:page ri:space-key="DEV" ri:content-title="Runbook"/></ac:link></p>`,
			want: Refs{
				AccountIDs: []string{"acc-1"},
				Pages:      []PageRef{{SpaceKey: "DEV", Title: "Runbook"}},
			},
		},
		{
			name: "repeated references are collected once and sorted",
			in: `<p><ac:link><ri:user ri:account-id="acc-2"/></ac:link>` +
				`<ac:link><ri:user ri:account-id="acc-1"/></ac:link>` +
				`<ac:link><ri:user ri:account-id="acc-2"/></ac:link>` +
				`<ac:link><ri:page ri:space-key="OPS" ri:content-title="B"/></ac:link>` +
				`<ac:link><ri:page ri:space-key="DEV" ri:content-title="B"/></ac:link>` +
				`<ac:link><ri:page ri:space-key="DEV" ri:content-title="A"/></ac:link>` +
				`<ac:link><ri:page ri:space-key="DEV" ri:content-title="A"/></ac:link></p>`,
			want: Refs{
				AccountIDs: []string{"acc-1", "acc-2"},
				Pages: []PageRef{
					{SpaceKey: "DEV", Title: "A"},
					{SpaceKey: "DEV", Title: "B"},
					{SpaceKey: "OPS", Title: "B"},
				},
			},
		},
		{
			name: "a same-space link keeps the empty space key link() will look up",
			in:   `<p><ac:link><ri:page ri:content-title="Other Page"/></ac:link></p>`,
			want: Refs{Pages: []PageRef{{Title: "Other Page"}}},
		},
		{
			name: "references nested inside other elements are still found",
			in: `<table><tr><td><ac:link><ri:user ri:account-id="acc-1"/></ac:link></td></tr></table>` +
				`<ac:structured-macro ac:name="expand"><ac:rich-text-body>` +
				`<ac:link><ri:page ri:space-key="DEV" ri:content-title="Nested"/></ac:link>` +
				`</ac:rich-text-body></ac:structured-macro>`,
			want: Refs{
				AccountIDs: []string{"acc-1"},
				Pages:      []PageRef{{SpaceKey: "DEV", Title: "Nested"}},
			},
		},
		{
			name: "attachments are not collected, since cflio cannot resolve them to anything actionable",
			in:   `<p><ac:link><ri:attachment ri:filename="spec.pdf"/></ac:link></p>`,
			want: Refs{},
		},
		{
			name: "an anchor-only link names no resource to resolve",
			in:   `<p><ac:link ac:anchor="Section"><ac:plain-text-link-body><![CDATA[jump]]></ac:plain-text-link-body></ac:link></p>`,
			want: Refs{},
		},
		{
			name: "a target with no identifier is dropped rather than looked up",
			in: `<p><ac:link><ri:user ri:account-id=""/></ac:link>` +
				`<ac:link><ri:page ri:space-key="DEV" ri:content-title=""/></ac:link></p>`,
			want: Refs{},
		},
		{
			// ac:image reaches the same ri: children, so collecting from
			// anything but a link would spend a lookup on a reference the
			// renderer never resolves.
			name: "an image target is not a link target",
			in:   `<p><ac:image><ri:page ri:space-key="DEV" ri:content-title="Runbook"/></ac:image></p>`,
			want: Refs{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := References(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("References(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToMarkdownReportsWhatItCouldNotRepresent(t *testing.T) {
	storage := `<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">A-1</ac:parameter></ac:structured-macro>` +
		`<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">A-2</ac:parameter></ac:structured-macro>` +
		`<ac:adf-extension><ac:adf-node type="panel"/></ac:adf-extension>`

	result := ToMarkdown(storage, Options{})

	if result.UnsupportedCount != 3 {
		t.Errorf("UnsupportedCount = %d, want every placeholder counted (3)", result.UnsupportedCount)
	}
	want := []string{"adf-extension", "jira"}
	if strings.Join(result.Unsupported, ",") != strings.Join(want, ",") {
		t.Errorf("Unsupported = %v, want the distinct names sorted %v", result.Unsupported, want)
	}
}

func TestToMarkdownReportsNothingForAConversionItFullyRepresents(t *testing.T) {
	result := ToMarkdown("<p>plain</p>", Options{})

	if result.UnsupportedCount != 0 || result.Unsupported != nil {
		t.Errorf("Unsupported = %v (%d), want nothing reported for a clean conversion",
			result.Unsupported, result.UnsupportedCount)
	}
}

func TestToMarkdownEscapesOnlyWhatWouldBeMisread(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "emphasis markers are escaped",
			in:   "<p>2 * 3 * 4</p>",
			want: "2 \\* 3 \\* 4\n",
		},
		{
			name: "underscores are left alone so identifiers stay readable",
			in:   "<p>call read_page_body first</p>",
			want: "call read_page_body first\n",
		},
		{
			name: "link syntax and backslashes are escaped",
			in:   `<p>see [1] or C:\temp</p>`,
			want: "see \\[1\\] or C:\\\\temp\n",
		},
		{
			name: "backticks are escaped",
			in:   "<p>the `code` span</p>",
			want: "the \\`code\\` span\n",
		},
		{
			name: "decoded angle brackets do not become raw HTML",
			in:   "<p>&lt;div&gt;</p>",
			want: "\\<div>\n",
		},
		{
			name: "a paragraph that would read as a list is escaped",
			in:   "<p>- not a list</p>",
			want: "\\- not a list\n",
		},
		{
			name: "a paragraph that would read as an ordered list is escaped",
			in:   "<p>1. not a list</p>",
			want: "1\\. not a list\n",
		},
		{
			name: "a paragraph that would read as a heading is escaped",
			in:   "<p># not a heading</p>",
			want: "\\# not a heading\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToMarkdown(tt.in, Options{}).Markdown; got != tt.want {
				t.Errorf("ToMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Emoticons carry meaning a reader acts on — a tick in a status column is
// the cell's whole content — and they are the one degradation that would be
// invisible: they leave no placeholder and nothing to count.
func TestToMarkdownKeepsEmoticonsAsText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "the emoji fallback is preferred",
			in:   `<p>done <ac:emoticon ac:name="tick" ac:emoji-shortname=":check_mark:" ac:emoji-fallback="✅"/></p>`,
			want: "done ✅\n",
		},
		{
			name: "the shortname stands in when there is no fallback",
			in:   `<p>rated <ac:emoticon ac:name="blue-star" ac:emoji-shortname=":star:"/></p>`,
			want: "rated :star:\n",
		},
		{
			name: "an old-style emoticon becomes its name in shortname form",
			in:   `<p>nice <ac:emoticon ac:name="smile"/></p>`,
			want: "nice :smile:\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToMarkdown(tt.in, Options{}).Markdown; got != tt.want {
				t.Errorf("ToMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Storage carries HTML entities that no XML document type declares, so the
// decoder is configured with the HTML entity table. Both halves of that
// setting matter: known names have to resolve, and an unknown one has to
// leave the text alone rather than abort the parse and lose the rest.
func TestToMarkdownDecodesHTMLEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "named HTML entities are decoded",
			in:   "<p>100&euro; total</p>",
			want: "100€ total\n",
		},
		{
			name: "unknown entities are left as written rather than dropping the text",
			in:   "<p>100&bogus; total</p>",
			want: "100&bogus; total\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToMarkdown(tt.in, Options{}).Markdown; got != tt.want {
				t.Errorf("ToMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToMarkdownKeepsGoingOnAwkwardInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "whitespace-only paragraphs are dropped",
			in:   "<p>&nbsp;</p><p>text</p><p>  </p>",
			want: "text\n",
		},
		{
			name: "unbalanced markup still yields its text",
			in:   "<p>open <strong>bold</p>",
			want: "open **bold**\n",
		},
		{
			name: "an unknown element passes its children through",
			in:   `<ac:something-new ac:local-id="1"><p>kept</p></ac:something-new>`,
			want: "kept\n",
		},
		{
			name: "a task with no body does not abort the conversion",
			in: `<ac:task-list><ac:task><ac:task-id>1</ac:task-id>` +
				`<ac:task-status>complete</ac:task-status></ac:task></ac:task-list>`,
			want: "- [x]\n",
		},
		{
			name: "adjacent unknown block containers do not run their text together",
			in:   "<div>alpha</div><div>beta</div>",
			want: "alpha\n\nbeta\n",
		},
		{
			name: "a definition list keeps its terms and definitions apart",
			in:   "<dl><dt>Term</dt><dd>Definition</dd></dl>",
			want: "Term\n\nDefinition\n",
		},
		{
			name: "plain text outside any element is kept",
			in:   "just words",
			want: "just words\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToMarkdown(tt.in, Options{}).Markdown; got != tt.want {
				t.Errorf("ToMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A plantumlcloud macro keeps its diagram source in an encoded parameter
// rather than in a body, so rendering that source is what makes the macro
// fully representable — and the conversion has to say so by reporting nothing.
func TestToMarkdownDoesNotDegradeAPlantUMLMacroItRenders(t *testing.T) {
	const input = "testdata/plantumlcloud_macro.xml"

	storage, err := os.ReadFile(input)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", input, err)
	}

	result := ToMarkdown(string(storage), Options{})

	if result.UnsupportedCount != 0 || result.Unsupported != nil {
		t.Errorf("Unsupported = %v (%d), want nothing reported for a diagram whose source survived",
			result.Unsupported, result.UnsupportedCount)
	}
}

// Only the encoding observed on a real page is decoded; anything else falls
// back to the placeholder rather than to a guess at a format nobody has seen.
func TestToMarkdownDegradesAPlantUMLMacroItCannotDecode(t *testing.T) {
	const compressed = `<ac:parameter ac:name="compressed">true</ac:parameter>`

	// A payload that decodes cleanly, so the two parameter cases below isolate
	// the guard they are about instead of failing further down the pipeline.
	valid := dataParameter(deflateBase64(t, "%40startuml%0A%40enduml"))

	tests := []struct {
		name       string
		parameters string
	}{
		{
			name:       "no data parameter",
			parameters: compressed,
		},
		{
			name:       "no compressed parameter",
			parameters: valid,
		},
		{
			name:       "compressed is not true",
			parameters: `<ac:parameter ac:name="compressed">false</ac:parameter>` + valid,
		},
		{
			name:       "the data parameter is not base64",
			parameters: compressed + dataParameter("not base64!"),
		},
		{
			name:       "the base64 payload is not deflate",
			parameters: compressed + dataParameter(base64.StdEncoding.EncodeToString([]byte("not deflate"))),
		},
		{
			name:       "the inflated payload is not percent-encoded",
			parameters: compressed + dataParameter(deflateBase64(t, "%zz")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := `<ac:structured-macro ac:name="plantumlcloud">` + tt.parameters + `</ac:structured-macro>`

			result := ToMarkdown(storage, Options{})

			if want := "[unsupported macro: plantumlcloud]\n"; result.Markdown != want {
				t.Errorf("ToMarkdown() = %q, want the placeholder %q", result.Markdown, want)
			}
			if result.UnsupportedCount != 1 {
				t.Errorf("UnsupportedCount = %d, want the macro counted as degraded (1)",
					result.UnsupportedCount)
			}
		})
	}
}

// dataParameter wraps a payload the way storage carries a plantumlcloud
// macro's diagram source.
func dataParameter(payload string) string {
	return `<ac:parameter ac:name="data">` + payload + `</ac:parameter>`
}

// deflateBase64 encodes s the way a plantumlcloud macro's data parameter is
// encoded, minus the percent-encoding the real payload carries. That lets a
// test hand the decoder input whose only invalid stage is the last one.
func deflateBase64(t *testing.T, s string) string {
	t.Helper()

	var deflated bytes.Buffer
	w, err := flate.NewWriter(&deflated, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter() error = %v", err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("Write(%q) error = %v", s, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(deflated.Bytes())
}

// A data value renders identically whether or not it carries = padding: a
// real page can carry an unpadded value (#37), and only the 4n length class
// happens to look the same padded or not, which is why the #35 fixture
// didn't catch this.
func TestToMarkdownRendersAPlantUMLMacroRegardlessOfBase64Padding(t *testing.T) {
	const compressed = `<ac:parameter ac:name="compressed">true</ac:parameter>`

	tests := []struct {
		name    string
		wantMod int
		padded  bool
	}{
		{name: "unpadded, encoded length 4n+2", wantMod: 2, padded: false},
		{name: "unpadded, encoded length 4n+3", wantMod: 3, padded: false},
		{name: "padded, encoded length 4n+2", wantMod: 2, padded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := growPlantUMLSource(t, tt.wantMod)
			payload := deflateBase64(t, source)
			if !tt.padded {
				payload = strings.TrimRight(payload, "=")
			}
			if got := len(strings.TrimRight(payload, "=")) % 4; got != tt.wantMod {
				t.Fatalf("payload length class = %d, want %d", got, tt.wantMod)
			}

			storage := `<ac:structured-macro ac:name="plantumlcloud">` + compressed + dataParameter(payload) + `</ac:structured-macro>`

			result := ToMarkdown(storage, Options{})

			if result.UnsupportedCount != 0 || result.Unsupported != nil {
				t.Errorf("Unsupported = %v (%d), want nothing reported for a diagram whose source decoded",
					result.Unsupported, result.UnsupportedCount)
			}
			if want := "```plantuml\n"; !strings.HasPrefix(result.Markdown, want) {
				t.Errorf("ToMarkdown() = %q, want a fenced block starting with %q", result.Markdown, want)
			}
		})
	}
}

// growPlantUMLSource grows a %-encoded plantuml source until its deflated,
// base64-unpadded length is wantMod (mod 4). Deflate output length isn't
// predictable from input length, so this can't be a fixed hand-picked string
// (see #37) — it must probe and assert the class it lands on.
func growPlantUMLSource(t *testing.T, wantMod int) string {
	t.Helper()

	source := "%40startuml%0A%40enduml"
	for range 1000 {
		payload := deflateBase64(t, source)
		if unpadded := strings.TrimRight(payload, "="); len(unpadded)%4 == wantMod {
			return source
		}
		source += "x"
	}
	t.Fatalf("could not grow a source whose unpadded base64 length is %d (mod 4)", wantMod)
	return ""
}
