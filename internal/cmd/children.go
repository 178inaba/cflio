package cmd

import (
	"fmt"

	"github.com/178inaba/cflio/internal/pageref"
	"github.com/spf13/cobra"
)

const defaultChildrenLimit = 100

var childrenLimitFlag int

var childrenCmd = &cobra.Command{
	Use:   "children <page-url|page-id>",
	Short: "List a page's direct child pages",
	Long: `List the pages directly beneath a page.

Only one level is listed: to walk further down the tree, run the command
again on a child.`,
	Args: cobra.ExactArgs(1),
	RunE: runChildren,
}

func init() {
	childrenCmd.Flags().IntVar(&childrenLimitFlag, "limit", defaultChildrenLimit,
		"maximum number of child pages to fetch")
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

func runChildren(cmd *cobra.Command, args []string) error {
	if err := validateLimit(childrenLimitFlag); err != nil {
		return err
	}

	ref, err := pageref.Parse(args[0])
	if err != nil {
		return err
	}

	client, creds, err := resolveClient(ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd)
	defer cancel()

	children, hasMore, err := client.ChildPages(ctx, ref.PageID, childrenLimitFlag)
	if err != nil {
		return err
	}

	// Child listings carry a space ID but no link of their own. Every child
	// of a page lives in that page's space, so one lookup covers them all.
	spaceKeys := map[string]string{}
	items := make([]childItem, 0, len(children))
	for _, child := range children {
		key, ok := spaceKeys[child.SpaceID]
		if !ok {
			if key, err = client.SpaceKey(ctx, child.SpaceID); err != nil {
				return err
			}
			spaceKeys[child.SpaceID] = key
		}

		items = append(items, childItem{
			Title:  child.Title,
			ID:     child.ID,
			Status: child.Status,
			URL:    pageref.SpacePageURL(creds.SiteURL, key, child.ID),
		})
	}

	// v2 pages with opaque cursors and reports no total, so the notice
	// cannot say how many child pages were left out.
	notice := ""
	if hasMore {
		notice = "More child pages available; raise --limit to fetch them."
	}

	return writeList(cmd, "child pages", items, notice)
}
