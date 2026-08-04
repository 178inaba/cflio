package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// maxLimit bounds --limit so a typo cannot turn one command into thousands
// of paged requests against the invocation's deadline.
const maxLimit = 1000

func validateLimit(limit int) error {
	if limit < 1 || limit > maxLimit {
		return fmt.Errorf("invalid --limit %d: must be between 1 and %d", limit, maxLimit)
	}
	return nil
}

// markdownItem is a list entry that can render itself for the Markdown
// output. The JSON output uses the concrete types' struct tags instead.
type markdownItem interface {
	markdown() string
}

// writeList renders items per --format. name labels the JSON array and the
// "no results" line, e.g. "results" or "child pages".
func writeList[T markdownItem](cmd *cobra.Command, name string, items []T, notice string) error {
	out := cmd.OutOrStdout()

	if formatFlag == "json" {
		encoded, err := marshalList(name, items, notice)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(encoded))
		return err
	}

	if len(items) == 0 {
		if _, err := fmt.Fprintf(out, "No %s.\n", name); err != nil {
			return err
		}
	}
	for _, item := range items {
		if _, err := fmt.Fprintln(out, item.markdown()); err != nil {
			return err
		}
	}
	if notice != "" {
		if _, err := fmt.Fprintf(out, "\n%s\n", notice); err != nil {
			return err
		}
	}
	return nil
}

// marshalList builds {"<name>": [...], "notice": "..."} with the array
// always present, so a consumer can index it without a nil check.
func marshalList[T any](name string, items []T, notice string) ([]byte, error) {
	if items == nil {
		items = []T{}
	}

	payload := map[string]any{jsonKey(name): items}
	if notice != "" {
		payload["notice"] = notice
	}
	return json.MarshalIndent(payload, "", "  ")
}

// jsonKey turns a human label like "child pages" into "child_pages".
func jsonKey(name string) string {
	key := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ' ' {
			r = '_'
		}
		key = append(key, r)
	}
	return string(key)
}
