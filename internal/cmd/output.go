package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// maxLimit bounds --limit so a typo cannot turn one command into thousands
// of paged requests against the invocation's deadline.
const maxLimit = 1000

// The two --format values.
const (
	formatMarkdown = "md"
	formatJSON     = "json"
)

func validateLimit(limit int) error {
	if limit < 1 || limit > maxLimit {
		return fmt.Errorf("invalid --limit %d: must be between 1 and %d", limit, maxLimit)
	}
	return nil
}

// addFormatFlag registers --format on a single command, along with the check
// that rejects an unknown value. It is registered per command rather than on
// the root so it never appears on `auth` and `profile`, which would silently
// ignore it; pairing the two here means a command cannot take the flag
// without also validating it.
//
// The check hangs off PreRunE rather than starting RunE because cobra
// validates required flags in between: `update --format bogus` with no -f
// has to name the bad format, not the missing file.
func addFormatFlag(cmd *cobra.Command, outFormat *string) {
	cmd.Flags().StringVar(outFormat, "format", formatMarkdown, `output format: "md" or "json"`)
	cmd.PreRunE = func(*cobra.Command, []string) error {
		if *outFormat != formatMarkdown && *outFormat != formatJSON {
			return fmt.Errorf(`invalid --format %q: must be "md" or "json"`, *outFormat)
		}
		return nil
	}
}

// writeJSON renders payload as the --format json output. Every command's
// JSON branch goes through here so indentation and framing stay identical
// across them.
func writeJSON(cmd *cobra.Command, payload any) error {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	return err
}

// markdownItem is a list entry that can render itself for the Markdown
// output. The JSON output uses the concrete types' struct tags instead.
type markdownItem interface {
	markdown() string
}

// writeList renders items per --format. name labels the JSON array and the
// "no results" line, e.g. "results" or "child pages".
func writeList[T markdownItem](cmd *cobra.Command, outFormat, name string, items []T, notice string) error {
	if outFormat == formatJSON {
		return writeJSON(cmd, listPayload(name, items, notice))
	}

	out := cmd.OutOrStdout()
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

// listPayload builds {"<name>": [...], "notice": "..."} with the array
// always present, so a consumer can index it without a nil check.
func listPayload[T any](name string, items []T, notice string) map[string]any {
	if items == nil {
		items = []T{}
	}

	payload := map[string]any{jsonKey(name): items}
	if notice != "" {
		payload["notice"] = notice
	}
	return payload
}

// jsonKey turns a human label like "child pages" into "child_pages".
func jsonKey(name string) string {
	return strings.ReplaceAll(name, " ", "_")
}
