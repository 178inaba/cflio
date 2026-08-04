package cmd

import (
	"fmt"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/spf13/cobra"
)

const defaultSearchLimit = 20

var searchLimitFlag int

var searchCmd = &cobra.Command{
	Use:   "search <cql>",
	Short: "Search content with CQL",
	Long: `Search with Confluence Query Language.

The query is passed to Confluence unchanged, so use CQL's own syntax
(e.g. 'type = page and space = "DEV" and text ~ "release notes"').

CQL matches more than pages — blog posts, attachments and comments can come
back too — so each result shows its type rather than being filtered out.`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

func init() {
	searchCmd.Flags().IntVar(&searchLimitFlag, "limit", defaultSearchLimit,
		"maximum number of results to fetch")
}

// searchItem is one rendered result. ID is empty for hits that are not
// content (spaces, users), which carry no content object.
type searchItem struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	URL   string `json:"url"`
}

func runSearch(cmd *cobra.Command, args []string) error {
	if err := validateLimit(searchLimitFlag); err != nil {
		return err
	}

	client, creds, err := resolveClient("")
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd)
	defer cancel()

	results, total, err := client.Search(ctx, args[0], searchLimitFlag)
	if err != nil {
		return err
	}

	items := make([]searchItem, 0, len(results))
	for _, r := range results {
		items = append(items, searchItemFrom(r, creds.SiteURL))
	}

	// v1 search is the only endpoint here that reports a total, so it is
	// the only one whose truncation notice can carry a count.
	notice := ""
	if more := total - len(items); more > 0 {
		notice = fmt.Sprintf("%d more results; raise --limit to fetch them", more)
	}

	return writeList(cmd, "results", items, notice)
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
