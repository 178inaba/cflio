package atlassian

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	// maxErrorBody bounds how much of a failed response is read to build the
	// error from it. See responseError for why it is this far above the width
	// the message is finally truncated to.
	maxErrorBody = 64 << 10

	// maxRawBodyInError bounds how much of an unparseable error body is
	// echoed. A gateway answers some failures with a full HTML page, and
	// dumping that into a terminal buries the status line that actually
	// matters.
	maxRawBodyInError = 300
)

// APIError is a non-2xx response. It keeps the status code accessible
// because not every failure a caller has to handle is documented with a
// status of its own, so some can only be surfaced, never anticipated.
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

// responseError turns a non-2xx response into an *APIError.
//
// The body is read through a LimitReader because it is not always the small
// JSON envelope the API documents: a gateway answers with an HTML page, and the
// download path can be handed anything at all. maxErrorBody sits far above
// maxRawBodyInError, which is where the message actually gets cut — the limit
// here only stops an unbounded read, and cutting near the display width would
// split a multi-byte rune where truncate's rune-boundary rewind can no longer
// see it.
func (c *Client) responseError(method, path string, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("read response from %s %s: %w", method, path, err)
	}

	apiErr := &APIError{Method: method, Path: path, Status: resp.StatusCode, Body: string(body)}
	if c.messages != nil {
		apiErr.Messages = c.messages(body)
	}
	return apiErr
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
