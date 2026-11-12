package cmd

import (
	"cmp"
	"encoding/json/jsontext"
	"fmt"
	"strings"

	"github.com/178inaba/atlio/internal/format"
	"github.com/178inaba/atlio/internal/issueref"
	"github.com/178inaba/atlio/internal/jira"
	"github.com/spf13/cobra"
)

// newJiraCmd builds the group holding every command that talks to Jira.
func newJiraCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jira",
		Short: "Read Jira issues",
	}
	cmd.AddCommand(newJiraIssueCmd(g), newJiraSearchCmd(g))
	return cmd
}

func newJiraIssueCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Read one Jira issue",
	}
	cmd.AddCommand(newJiraIssueReadCmd(g))
	return cmd
}

func newJiraIssueReadCmd(g *globalFlags) *cobra.Command {
	var (
		outPath   string
		markdown  bool
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "read <issue-key|issue-url>",
		Short: "Write an issue's rich-text fields to a file and print its metadata",
		Long: `Write an issue's rich-text fields to a local file and print the issue's
metadata: key, summary, type, status, people, labels, parent, subtasks, links,
dates and attachments.

Every field whose value is an Atlassian Document Format (ADF) document is
written — the description, the environment and multi-line custom fields — in
that order, custom fields by field ID. By default the file is one JSON object
holding those fields, keyed by field ID, each value exactly as the API
returned it (./<KEY>.adf.json).

` + "`--markdown`" + ` converts the same fields to Markdown for reading instead
(./<KEY>.md): the description first, then one section per other field, headed
by the field's name. That file is for reading only. Whatever the conversion
could not represent is counted in Degraded; read the default file when the task
depends on that part.

The issue is named by its key (KAN-1) or by a browser URL: /browse/KAN-1, a
board, backlog or list URL with the issue open in its panel (selectedIssue),
or an issue URL under /jira/. A URL selects the profile registered for its
site. The key Jira returns, which differs from the one given when the issue
has moved, is the one the output and the default filename use. An issue with
no rich-text field writes no file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJiraIssueRead(cmd, args[0], g, outPath, markdown, outFormat)
		},
	}

	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"file to write the fields to (default ./<KEY>.adf.json, or .md with --markdown)")
	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().BoolVar(&markdown, "markdown", false,
		"convert the fields to Markdown for reading")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// jiraIssueResult is what `jira issue read` prints: metadata and the file
// path, never a field's content. Key names follow the Confluence commands'
// where the two describe the same thing.
type jiraIssueResult struct {
	Key         string           `json:"key"`
	URL         string           `json:"url"`
	Summary     string           `json:"summary"`
	Type        string           `json:"type"`
	Status      string           `json:"status"`
	Priority    string           `json:"priority,omitempty"`
	Assignee    string           `json:"assignee,omitempty"`
	Reporter    string           `json:"reporter,omitempty"`
	Labels      []string         `json:"labels,omitempty"`
	Parent      *issueSummary    `json:"parent,omitzero"`
	Subtasks    []issueStatus    `json:"subtasks,omitempty"`
	Links       []issueLink      `json:"links,omitempty"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	Attachments []attachmentItem `json:"attachments,omitempty"`
	// writtenBody is nil when the issue has no rich-text field and nothing
	// was written; embedding it is what drops body_path and bytes together.
	*writtenBody `json:",embed"`
	// Unsupported and UnsupportedCount say omitempty and omitzero for the
	// reason readResult's do: absent when there is nothing to report.
	Unsupported      []string `json:"unsupported,omitempty"`
	UnsupportedCount int      `json:"unsupported_count,omitzero"`
}

type writtenBody struct {
	BodyPath string `json:"body_path"`
	Bytes    int    `json:"bytes"`
}

type issueSummary struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type issueStatus struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

type issueLink struct {
	// Relation is the link type's phrase as it reads from this issue:
	// "blocks" toward an outward issue, "is blocked by" toward an inward one.
	Relation string `json:"relation"`
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
}

func runJiraIssueRead(cmd *cobra.Command, arg string, g *globalFlags, outPath string, markdown bool, outFormat format.Format) error {
	ref, err := issueref.Parse(arg)
	if err != nil {
		return err
	}

	client, err := resolveJira(g.profile, ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	issue, err := client.GetIssue(ctx, ref.Key)
	if err != nil {
		return err
	}

	result := newJiraIssueResult(client.BrowseURL(issue.Key), issue)

	if fields := issue.ADFFields(); len(fields) > 0 {
		var name, body string
		if markdown {
			converted := format.ADFSectionsToMarkdown(issueSections(fields, issue.Names))
			name, body = issue.Key+".md", converted.Markdown
			result.Unsupported = converted.Unsupported
			result.UnsupportedCount = converted.UnsupportedCount
		} else {
			name = issue.Key + ".adf.json"
			if body, err = adfFile(fields); err != nil {
				return err
			}
		}

		bodyPath := cmp.Or(outPath, name)
		if err := writeFreshBody(bodyPath, body); err != nil {
			return err
		}
		result.writtenBody = &writtenBody{BodyPath: bodyPath, Bytes: len(body)}
	}

	return writeJiraIssueResult(cmd, outFormat, result)
}

func newJiraIssueResult(url string, issue *jira.Issue) jiraIssueResult {
	f := issue.Fields
	result := jiraIssueResult{
		Key:       issue.Key,
		URL:       url,
		Summary:   f.Summary,
		Type:      f.IssueType.Name,
		Status:    f.Status.Name,
		Labels:    f.Labels,
		CreatedAt: f.Created,
		UpdatedAt: f.Updated,
	}
	if f.Priority != nil {
		result.Priority = f.Priority.Name
	}
	if f.Assignee != nil {
		result.Assignee = f.Assignee.DisplayName
	}
	if f.Reporter != nil {
		result.Reporter = f.Reporter.DisplayName
	}
	if f.Parent != nil {
		result.Parent = &issueSummary{Key: f.Parent.Key, Summary: f.Parent.Fields.Summary}
	}
	for _, sub := range f.Subtasks {
		result.Subtasks = append(result.Subtasks,
			issueStatus{Key: sub.Key, Summary: sub.Fields.Summary, Status: sub.Fields.Status.Name})
	}
	for _, link := range f.IssueLinks {
		relation, other := link.Type.Outward, link.OutwardIssue
		if other == nil {
			relation, other = link.Type.Inward, link.InwardIssue
		}
		if other == nil {
			continue
		}
		result.Links = append(result.Links, issueLink{
			Relation: relation, Key: other.Key, Summary: other.Fields.Summary, Status: other.Fields.Status.Name,
		})
	}
	for _, a := range f.Attachments {
		result.Attachments = append(result.Attachments,
			attachmentItem{Filename: a.Filename, MediaType: a.MimeType, FileSize: a.Size})
	}
	return result
}

// adfFile renders the default-mode file: one JSON object of the ADF fields,
// keyed by field ID, in the order they were given. It is written by hand
// rather than marshalled because neither half survives an encoder: a map
// would come out in key order, which puts customfield_* ahead of
// description, and re-encoding a value would change the bytes the API sent.
func adfFile(fields []jira.Field) (string, error) {
	var out strings.Builder
	out.WriteString("{\n")
	for i, f := range fields {
		key, err := jsontext.AppendQuote(nil, f.ID)
		if err != nil {
			return "", fmt.Errorf("encode field id %q: %w", f.ID, err)
		}
		out.WriteString("  ")
		out.Write(key)
		out.WriteString(": ")
		out.Write(f.Value)
		if i < len(fields)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("}\n")
	return out.String(), nil
}

// issueSections lays the ADF fields out as the Markdown file's sections: the
// description first and unheaded, since it is what the issue says, then one
// section per other field under its display name.
func issueSections(fields []jira.Field, names map[string]string) []format.ADFSection {
	sections := make([]format.ADFSection, 0, len(fields))
	for _, f := range fields {
		section := format.ADFSection{Doc: f.Value}
		if f.ID != "description" {
			section.Heading = fieldName(f.ID, names)
		}
		sections = append(sections, section)
	}
	return sections
}

// fieldName is the name Jira displays for a field, or its ID when the
// response named it nothing.
func fieldName(id string, names map[string]string) string {
	if name := names[id]; name != "" {
		return name
	}
	return id
}

func writeJiraIssueResult(cmd *cobra.Command, outFormat format.Format, result jiraIssueResult) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	var out strings.Builder
	// Values start in column 10, as `confluence read` aligns them; a label
	// too long for that keeps one space after its colon.
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&out, "%-*s%s\n", max(10, len(label)+2), label+":", value)
		}
	}

	line("Key", result.Key)
	line("Summary", result.Summary)
	line("Type", result.Type)
	line("Status", result.Status)
	line("Priority", result.Priority)
	line("Assignee", result.Assignee)
	line("Reporter", result.Reporter)
	line("Labels", strings.Join(result.Labels, ", "))
	if p := result.Parent; p != nil {
		line("Parent", fmt.Sprintf("%s (%s)", p.Key, p.Summary))
	}
	for _, s := range result.Subtasks {
		line("Subtask", fmt.Sprintf("%s [%s] %s", s.Key, s.Status, s.Summary))
	}
	for _, l := range result.Links {
		line("Link", fmt.Sprintf("%s %s [%s] %s", l.Relation, l.Key, l.Status, l.Summary))
	}
	line("Created", result.CreatedAt)
	line("Updated", result.UpdatedAt)
	for _, a := range result.Attachments {
		line("Attachment", fmt.Sprintf("%s (%s, %d bytes)", a.Filename, a.MediaType, a.FileSize))
	}
	line("URL", result.URL)
	if b := result.writtenBody; b != nil {
		line("Body", fmt.Sprintf("%s (%d bytes)", b.BodyPath, b.Bytes))
	}
	if result.UnsupportedCount > 0 {
		fmt.Fprintf(&out, "Degraded: %d (%s)\n", result.UnsupportedCount, strings.Join(result.Unsupported, ", "))
	}

	_, err := fmt.Fprint(cmd.OutOrStdout(), out.String())
	return err
}
