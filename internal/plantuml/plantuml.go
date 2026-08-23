// Package plantuml is the codec for the payload a plantumlcloud macro carries
// in its data parameter — the app "PlantUML Diagrams for Confluence".
//
// The encoding is the app's own and undocumented, and it is not PlantUML's
// public one: the public encoding uses a custom base64 alphabet over deflate,
// which this app does not accept. Encode and Decode are each other's inverse
// and live together for that reason — a decoder that drifts from the encoder
// would let cflio write a diagram it cannot read back.
//
// Source is the entry point for a macro's parameters, Encode and Decode the
// codec underneath. Whether a payload is in this format at all is what the
// macro's compressed parameter answers, so the rule for reading it lives here
// with the format it guards rather than being restated by every caller.
package plantuml

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Encode turns diagram source into the data parameter's value: the source
// percent-encoded, raw-deflated and base64'd, which is the app's own shape.
func Encode(source string) (string, error) {
	var deflated bytes.Buffer

	// BestCompression rather than the default: the result is stored in a page
	// body, where it is read far more often than it is written. Nothing
	// depends on the exact bytes — compress/flate does not reproduce the
	// app's deflate output at any level, and the app inflates whatever it is
	// given.
	w, err := flate.NewWriter(&deflated, flate.BestCompression)
	if err != nil {
		return "", fmt.Errorf("start deflate stream: %w", err)
	}
	if _, err := io.WriteString(w, escape(source)); err != nil {
		return "", fmt.Errorf("deflate diagram source: %w", err)
	}
	// Closed explicitly rather than deferred: the final block is written here,
	// so a deferred close would leave a truncated stream on the error path.
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("finish deflate stream: %w", err)
	}

	// Standard alphabet with no padding, which is what the app writes. A
	// padded value also renders, but emitting the app's shape keeps a page
	// cflio wrote indistinguishable from one the app wrote.
	return base64.RawStdEncoding.EncodeToString(deflated.Bytes()), nil
}

// Source decodes the diagram a macro carries, given its compressed and data
// parameters exactly as the body writes them.
//
// A compressed value other than "true" is refused rather than guessed at: it
// has never been seen on a real page, its payload format is unknown, and
// decoding one anyway would be a way to show a diagram nobody wrote.
func Source(compressed, data string) (string, error) {
	if compressed != "true" {
		return "", fmt.Errorf("compressed=%q rather than \"true\": "+
			"cflio does not know that payload format and will not guess at it", compressed)
	}
	return Decode(data)
}

// Decode reads the diagram source back out of a data parameter's value.
func Decode(data string) (string, error) {
	if data == "" {
		return "", fmt.Errorf("empty data parameter")
	}

	// Standard base64, not PlantUML's own URL-safe alphabet: the payload
	// carries + and /.
	//
	// Padding is unreliable: a real page can carry a data value with no =
	// padding (#37). Neither Encoding alone accepts both shapes, so any
	// padding is trimmed first and the rest is decoded raw.
	deflated, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "="))
	if err != nil {
		return "", fmt.Errorf("decode base64 payload: %w", err)
	}

	// Raw deflate, with neither a zlib nor a gzip header. Not closing the
	// reader loses nothing: it wraps a buffer, and ReadAll already surfaces a
	// truncated or corrupt stream as an error.
	escaped, err := io.ReadAll(flate.NewReader(bytes.NewReader(deflated)))
	if err != nil {
		return "", fmt.Errorf("inflate payload: %w", err)
	}

	// PathUnescape rather than QueryUnescape, because a + in the inflated
	// source is a plus sign and not a space. Non-ASCII is percent-encoded too,
	// so what comes out is UTF-8 text.
	source, err := url.PathUnescape(string(escaped))
	if err != nil {
		return "", fmt.Errorf("unescape diagram source: %w", err)
	}
	return source, nil
}

// escape percent-encodes source the way JavaScript's encodeURIComponent does,
// which is what the app's own payloads are encoded with: every byte outside
// the unreserved set below becomes %XX.
//
// Written out rather than delegated because no stdlib escaper has this set.
// url.PathEscape leaves far more characters bare, and url.QueryEscape writes a
// space as + — which the decoder, reading a + as a plus sign, would hand back
// as a plus sign.
func escape(source string) string {
	var out strings.Builder
	// Half again the input: diagram source escapes a good fraction of its
	// bytes -- every space, newline, > and : -- so sizing for the input alone
	// would have the builder regrow, copying what it holds each time.
	out.Grow(len(source) + len(source)/2)

	// Ranged over as bytes, not runes: a non-ASCII character is escaped one
	// UTF-8 byte at a time, which is what url.PathUnescape reassembles.
	for i := range len(source) {
		c := source[i]
		if isUnreserved(c) {
			out.WriteByte(c)
			continue
		}
		// Written out rather than formatted: this runs once per escaped byte,
		// which is most of them.
		out.WriteByte('%')
		out.WriteByte(hexDigits[c>>4])
		out.WriteByte(hexDigits[c&0xF])
	}
	return out.String()
}

// hexDigits is upper-case, the case encodeURIComponent emits.
const hexDigits = "0123456789ABCDEF"

// isUnreserved reports whether encodeURIComponent leaves a byte bare.
func isUnreserved(c byte) bool {
	switch {
	case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("-_.!~*'()", c) >= 0
}
