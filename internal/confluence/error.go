package confluence

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxRawBodyInError bounds how much of an unparseable error body is echoed.
// Confluence answers some failures with a full HTML page, and dumping that
// into a terminal buries the status line that actually matters.
const maxRawBodyInError = 300

// APIError is a non-2xx response. It keeps the status code accessible
// because the API documents no status for a stale-version conflict on page
// update, so that case can only be surfaced, never anticipated.
type APIError struct {
	Method   string
	Path     string
	Status   int
	Messages []string
	Body     string
}

func (e *APIError) Error() string {
	detail := strings.Join(e.Messages, "; ")
	if detail == "" {
		detail = truncate(strings.TrimSpace(e.Body), maxRawBodyInError)
	}
	if detail == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, detail)
}

// newAPIError parses whichever error envelope the responding API version
// uses. v2 returns {"errors":[{"title","detail"}]}, v1 returns
// {"message":…}; anything else falls back to the raw body.
func newAPIError(method, path string, status int, body []byte) *APIError {
	err := &APIError{Method: method, Path: path, Status: status, Body: string(body)}

	var v2 struct {
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if jsonErr := json.Unmarshal(body, &v2); jsonErr == nil {
		for _, e := range v2.Errors {
			if msg := joinNonEmpty(": ", e.Title, e.Detail); msg != "" {
				err.Messages = append(err.Messages, msg)
			}
		}
	}
	if len(err.Messages) > 0 {
		return err
	}

	var v1 struct {
		Message string `json:"message"`
	}
	if jsonErr := json.Unmarshal(body, &v1); jsonErr == nil && v1.Message != "" {
		err.Messages = append(err.Messages, v1.Message)
	}
	return err
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

// truncate cuts s to at most limit bytes, stepping back to a rune boundary
// so a multi-byte character is never split into invalid UTF-8.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}
