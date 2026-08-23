package plantuml

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"os"
	"strings"
	"testing"
)

// appPayload returns the app-generated data parameter and the source it holds,
// lifted from a real page. It is what pins the codec to the app's own shape
// rather than to this package's idea of it.
//
// The source fixture carries a trailing newline because a text file ends with
// one; the payload's own source does not, so it is trimmed back off here.
func appPayload(t *testing.T) (data, source string) {
	t.Helper()

	rawData, err := os.ReadFile("testdata/app_payload.txt")
	if err != nil {
		t.Fatalf("read payload fixture: %v", err)
	}
	rawSource, err := os.ReadFile("testdata/app_source.puml")
	if err != nil {
		t.Fatalf("read source fixture: %v", err)
	}
	return strings.TrimSpace(string(rawData)), strings.TrimSuffix(string(rawSource), "\n")
}

func TestDecodeReadsAnAppGeneratedPayload(t *testing.T) {
	data, want := appPayload(t)

	got, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != want {
		t.Errorf("Decode() = %q, want %q", got, want)
	}
}

// TestDecodeAcceptsPaddedBase64 covers the other shape a real page can carry:
// the app writes its base64 unpadded, but a padded value has to decode too
// (#37 is the page that made this a regression rather than a hypothetical).
func TestDecodeAcceptsPaddedBase64(t *testing.T) {
	data, want := appPayload(t)

	deflated, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("decode fixture payload: %v", err)
	}
	padded := base64.StdEncoding.EncodeToString(deflated)

	got, err := Decode(padded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != want {
		t.Errorf("Decode() = %q, want %q", got, want)
	}
}

func TestEncodeWritesTheShapeTheAppWrites(t *testing.T) {
	_, source := appPayload(t)

	data, err := Encode(source)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	// Not compared against the fixture's own bytes: compress/flate does not
	// produce the app's deflate output. What has to match is the framing —
	// standard alphabet, no padding — and that the payload inflates to the
	// percent-encoded source.
	if strings.ContainsAny(data, "=-_") {
		t.Errorf("Encode() = %q, want standard base64 with no padding", data)
	}
	deflated, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("decode Encode() output: %v", err)
	}
	escaped, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflated)))
	if err != nil {
		t.Fatalf("inflate Encode() output: %v", err)
	}
	if got := string(escaped); got != escape(source) {
		t.Errorf("inflated payload = %q, want %q", got, escape(source))
	}
}

// TestEscapeLeavesTheUnreservedSetBare pins the percent-encoding itself. The
// set is encodeURIComponent's, which is what the app's own payloads use, and
// no stdlib escaper has it — so it is asserted directly rather than through a
// round-trip that would pass for any self-consistent encoding.
func TestEscapeLeavesTheUnreservedSetBare(t *testing.T) {
	const unreserved = "ABCXYZabcxyz0189-_.!~*'()"
	if got := escape(unreserved); got != unreserved {
		t.Errorf("escape(%q) = %q, want it unchanged", unreserved, got)
	}

	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{name: "space", in: " ", want: "%20"},
		{name: "plus", in: "+", want: "%2B"},
		{name: "percent", in: "%", want: "%25"},
		{name: "slash", in: "/", want: "%2F"},
		{name: "at", in: "@startuml", want: "%40startuml"},
		{name: "newline", in: "\n", want: "%0A"},
		{name: "reserved punctuation", in: ",;:=&$#?[]", want: "%2C%3B%3A%3D%26%24%23%3F%5B%5D"},
		// Escaped per UTF-8 byte, not per rune: three bytes for one character.
		{name: "non-ASCII", in: "図", want: "%E5%9B%B3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := escape(tt.in); got != tt.want {
				t.Errorf("escape(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodeOfEncodeReturnsTheSource(t *testing.T) {
	_, appSource := appPayload(t)

	for _, tt := range []struct {
		name   string
		source string
	}{
		{name: "empty", source: ""},
		{name: "ascii", source: "@startuml\nAlice -> Bob: hi\n@enduml"},
		{name: "trailing newline", source: "@startuml\n@enduml\n"},
		{name: "unreserved punctuation", source: "note left: -_.!~*'()"},
		{name: "reserved punctuation", source: "note left: +%/&=?#[]{} \t"},
		{name: "non-ASCII", source: "@startuml\nactor 利用者\nnote right: 図の説明\n@enduml"},
		{name: "app fixture", source: appSource},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Encode(tt.source)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			got, err := Decode(data)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got != tt.source {
				t.Errorf("Decode(Encode(%q)) = %q", tt.source, got)
			}
		})
	}
}

func TestDecodeRejectsAPayloadItCannotGetThrough(t *testing.T) {
	deflate := func(t *testing.T, s string) string {
		t.Helper()

		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, flate.BestCompression)
		if err != nil {
			t.Fatalf("flate.NewWriter() error = %v", err)
		}
		if _, err := io.WriteString(w, s); err != nil {
			t.Fatalf("write deflate stream: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close deflate stream: %v", err)
		}
		return base64.RawStdEncoding.EncodeToString(buf.Bytes())
	}

	for _, tt := range []struct {
		name string
		data func(t *testing.T) string
	}{
		{name: "empty", data: func(*testing.T) string { return "" }},
		{name: "not base64", data: func(*testing.T) string { return "not base64!!" }},
		{name: "not deflate", data: func(*testing.T) string {
			return base64.RawStdEncoding.EncodeToString([]byte("plain text"))
		}},
		{name: "not percent-encoded", data: func(t *testing.T) string { return deflate(t, "100%") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Decode(tt.data(t)); err == nil {
				t.Errorf("Decode() = %q, want an error", got)
			}
		})
	}
}
