package format

import "testing"

func TestStripStorage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain paragraph",
			in:   "<p>Hello there</p>",
			want: "Hello there",
		},
		{
			name: "paragraphs become separate lines",
			in:   "<p>First</p><p>Second</p>",
			want: "First\nSecond",
		},
		{
			name: "line breaks",
			in:   "<p>First<br/>Second</p>",
			want: "First\nSecond",
		},
		{
			name: "entities are decoded",
			in:   "<p>a &amp; b &lt;c&gt; &quot;d&quot;</p>",
			want: `a & b <c> "d"`,
		},
		{
			name: "non-breaking spaces become ordinary ones",
			in:   "<p>a&nbsp;b</p>",
			want: "a b",
		},
		{
			name: "inline markup is unwrapped",
			in:   "<p>a <strong>bold</strong> and <em>italic</em> word</p>",
			want: "a bold and italic word",
		},
		{
			name: "links keep their text",
			in:   `<p>see <a href="https://example.com">the docs</a></p>`,
			want: "see the docs",
		},
		{
			name: "list items are bulleted",
			in:   "<ul><li>one</li><li>two</li></ul>",
			want: "- one\n- two",
		},
		{
			name: "headings sit on their own line",
			in:   "<h2>Title</h2><p>body</p>",
			want: "Title\nbody",
		},
		{
			name: "table rows are separated",
			in:   "<table><tbody><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></tbody></table>",
			want: "a b\nc",
		},
		{
			name: "user mentions render as account ids",
			in:   `<p>ping <ac:link><ri:user ri:account-id="acc-123"/></ac:link> please</p>`,
			want: "ping @acc-123 please",
		},
		{
			name: "macros keep their rich text body",
			in:   `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>heads up</p></ac:rich-text-body></ac:structured-macro>`,
			want: "heads up",
		},
		{
			name: "undeclared namespace prefixes do not abort parsing",
			in:   `<p>before</p><ac:emoticon ac:name="smile"/><p>after</p>`,
			want: "before\nafter",
		},
		{
			name: "named HTML entities are decoded",
			in:   "<p>100&euro; total</p>",
			want: "100€ total",
		},
		{
			name: "unknown entities are left as written rather than dropping the text",
			in:   "<p>100&bogus; total</p>",
			want: "100&bogus; total",
		},
		{
			name: "an ac:link wrapper does not swallow the text after it",
			in:   `<p>see <ac:link><ri:page ri:content-title="Other"/></ac:link> for more</p>`,
			want: "see for more",
		},
		{
			name: "blank runs are collapsed",
			in:   "<p>a</p><p></p><p></p><p>b</p>",
			want: "a\nb",
		},
		{
			name: "surrounding whitespace is trimmed",
			in:   "\n  <p>  padded  </p>  \n",
			want: "padded",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "plain text without markup",
			in:   "just words",
			want: "just words",
		},
		{
			name: "unbalanced markup still yields its text",
			in:   "<p>open <strong>bold</p>",
			want: "open bold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripStorage(tt.in); got != tt.want {
				t.Errorf("StripStorage(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripHighlightMarkers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "markers around a match",
			in:   "The @@@hl@@@quick@@@endhl@@@ brown fox",
			want: "The quick brown fox",
		},
		{
			name: "several matches",
			in:   "@@@hl@@@a@@@endhl@@@ and @@@hl@@@b@@@endhl@@@",
			want: "a and b",
		},
		{
			name: "no markers is a no-op",
			in:   "plain title",
			want: "plain title",
		},
		{
			name: "html entities from the excerpt are decoded",
			in:   "a &amp; b",
			want: "a & b",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripHighlightMarkers(tt.in); got != tt.want {
				t.Errorf("StripHighlightMarkers(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIndent(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		prefix string
		want   string
	}{
		{name: "single line", text: "a", prefix: "  ", want: "  a"},
		{name: "multiple lines", text: "a\nb", prefix: "> ", want: "> a\n> b"},
		{name: "blank lines are not padded", text: "a\n\nb", prefix: "> ", want: "> a\n>\n> b"},
		{name: "empty", text: "", prefix: "> ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Indent(tt.text, tt.prefix); got != tt.want {
				t.Errorf("Indent(%q, %q) = %q, want %q", tt.text, tt.prefix, got, tt.want)
			}
		})
	}
}
