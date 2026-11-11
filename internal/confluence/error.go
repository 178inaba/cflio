package confluence

import (
	"encoding/json/v2"
	"strings"
)

// apiErrorMessages parses whichever error envelope the responding API
// version uses. v2 returns {"errors":[{"title","detail"}]}, v1 returns
// {"message":…}; anything else yields nil, leaving the raw body to speak.
func apiErrorMessages(body []byte) []string {
	var v2 struct {
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	var messages []string
	if err := json.Unmarshal(body, &v2); err == nil {
		for _, e := range v2.Errors {
			if msg := joinNonEmpty(": ", e.Title, e.Detail); msg != "" {
				messages = append(messages, msg)
			}
		}
	}
	if len(messages) > 0 {
		return messages
	}

	var v1 struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &v1); err == nil && v1.Message != "" {
		return []string{v1.Message}
	}
	return nil
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
