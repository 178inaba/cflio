package format

import (
	"fmt"
	"strings"
	"testing"
)

func TestFormatSet(t *testing.T) {
	tests := []struct {
		name string
		// start is what the receiver holds before Set runs. The accepted
		// values start from one no case assigns, so a Set that writes nothing
		// fails; the rejected ones start from a real value, so a Set that
		// writes before validating clobbers it.
		start   Format
		in      string
		want    Format
		wantErr bool
	}{
		{name: "md", start: Format(""), in: "md", want: Markdown},
		{name: "json", start: Format(""), in: "json", want: JSON},
		{name: "unknown value leaves the receiver alone", start: Markdown, in: "bogus", want: Markdown, wantErr: true},
		{name: "empty value leaves the receiver alone", start: Markdown, in: "", want: Markdown, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.start
			err := got.Set(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Format.Set(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				if want := fmt.Sprintf("invalid --format %q", tt.in); !strings.Contains(err.Error(), want) {
					t.Errorf("Format.Set(%q) error = %q, want it to contain %q", tt.in, err, want)
				}
			}
			if got != tt.want {
				t.Errorf("after Format.Set(%q) the receiver is %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatValidate(t *testing.T) {
	tests := []struct {
		name    string
		f       Format
		wantErr bool
	}{
		{name: "md", f: Markdown},
		{name: "json", f: JSON},
		// Set is not the only way to build a Format: these two type-check,
		// which is why the callers that branch on one keep the guard.
		{name: "converted from an arbitrary string", f: Format("xml"), wantErr: true},
		{name: "the zero value", f: Format(""), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.f.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Format(%q).Validate() error = %v, wantErr %v", string(tt.f), err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			if want := fmt.Sprintf("invalid --format %q", string(tt.f)); !strings.Contains(err.Error(), want) {
				t.Errorf("Format(%q).Validate() error = %q, want it to contain %q", string(tt.f), err, want)
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
