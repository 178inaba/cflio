package cmd

import (
	"fmt"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/spf13/cobra"
)

const defaultSearchLimit = 20

func newSearchCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "search <cql>",
		Short: "Search content with CQL",
		Long: `Search with Confluence Query Language.

The query is passed to Confluence unchanged, so use CQL's own syntax
(e.g. 'type = page and space = "DEV" and text ~ "release notes"').

CQL matches more than pages — blog posts, attachments and comments can come
back too — so each result shows its type rather than being filtered out.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args, g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultSearchLimit,
		"maximum number of results to fetch")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// searchItem is one rendered result. ID is empty for hits that are not
// content (spaces, users), which carry no content object.
type searchItem struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	URL   string `json:"url"`
}

func runSearch(cmd *cobra.Command, args []string, g *globalFlags, limit int, outFormat format.Format) error {
	if err := validateLimit(limit); err != nil {
		return err
	}

	client, creds, err := resolveClient(g.profile, "")
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	results, total, err := client.Search(ctx, args[0], limit)
	if err != nil {
		return err
	}

	items := make([]searchItem, 0, len(results))
	for _, r := range results {
		items = append(items, searchItemFrom(r, creds.SiteURL))
	}

	// v1 search is the only endpoint here that reports a total, so it is
	// the only one whose truncation notice can carry a count. The notice is
	// gated on having actually filled the limit: Search stops early when the
	// server runs out of hits, and in that case a totalSize larger than what
	// came back does not mean raising --limit would fetch more.
	notice := ""
	if more := total - len(items); more > 0 && len(items) == limit {
		notice = fmt.Sprintf("%d more results; raise --limit to fetch them", more)
	}

	return writeList(cmd, outFormat, "results", items, notice)
}

func searchItemFrom(r confluence.SearchResult, site string) searchItem {
	item := searchItem{
		Title: format.StripHighlightMarkers(r.Title),
		Type:  r.EntityType,
		URL:   absoluteURL(site, r.URL),
	}
	if r.Content != nil {
		item.Title = format.StripHighlightMarkers(r.Content.Title)
		item.Type = r.Content.Type
		item.ID = r.Content.ID
	}
	return item
}

// absoluteURL joins a site-relative result link to the site base. The v1
// schema does not document the link as relative, so an already-absolute
// value is passed through rather than mangled.
func absoluteURL(site, link string) string {
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}
	return strings.TrimSuffix(site, "/") + "/" + strings.TrimPrefix(link, "/")
}

func (i searchItem) markdown() string {
	head := fmt.Sprintf("- **%s** (%s", i.Title, i.Type)
	if i.ID != "" {
		head += ", ID " + i.ID
	}
	head += ")"
	if i.URL != "" {
		head += "\n  " + i.URL
	}
	return head
}
