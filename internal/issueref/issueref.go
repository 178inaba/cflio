// Package issueref parses the ways a caller can name a Jira issue: its key,
// or a browser URL the key can be read from.
package issueref

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Ref is a parsed issue reference. Host is empty when the caller passed a
// bare key, in which case there is nothing to select a profile from.
type Ref struct {
	Key  string
	Host string
}

// keyPattern is an issue key: a project key — a letter, then letters, digits
// or underscores — a hyphen and the issue number. Lowercase is accepted
// because Jira matches keys case-insensitively; the key Jira returns is the
// one any output uses.
var keyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)

// Parse accepts an issue key or an issue URL. A URL is read in this order:
// a selectedIssue query parameter, then /browse/<KEY>, then the last segment
// of a path under /jira/. selectedIssue comes first because it names the
// issue open in a panel over whatever page the path itself shows.
func Parse(arg string) (Ref, error) {
	arg = strings.TrimSpace(arg)

	if keyPattern.MatchString(arg) {
		return Ref{Key: arg}, nil
	}

	u, err := url.Parse(arg)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return Ref{}, unrecognizedError(arg)
	}

	p := strings.TrimSuffix(u.Path, "/")
	selected, hasSelected := u.Query()["selectedIssue"]
	browse, isBrowse := strings.CutPrefix(p, "/browse/")
	underJira := strings.HasPrefix(p, "/jira/")

	switch {
	case hasSelected && keyPattern.MatchString(selected[0]):
		return Ref{Key: selected[0], Host: u.Host}, nil
	case isBrowse && keyPattern.MatchString(browse):
		return Ref{Key: browse, Host: u.Host}, nil
	case underJira && keyPattern.MatchString(path.Base(p)):
		return Ref{Key: path.Base(p), Host: u.Host}, nil
	}

	if hasSelected || isBrowse || underJira {
		return Ref{}, fmt.Errorf("%q is a Jira URL that names no issue; pass an issue URL "+
			"(e.g. https://example.atlassian.net/browse/KAN-1), a board or list URL with an issue "+
			"open in its panel, or an issue key (e.g. KAN-1)", arg)
	}
	return Ref{}, unrecognizedError(arg)
}

func unrecognizedError(arg string) error {
	return fmt.Errorf("%q is not an issue URL or issue key; pass either an issue key (e.g. KAN-1) "+
		"or an issue URL (e.g. https://example.atlassian.net/browse/KAN-1)", arg)
}
