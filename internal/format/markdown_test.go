package format

import (
	"flag"
	"os"
	"path/filepath"
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

			got := ToMarkdown(string(storage), Options{}).Markdown

			golden := strings.TrimSuffix(input, ".xml") + ".md"
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
				t.Errorf("ToMarkdown(%s) =\n%s\nwant\n%s", input, got, want)
			}
		})
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
