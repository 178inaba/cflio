package format

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestADFToMarkdownMatchesTheGoldenFiles pins the ADF rendering of every node
// and mark the converter represents natively. Each fixture is also asserted to
// report no loss: a native rendering that counted itself as degraded would
// tell a reader to distrust output that is faithful.
func TestADFToMarkdownMatchesTheGoldenFiles(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "adf_*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(inputs) == 0 {
		t.Fatal("no ADF testdata fixtures found")
	}

	for _, input := range inputs {
		t.Run(strings.TrimSuffix(filepath.Base(input), ".json"), func(t *testing.T) {
			doc, err := os.ReadFile(input)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", input, err)
			}

			got := ADFToMarkdown(doc)
			checkGolden(t, strings.TrimSuffix(input, ".json")+".md", got.Markdown)
			if got.UnsupportedCount != 0 || len(got.Unsupported) != 0 {
				t.Errorf("Unsupported = %v (count %d), want nothing for natively rendered input",
					got.Unsupported, got.UnsupportedCount)
			}
		})
	}
}

func TestADFToMarkdownCountsWhatItCouldNotRepresent(t *testing.T) {
	doc := `{"type":"doc","version":1,"content":[
		{"type":"mediaSingle","attrs":{"layout":"center"},"content":[
			{"type":"media","attrs":{"id":"m-1","type":"file","collection":"c"}}
		]},
		{"type":"extension","attrs":{"extensionType":"com.atlassian.ecosystem","extensionKey":"jira-chart"}},
		{"type":"madeUp","content":[
			{"type":"paragraph","content":[{"type":"text","text":"kept text"}]}
		]}
	]}`

	got := ADFToMarkdown([]byte(doc))

	if got.UnsupportedCount != 3 {
		t.Errorf("UnsupportedCount = %d, want 3", got.UnsupportedCount)
	}
	if want := []string{"extension", "madeUp", "mediaSingle"}; !slices.Equal(got.Unsupported, want) {
		t.Errorf("Unsupported = %v, want %v", got.Unsupported, want)
	}
	for _, want := range []string{
		"[unsupported mediaSingle]",
		"[unsupported extension: jira-chart]",
		"[unsupported madeUp]",
		"kept text",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("Markdown = %q, want it to contain %q", got.Markdown, want)
		}
	}
}

func TestADFToMarkdownDegrades(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		want       string
		wantNames  []string
		wantCount  int
		wantAbsent string
	}{
		{
			name: "a media node outside a container",
			content: `{"type":"paragraph","content":[{"type":"text","text":"see "},
				{"type":"mediaInline","attrs":{"id":"m-2","type":"file","collection":"c"}}]},
				{"type":"media","attrs":{"id":"m-3","type":"file","collection":"c"}}`,
			want:      "see [unsupported mediaInline]\n\n[unsupported media]\n",
			wantNames: []string{"media", "mediaInline"},
			wantCount: 2,
		},
		{
			name: "a media group counted once and its caption kept",
			content: `{"type":"mediaGroup","content":[
					{"type":"media","attrs":{"id":"a","type":"file","collection":"c"}},
					{"type":"media","attrs":{"id":"b","type":"file","collection":"c"}}]},
				{"type":"mediaSingle","content":[
					{"type":"media","attrs":{"id":"c","type":"file","collection":"c"}},
					{"type":"caption","content":[{"type":"text","text":"A diagram"}]}]}`,
			want:      "[unsupported mediaGroup]\n\n[unsupported mediaSingle]\n\nA diagram\n",
			wantNames: []string{"mediaGroup", "mediaSingle"},
			wantCount: 2,
		},
		{
			name: "a bodied extension keeps its body",
			content: `{"type":"bodiedExtension","attrs":{"extensionKey":"details"},"content":[
				{"type":"paragraph","content":[{"type":"text","text":"inside"}]}]}`,
			want:      "[unsupported bodiedExtension: details]\n\ninside\n",
			wantNames: []string{"bodiedExtension"},
			wantCount: 1,
		},
		{
			name: "an unknown inline node keeps its text",
			content: `{"type":"paragraph","content":[{"type":"text","text":"a "},
				{"type":"placeholderish","content":[{"type":"text","text":"b"}]},
				{"type":"inlineExtension","attrs":{"extensionKey":"x"}}]}`,
			want:      "a [unsupported placeholderish]b[unsupported inlineExtension: x]\n",
			wantNames: []string{"inlineExtension", "placeholderish"},
			wantCount: 2,
		},
		{
			name:      "an unknown block node with direct text",
			content:   `{"type":"decisionList","content":[{"type":"text","text":"loose"}]}`,
			want:      "[unsupported decisionList]\n\nloose\n",
			wantNames: []string{"decisionList"},
			wantCount: 1,
		},
		{
			name:      "a node with no type",
			content:   `{"content":[{"type":"paragraph","content":[{"type":"text","text":"orphan"}]}]}`,
			want:      "[unsupported unnamed]\n\norphan\n",
			wantNames: []string{"unnamed"},
			wantCount: 1,
		},
		{
			name:       "a card that carries data instead of a url",
			content:    `{"type":"paragraph","content":[{"type":"inlineCard","attrs":{"data":{"@type":"Document","name":"Doc"}}}]}`,
			want:       "[unsupported inlineCard]\n",
			wantNames:  []string{"inlineCard"},
			wantCount:  1,
			wantAbsent: "](",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ADFToMarkdown([]byte(`{"type":"doc","version":1,"content":[` + tt.content + `]}`))

			if got.Markdown != tt.want {
				t.Errorf("Markdown = %q, want %q", got.Markdown, tt.want)
			}
			if !slices.Equal(got.Unsupported, tt.wantNames) {
				t.Errorf("Unsupported = %v, want %v", got.Unsupported, tt.wantNames)
			}
			if got.UnsupportedCount != tt.wantCount {
				t.Errorf("UnsupportedCount = %d, want %d", got.UnsupportedCount, tt.wantCount)
			}
			if tt.wantAbsent != "" && strings.Contains(got.Markdown, tt.wantAbsent) {
				t.Errorf("Markdown = %q, want no %q", got.Markdown, tt.wantAbsent)
			}
		})
	}
}

// TestADFToMarkdownToleratesMissingAttributes covers attributes the schema
// requires but the converter cannot rely on: it is contracted never to fail,
// and an absent attribute must not cost the content around it.
func TestADFToMarkdownToleratesMissingAttributes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "a heading without a level",
			content: `{"type":"heading","content":[{"type":"text","text":"Top"}]}`,
			want:    "# Top\n",
		},
		{
			name:    "a heading with an out-of-range level",
			content: `{"type":"heading","attrs":{"level":9},"content":[{"type":"text","text":"Top"}]}`,
			want:    "# Top\n",
		},
		{
			name:    "a date in seconds",
			content: `{"type":"paragraph","content":[{"type":"date","attrs":{"timestamp":"1582152559"}}]}`,
			want:    "2020-02-19\n",
		},
		{
			name:    "a date that is not a number",
			content: `{"type":"paragraph","content":[{"type":"date","attrs":{"timestamp":"tomorrow"}}]}`,
			want:    "tomorrow\n",
		},
		{
			name:    "an ordered list starting at zero",
			content: `{"type":"orderedList","attrs":{"order":0},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"zero"}]}]}]}`,
			want:    "0. zero\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ADFToMarkdown([]byte(`{"type":"doc","version":1,"content":[` + tt.content + `]}`))
			if got.Markdown != tt.want {
				t.Errorf("Markdown = %q, want %q", got.Markdown, tt.want)
			}
		})
	}
}

func TestADFToMarkdownKeepsGoingOnMalformedInput(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		wantText  string
		wantNames []string
	}{
		{
			name:      "a document with no type at the root",
			doc:       `{"content":[{"type":"paragraph","content":[{"type":"text","text":"rootless"}]}]}`,
			wantText:  "rootless",
			wantNames: []string{"invalid-adf"},
		},
		{
			name:     "a document cut off mid-way",
			doc:      `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"survives"}]},{"type":"para`,
			wantText: "survives",
		},
		{
			name: "not JSON at all",
			doc:  `<p>storage</p>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ADFToMarkdown([]byte(tt.doc))
			if !strings.Contains(got.Markdown, tt.wantText) {
				t.Errorf("Markdown = %q, want it to contain %q", got.Markdown, tt.wantText)
			}
			for _, name := range tt.wantNames {
				if !slices.Contains(got.Unsupported, name) {
					t.Errorf("Unsupported = %v, want it to name %q", got.Unsupported, name)
				}
			}
		})
	}
}

// TestADFSectionsToMarkdownReportsOneResult covers what a file of several
// documents adds over one: each later document under its heading, and the
// losses of all of them reported together, names merged and counts summed.
func TestADFSectionsToMarkdownReportsOneResult(t *testing.T) {
	const (
		description = `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Intro"}]},
			{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"a","type":"file","collection":"c"}}]}]}`
		environment = `{"type":"doc","content":[{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"b","type":"file","collection":"c"}}]},
			{"type":"extension","attrs":{"extensionKey":"k"}}]}`
		empty = `{"type":"doc","content":[]}`
	)

	got := ADFSectionsToMarkdown([]ADFSection{
		{Doc: []byte(description)},
		{Heading: "Environment", Doc: []byte(environment)},
		{Heading: "Notes *draft*", Doc: []byte(empty)},
	})

	want := "Intro\n\n[unsupported mediaSingle]\n\n## Environment\n\n[unsupported mediaSingle]\n\n" +
		"[unsupported extension: k]\n\n## Notes \\*draft\\*\n"
	if got.Markdown != want {
		t.Errorf("Markdown =\n%s\nwant\n%s", got.Markdown, want)
	}
	if want := []string{"extension", "mediaSingle"}; !slices.Equal(got.Unsupported, want) {
		t.Errorf("Unsupported = %v, want %v", got.Unsupported, want)
	}
	if got.UnsupportedCount != 3 {
		t.Errorf("UnsupportedCount = %d, want 3", got.UnsupportedCount)
	}
}
