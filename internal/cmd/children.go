package cmd

import (
	"fmt"

	"github.com/178inaba/cflio/internal/pageref"
	"github.com/spf13/cobra"
)

const defaultChildrenLimit = 100

func newChildrenCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat string
	)

	cmd := &cobra.Command{
		Use:   "children <page-url|page-id>",
		Short: "List a page's direct child pages",
		Long: `List the pages directly beneath a page.

Only one level is listed: to walk further down the tree, run the command
again on a child.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChildren(cmd, args, g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultChildrenLimit,
		"maximum number of child pages to fetch")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

type childItem struct {
	Title  string `json:"title"`
	ID     string `json:"id"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

func (c childItem) markdown() string {
	return fmt.Sprintf("- **%s** (ID %s, %s)\n  %s", c.Title, c.ID, c.Status, c.URL)
}

func runChildren(cmd *cobra.Command, args []string, g *globalFlags, limit int, outFormat string) error {
	if err := validateLimit(limit); err != nil {
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

	children, hasMore, err := client.ChildPages(ctx, ref.PageID, limit)
	if err != nil {
		return err
	}

	// Child listings carry neither a link of their own nor the child's own
	// space. Every child of a page lives in that page's space, so the
	// parent's web link is what their URLs are built from — one lookup for
	// the whole listing, and none at all when there is nothing to build.
	parentWebUI := ""
	if len(children) > 0 {
		parent, err := client.GetPage(ctx, ref.PageID, false)
		if err != nil {
			return err
		}
		parentWebUI = parent.Links.WebUI
	}

	items := make([]childItem, 0, len(children))
	for _, child := range children {
		items = append(items, childItem{
			Title:  child.Title,
			ID:     child.ID,
			Status: child.Status,
			URL:    pageref.ChildPageURL(creds.SiteURL, parentWebUI, child.ID),
		})
	}

	// v2 pages with opaque cursors and reports no total, so the notice
	// cannot say how many child pages were left out.
	notice := ""
	if hasMore {
		notice = "More child pages available; raise --limit to fetch them."
	}

	return writeList(cmd, outFormat, "child pages", items, notice)
}
