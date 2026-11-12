package cmd

import (
	"fmt"
	"strings"

	"github.com/178inaba/atlio/internal/format"
	"github.com/178inaba/atlio/internal/jira"
	"github.com/spf13/cobra"
)

func newJiraSearchCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "search <jql>",
		Short: "Search issues with JQL",
		Long: `List the issues a Jira Query Language query matches: key, summary, type,
status, assignee, when each was last updated, and its URL.

The query is passed to Jira unchanged, so use JQL's own syntax
(e.g. 'project = KAN and status != Done order by updated desc').

A query needs at least one search restriction. Jira rejects one that only
orders (e.g. 'order by created desc' alone) and the error carries Jira's own
message.

Jira reports no total for a search, so when more issues match than --limit
the listing says only that more are available.

A query names no site, so the profile comes from --profile, else
ATLIO_PROFILE, else the default profile.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJiraSearch(cmd, args[0], g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultSearchLimit,
		"maximum number of issues to fetch")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

type jiraSearchItem struct {
	Key       string `json:"key"`
	Summary   string `json:"summary"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Assignee  string `json:"assignee,omitempty"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
}

func (i jiraSearchItem) markdown() string {
	details := []string{i.Type, i.Status}
	if i.Assignee != "" {
		details = append(details, i.Assignee)
	}
	details = append(details, "updated "+i.UpdatedAt)
	return fmt.Sprintf("- **%s** %s (%s)\n  %s", i.Key, i.Summary, strings.Join(details, ", "), i.URL)
}

func runJiraSearch(cmd *cobra.Command, jql string, g *globalFlags, limit int, outFormat format.Format) error {
	if err := validateLimit(limit); err != nil {
		return err
	}

	client, err := resolveJira(g.profile, "")
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	issues, hasMore, err := client.Search(ctx, jql, limit)
	if err != nil {
		return err
	}

	items := make([]jiraSearchItem, 0, len(issues))
	for _, issue := range issues {
		items = append(items, jiraSearchItemFrom(issue, client.BrowseURL(issue.Key)))
	}

	notice := ""
	if hasMore {
		notice = "More issues available; raise --limit to fetch them."
	}

	return writeList(cmd, outFormat, "issues", items, notice)
}

func jiraSearchItemFrom(issue jira.SearchIssue, url string) jiraSearchItem {
	f := issue.Fields
	item := jiraSearchItem{
		Key:       issue.Key,
		Summary:   f.Summary,
		Type:      f.IssueType.Name,
		Status:    f.Status.Name,
		UpdatedAt: f.Updated,
		URL:       url,
	}
	if f.Assignee != nil {
		item.Assignee = f.Assignee.DisplayName
	}
	return item
}
