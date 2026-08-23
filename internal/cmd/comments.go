package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/spf13/cobra"
)

// defaultCommentsLimit is lower than the other commands' because each root
// comment costs an extra request for its replies.
const defaultCommentsLimit = 25

func newCommentsCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comments",
		Short: "Read a page's comments and post new ones",
		// `cflio comments <page>` was this command before `create` joined
		// it, and a page id is not a misspelling of `list`, so the
		// suggestions cobra offers for a mistyped subcommand never fire for
		// the retired form. The hint is what names its replacement.
		Annotations: map[string]string{
			unknownCommandHint: "A page's comments are now read with " +
				"`cflio comments list <page-url|page-id>`.",
		},
	}
	cmd.AddCommand(newCommentsListCmd(g), newCommentsCreateCmd(g))
	return cmd
}

func newCommentsListCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "list <page-url|page-id>",
		Short: "Read a page's footer and inline comments",
		Long: `Print a page's footer comments and inline comments, oldest first, with
their direct replies. Inline comments also show the text they are anchored
to and whether they are resolved.

This command only reads. Posting a footer comment is ` + "`cflio comments create`" + `;
replies and inline comments cannot be posted at all.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComments(cmd, args, g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultCommentsLimit,
		"maximum number of root comments per section, and replies per comment")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func newCommentsCreateCmd(g *globalFlags) *cobra.Command {
	var (
		file      string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "create <page-url|page-id> -f <file>",
		Short: "Post a file as a footer comment on a page",
		Long: `Post a file's contents as a footer comment on a page.

The file is sent as the storage representation, byte for byte: nothing is
converted on the way, so a comment can carry the same macros a page body
can -- and Markdown written into the file posts as the literal characters
you typed, not as formatting.

` + "`-f -`" + ` reads the body from stdin instead of from a file.

Top-level comments only. This command posts no replies, and no inline
comments, which have to be anchored to a span of the body.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommentsCreate(cmd, args, g, file, outFormat)
		},
	}

	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"file holding the comment body as storage XHTML; - reads it from stdin")
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// commentResult is what `comments create` prints on success. The comment ID
// is what a later reply would be posted against, once there is a command
// that posts one.
type commentResult struct {
	CommentID  string `json:"comment_id"`
	PageID     string `json:"page_id"`
	CommentURL string `json:"comment_url"`
}

func runCommentsCreate(cmd *cobra.Command, args []string, g *globalFlags, file string, outFormat format.Format) error {
	// Hoisted ahead of the request rather than left to the writer, as in
	// `attachments download`: this is the other command that would otherwise
	// fail on the output format with the outward-facing work already done,
	// and a comment cannot be taken back.
	if err := outFormat.Validate(); err != nil {
		return err
	}

	body, err := readCommentBody(cmd, file)
	if err != nil {
		return err
	}

	ref, err := pageref.Parse(args[0])
	if err != nil {
		return err
	}

	client, creds, err := resolveClient(g.profile, ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	comment, err := client.CreateFooterComment(ctx, ref.PageID, string(body))
	if err != nil {
		return err
	}

	return writeCommentResult(cmd, outFormat, commentResult{
		CommentID:  comment.ID,
		PageID:     ref.PageID,
		CommentURL: pageref.PageURL(creds.SiteURL, comment.Links.WebUI, ref.PageID),
	})
}

// readCommentBody reads the comment body from file, or from stdin when file
// is "-", as `gh` and `kubectl` spell the same thing. The bytes are returned
// as they were read: converting them here is exactly what this command exists
// not to do.
func readCommentBody(cmd *cobra.Command, file string) ([]byte, error) {
	if file == "-" {
		body, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("read the comment body from stdin: %w", err)
		}
		return body, nil
	}

	body, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return body, nil
}

// writeCommentResult renders one result per --format, the way
// writeUpdateResult does. It does not validate the format: its caller hoists
// that check ahead of posting the comment.
func writeCommentResult(cmd *cobra.Command, outFormat format.Format, result commentResult) error {
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Comment:  %s\nPage:     %s\nURL:      %s\n",
		result.CommentID, result.PageID, result.CommentURL)
	return err
}

// commentItem is one rendered comment. Author is an Atlassian account ID:
// resolving display names would cost a request per distinct author. Body is
// Markdown with no trailing newline, so whatever renders it terminates it
// itself.
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

func runComments(cmd *cobra.Command, args []string, g *globalFlags, limit int, outFormat format.Format) error {
	if err := validateLimit(limit); err != nil {
		return err
	}

	ref, err := pageref.Parse(args[0])
	if err != nil {
		return err
	}

	client, _, err := resolveClient(g.profile, ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
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
		section, err := collectComments(ctx, client, kind.kind, ref.PageID, limit)
		if err != nil {
			return err
		}
		section.Title, section.Key = kind.title, kind.key
		sections = append(sections, section)
	}

	return writeComments(cmd, outFormat, sections)
}

func collectComments(ctx context.Context, client *confluence.Client, kind confluence.CommentKind, pageID string, limit int) (commentSection, error) {
	roots, hasMore, err := client.PageComments(ctx, kind, pageID, limit)
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
		replies, moreReplies, err := client.CommentReplies(ctx, kind, root.ID, limit)
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
		// Comment endpoints reject body-format=view, so the storage XHTML is
		// converted here, by the same converter `read --markdown` uses: a
		// comment carries code blocks and tables as often as a page does.
		Body:      strings.TrimSuffix(format.ToMarkdown(c.Body.Storage.Value, format.Options{}).Markdown, "\n"),
		Highlight: c.Properties.InlineOriginalSelection,
		Status:    c.ResolutionStatus,
	}
}

func writeComments(cmd *cobra.Command, outFormat format.Format, sections []commentSection) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if outFormat == format.JSON {
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
