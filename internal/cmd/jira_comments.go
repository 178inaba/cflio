package cmd

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/178inaba/atlio/internal/format"
	"github.com/178inaba/atlio/internal/jira"
	"github.com/spf13/cobra"
)

func newJiraCommentsCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comments",
		Short: "Read an issue's comments",
	}
	cmd.AddCommand(newJiraCommentsListCmd(g))
	return cmd
}

func newJiraCommentsListCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "list <issue-key|issue-url>",
		Short: "Print an issue's comments",
		Long: `Print an issue's comments, oldest first: each one's author, when it was
posted and last edited, and its body converted from Atlassian Document Format
(ADF) to Markdown.

Whatever the conversion could not represent, across all the printed comments,
is counted once in Degraded.

At most --limit comments are printed. When the issue has more, the output
says how many there are in all; raise --limit to fetch the rest.

The issue is named by its key (KAN-1) or by a browser URL, as for
` + "`atlio jira issue read`" + `; a URL selects the profile registered for its site.

This command only reads: atlio posts nothing to Jira.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJiraCommentsList(cmd, args[0], g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultCommentsLimit,
		"maximum number of comments to fetch")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// jiraCommentItem is one rendered comment. Body is Markdown with no trailing
// newline, as commentItem's is.
type jiraCommentItem struct {
	ID string `json:"id"`
	// Author is empty, and left out, for a comment Jira names no author for:
	// an anonymous one, or one whose author it no longer reports.
	Author    string `json:"author,omitempty"`
	CreatedAt string `json:"created_at"`
	// UpdatedAt is left out when it equals CreatedAt: the comment was never
	// edited.
	UpdatedAt string `json:"updated_at,omitempty"`
	Body      string `json:"body"`
}

// jiraCommentsPayload is what `jira comments list` prints. Jira comments are
// one flat list, so there is one array where the Confluence command has two
// families. Unsupported and UnsupportedCount say omitempty and omitzero for
// the reason readResult's do: absent when there is nothing to report.
type jiraCommentsPayload struct {
	Comments         []jiraCommentItem `json:"comments"`
	Notice           string            `json:"notice,omitempty"`
	Unsupported      []string          `json:"unsupported,omitempty"`
	UnsupportedCount int               `json:"unsupported_count,omitzero"`
}

func runJiraCommentsList(cmd *cobra.Command, arg string, g *globalFlags, limit int, outFormat format.Format) error {
	if err := validateLimit(limit); err != nil {
		return err
	}

	client, key, err := resolveIssueClient(arg, g.profile)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	comments, total, err := client.Comments(ctx, key, limit)
	if err != nil {
		return err
	}

	payload := newJiraCommentsPayload(comments)
	// Gated on having filled the limit, as runSearch gates its notice: the
	// listing also ends on an empty page, and then a total larger than what
	// came back does not mean raising --limit would fetch more.
	if len(comments) == limit && total > limit {
		payload.Notice = fmt.Sprintf("Showing %d of %d comments; raise --limit to fetch the rest.", limit, total)
	}

	return writeJiraComments(cmd, outFormat, payload)
}

func newJiraCommentsPayload(comments []jira.Comment) jiraCommentsPayload {
	docs := make([][]byte, len(comments))
	for i, c := range comments {
		docs[i] = c.Body
	}
	converted := format.ADFDocumentsToMarkdown(docs)

	payload := jiraCommentsPayload{
		Comments:         make([]jiraCommentItem, 0, len(comments)),
		Unsupported:      converted.Unsupported,
		UnsupportedCount: converted.UnsupportedCount,
	}
	for i, c := range comments {
		item := jiraCommentItem{
			ID:        c.ID,
			CreatedAt: c.Created,
			Body:      strings.TrimSuffix(converted.Markdown[i], "\n"),
		}
		if c.Author != nil {
			item.Author = c.Author.DisplayName
		}
		if c.Updated != c.Created {
			item.UpdatedAt = c.Updated
		}
		payload.Comments = append(payload.Comments, item)
	}
	return payload
}

func writeJiraComments(cmd *cobra.Command, outFormat format.Format, payload jiraCommentsPayload) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if outFormat == format.JSON {
		return writeJSON(cmd, payload)
	}

	var out strings.Builder
	if len(payload.Comments) == 0 {
		out.WriteString("No comments.\n")
	}
	for _, c := range payload.Comments {
		out.WriteString(c.markdown())
	}
	if payload.Notice != "" {
		out.WriteString("\n" + payload.Notice + "\n")
	}
	if payload.UnsupportedCount > 0 {
		out.WriteString("\n" + degradedLine(payload.Unsupported, payload.UnsupportedCount))
	}

	_, err := fmt.Fprint(cmd.OutOrStdout(), out.String())
	return err
}

func (c jiraCommentItem) markdown() string {
	var b strings.Builder

	b.WriteString("- " + cmp.Or(c.Author, "Anonymous"))
	if c.CreatedAt != "" {
		b.WriteString(" at " + c.CreatedAt)
	}
	if c.UpdatedAt != "" {
		b.WriteString(" (updated " + c.UpdatedAt + ")")
	}
	b.WriteString("\n")

	if c.Body != "" {
		b.WriteString(format.Indent(c.Body, "  ") + "\n")
	}
	return b.String()
}
