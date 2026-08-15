// Package format holds what the commands share when rendering API results:
// the --format enum they render in, and the text helpers themselves —
// storage-XHTML to Markdown conversion, search highlight-marker stripping
// and indentation.
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
	"fmt"
	"html"
	"strings"
)

// Format is the output format a command renders its result in. It implements
// pflag.Value, so an unknown --format is rejected while cobra parses the
// flags: before PreRunE and RunE both, which leaves no hook a command could
// take over and no way to reach a command body with an unvalidated value.
type Format string

// The two accepted --format values.
const (
	Markdown Format = "md"
	JSON     Format = "json"
)

func (f Format) String() string { return string(f) }

// Set implements pflag.Value. It assigns only after validating, so a typo
// leaves the receiver — and with it the default the flag was registered
// with — untouched.
func (f *Format) Set(s string) error {
	v := Format(s)
	if err := v.Validate(); err != nil {
		return err
	}
	*f = v
	return nil
}

// Type names the value cobra prints in the --format help line. It reports
// "string" rather than "format" because that line is part of the agent-facing
// contract kept in sync with README.md and skills/cflio/SKILL.md.
func (Format) Type() string { return "string" }

// Validate rejects a Format that is not one of the accepted values. Set is not the
// only way to build a Format — a conversion from an arbitrary string still
// type-checks — so the callers that branch on one keep this guard rather than
// trusting the type.
func (f Format) Validate() error {
	switch f {
	case Markdown, JSON:
		return nil
	}
	return fmt.Errorf("invalid --format %q: must be %q or %q", string(f), Markdown, JSON)
}

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
