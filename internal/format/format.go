// Package format holds the text helpers the commands share when rendering
// API results: storage-XHTML decoding, search highlight-marker stripping and
// indentation.
//
// The storage-to-text conversion here is display-only: it exists because
// the comment endpoints refuse body-format=view, so comment bodies arrive
// as storage XHTML and have to be made readable locally. Page bodies never
// pass through it — those are written to and read from files untouched, so
// the round-trip stays byte-lossless.
package format

import (
	"encoding/xml"
	"html"
	"io"
	"strings"
)

// Highlight markers Confluence wraps around matched terms when a search
// runs with the default excerpt=highlight.
const (
	highlightStart = "@@@hl@@@"
	highlightEnd   = "@@@endhl@@@"
)

// blockElements end a line of output when they close. Storage format is
// XHTML, so the set is the usual block-level tags plus table rows.
var blockElements = map[string]bool{
	"p": true, "div": true, "li": true, "br": true, "tr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "pre": true,
}

// StripStorage turns storage XHTML into readable plain text on a
// best-effort basis: text is kept, block elements become line breaks, list
// items get a bullet, and user mentions become their account ID.
func StripStorage(storage string) string {
	decoder := xml.NewDecoder(strings.NewReader(storage))
	// Storage format is a fragment, not a document: it uses undeclared
	// ac:/ri: prefixes and HTML entities like &nbsp;. Non-strict mode with
	// the HTML entity table copes with both, and also closes stray tags so
	// malformed markup does not discard the rest of a comment.
	//
	// xml.HTMLAutoClose is deliberately NOT set: it treats every element
	// whose local name is an HTML void element as self-closing, which
	// swallows Confluence's <ac:link>…</ac:link> — the wrapper around every
	// mention and page link — and drops everything after it.
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity

	var out strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Non-strict decoding rarely fails; when it does, keep what
			// was recovered rather than losing the comment entirely.
			break
		}

		switch t := token.(type) {
		case xml.CharData:
			out.WriteString(string(t))
		case xml.StartElement:
			writeStartElement(&out, t)
		case xml.EndElement:
			if blockElements[strings.ToLower(t.Name.Local)] {
				out.WriteByte('\n')
			}
		}
	}

	return tidyLines(out.String())
}

func writeStartElement(out *strings.Builder, el xml.StartElement) {
	switch strings.ToLower(el.Name.Local) {
	case "li":
		out.WriteString("- ")
	case "br":
		// Self-closing in practice, so the end element never arrives.
		out.WriteByte('\n')
	case "td", "th":
		out.WriteByte(' ')
	case "user":
		// <ri:user ri:account-id="..."/> is a mention. The account ID is
		// all the body carries; resolving it to a name would cost a
		// request per mention.
		if id := attr(el, "account-id"); id != "" {
			out.WriteString("@" + id)
		}
	}
}

func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// tidyLines squeezes each line's whitespace down to single spaces and drops
// the blank lines that unwrapping markup leaves behind. strings.Fields splits
// on unicode.IsSpace, which covers the non-breaking spaces the HTML entity
// table decodes as well as ordinary ones.
func tidyLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// StripHighlightMarkers removes the markers Confluence search wraps around
// matched terms and decodes the HTML entities that come with them. A value
// without markers passes through unchanged, so this is safe to apply even
// if the server stops adding them.
func StripHighlightMarkers(s string) string {
	s = strings.ReplaceAll(s, highlightStart, "")
	s = strings.ReplaceAll(s, highlightEnd, "")
	return html.UnescapeString(s)
}

// Indent prefixes every line of text, leaving blank lines unpadded so no
// trailing whitespace is emitted.
func Indent(text, prefix string) string {
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = strings.TrimRight(prefix, " ")
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
