package format

import "testing"

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
