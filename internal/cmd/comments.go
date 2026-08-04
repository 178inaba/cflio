package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/spf13/cobra"
)

// defaultCommentsLimit is lower than the other commands' because each root
// comment costs an extra request for its replies.
const defaultCommentsLimit = 25

var commentsLimitFlag int

var commentsCmd = &cobra.Command{
	Use:   "comments <page-url|page-id>",
	Short: "Read a page's footer and inline comments",
	Long: `Print a page's footer comments and inline comments, oldest first, with
their direct replies. Inline comments also show the text they are anchored
to and whether they are resolved.

Read-only: cflio never posts or replies.`,
	Args: cobra.ExactArgs(1),
	RunE: runComments,
}

func init() {
	commentsCmd.Flags().IntVar(&commentsLimitFlag, "limit", defaultCommentsLimit,
		"maximum number of root comments per section, and replies per comment")
}

// commentItem is one rendered comment. Author is an Atlassian account ID:
// resolving display names would cost a request per distinct author.
type commentItem struct {
	ID        string        `json:"id"`
	Author    string        `json:"author_account_id"`
	CreatedAt string        `json:"created_at"`
	Body      string        `json:"body"`
	Highlight string        `json:"highlighted_text,omitempty"`
	Status    string        `json:"resolution_status,omitempty"`
	Replies   []commentItem `json:"replies,omitempty"`
}

// commentSection is one of the two comment families on a page.
type commentSection struct {
	Title    string        `json:"-"`
	Key      string        `json:"-"`
	Comments []commentItem `json:"comments"`
	Notice   string        `json:"notice,omitempty"`
}

func runComments(cmd *cobra.Command, args []string) error {
	if err := validateLimit(commentsLimitFlag); err != nil {
		return err
	}

	ref, err := pageref.Parse(args[0])
	if err != nil {
		return err
	}

	client, _, err := resolveClient(ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd)
	defer cancel()

	sections := make([]commentSection, 0, 2)
	for _, kind := range []struct {
		kind  confluence.CommentKind
		title string
		key   string
	}{
		{confluence.FooterComments, "Footer comments", "footer_comments"},
		{confluence.InlineComments, "Inline comments", "inline_comments"},
	} {
		section, err := collectComments(ctx, client, kind.kind, ref.PageID)
		if err != nil {
			return err
		}
		section.Title, section.Key = kind.title, kind.key
		sections = append(sections, section)
	}

	return writeComments(cmd, sections)
}

func collectComments(ctx context.Context, client *confluence.Client, kind confluence.CommentKind, pageID string) (commentSection, error) {
	roots, hasMore, err := client.PageComments(ctx, kind, pageID, commentsLimitFlag)
	if err != nil {
		return commentSection{}, err
	}

	section := commentSection{Comments: make([]commentItem, 0, len(roots))}
	if hasMore {
		section.Notice = "More comments available; raise --limit to fetch them."
	}

	for _, root := range roots {
		item := commentItemFrom(root)

		// The page-level endpoints return root comments only, so replies
		// have to be fetched per comment or the answers to every question
		// would silently go missing.
		replies, moreReplies, err := client.CommentReplies(ctx, kind, root.ID, commentsLimitFlag)
		if err != nil {
			return commentSection{}, err
		}
		for _, reply := range replies {
			item.Replies = append(item.Replies, commentItemFrom(reply))
		}
		if moreReplies {
			section.Notice = "More comments or replies available; raise --limit to fetch them."
		}

		section.Comments = append(section.Comments, item)
	}
	return section, nil
}

func commentItemFrom(c confluence.Comment) commentItem {
	return commentItem{
		ID:        c.ID,
		Author:    c.Version.AuthorID,
		CreatedAt: c.Version.CreatedAt,
		// Comment endpoints reject body-format=view, so the storage XHTML
		// is made readable here. This is display only; page bodies are
		// never converted.
		Body:      format.StripStorage(c.Body.Storage.Value),
		Highlight: c.Properties.InlineOriginalSelection,
		Status:    c.ResolutionStatus,
	}
}

func writeComments(cmd *cobra.Command, sections []commentSection) error {
	if formatFlag == "json" {
		payload := make(map[string]any, len(sections))
		for _, section := range sections {
			payload[section.Key] = section
		}
		return writeJSON(cmd, payload)
	}

	out := cmd.OutOrStdout()
	for i, section := range sections {
		if i > 0 {
			if _, err := fmt.Fprintln(out); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(out, "## %s\n\n", section.Title); err != nil {
			return err
		}
		if _, err := fmt.Fprint(out, section.markdown()); err != nil {
			return err
		}
	}
	return nil
}

func (s commentSection) markdown() string {
	if len(s.Comments) == 0 {
		return "None.\n"
	}

	var b strings.Builder
	for _, comment := range s.Comments {
		b.WriteString(comment.markdown(""))
		for _, reply := range comment.Replies {
			b.WriteString(reply.markdown("  "))
		}
	}
	if s.Notice != "" {
		b.WriteString("\n" + s.Notice + "\n")
	}
	return b.String()
}

func (c commentItem) markdown(indent string) string {
	var b strings.Builder

	b.WriteString(indent + "- " + c.Author)
	if c.CreatedAt != "" {
		b.WriteString(" at " + c.CreatedAt)
	}
	if c.Status != "" {
		b.WriteString(" [" + c.Status + "]")
	}
	b.WriteString("\n")

	if c.Highlight != "" {
		b.WriteString(format.Indent("on: "+c.Highlight, indent+"  ") + "\n")
	}
	if c.Body != "" {
		b.WriteString(format.Indent(c.Body, indent+"  ") + "\n")
	}
	return b.String()
}
