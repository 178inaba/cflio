// Package format holds the text helpers the commands share when rendering
// API results: storage-XHTML to Markdown conversion, search highlight-marker
// stripping and indentation.
//
// Nothing here is part of an edit round-trip. A page body that will be
// written back is read from and written to a file untouched, so that path
// stays byte-lossless; the conversions in this package produce output for
// reading only. ToMarkdown serves both readers of a storage body: `read
// --markdown`, whose output carries no sidecar and cannot be updated, and
// comment bodies, which arrive as storage XHTML because the comment
// endpoints refuse body-format=view.
package format

import (
	"html"
	"strings"
)

// Highlight markers Confluence wraps around matched terms when a search
// runs with the default excerpt=highlight.
const (
	highlightStart = "@@@hl@@@"
	highlightEnd   = "@@@endhl@@@"
)

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
