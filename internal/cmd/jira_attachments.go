package cmd

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/178inaba/atlio/internal/format"
	"github.com/178inaba/atlio/internal/jira"
	"github.com/spf13/cobra"
)

func newJiraAttachmentsCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachments",
		Short: "List and download an issue's attachments",
	}
	cmd.AddCommand(newJiraAttachmentsListCmd(g), newJiraAttachmentsDownloadCmd(g))
	return cmd
}

func newJiraAttachmentsListCmd(g *globalFlags) *cobra.Command {
	var outFormat format.Format

	cmd := &cobra.Command{
		Use:   "list <issue-key|issue-url>",
		Short: "List the files attached to an issue",
		Long: `List an issue's attachments with their filename, media type and size.

Download the ones you want with
` + "`atlio jira attachments download`" + `: the filename shown here is what its
--pattern matches against. Jira returns every attachment of an issue at once,
so there is no --limit.

The issue is named by its key or its URL, as for ` + "`jira issue read`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJiraAttachmentsList(cmd, args[0], g, outFormat)
		},
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func newJiraAttachmentsDownloadCmd(g *globalFlags) *cobra.Command {
	var (
		pattern   string
		outDir    string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "download <issue-key|issue-url> --pattern <glob>",
		Short: "Download an issue's attachments matching a filename glob",
		Long: `Download the attachments whose filename matches a glob, so an image on an
issue can be read with your regular file-reading tools. An image in a
description cannot be linked to its attachment from the Markdown
` + "`jira issue read`" + ` writes, so downloading it by filename is how to see it.

--pattern is required and case-sensitive: there is no "download everything"
form, so pulling a large PDF while reaching for a screenshot has to be asked
for on purpose (--pattern '*' does it). A pattern matching nothing is an
error rather than a silent success.

An existing file is never replaced. If any attachment would overwrite one,
the command fails before downloading anything and names the file, so a run
cannot leave some files replaced and others not.

Jira lets two attachments on one issue share a filename. If two matched
attachments have filenames that are equal ignoring case (a.png and A.png are
one file on a case-insensitive filesystem), the command fails before
downloading anything and names them. Narrow --pattern to pick one of a.png
and A.png; two attachments with exactly the same filename cannot be told
apart by --pattern, so leave them out of it.

Read-only against Jira: every request is a GET.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJiraAttachmentsDownload(cmd, args[0], g, pattern, outDir, outFormat)
		},
	}

	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().StringVar(&pattern, "pattern", "",
		"filename glob selecting which attachments to download, e.g. *.png")
	cmd.Flags().StringVarP(&outDir, "output", "o", ".",
		"directory to write the downloaded files to, created if it does not exist")
	if err := cmd.MarkFlagRequired("pattern"); err != nil {
		panic(err)
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func runJiraAttachmentsList(cmd *cobra.Command, arg string, g *globalFlags, outFormat format.Format) error {
	client, key, err := resolveIssueClient(arg, g.profile)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	attachments, err := client.Attachments(ctx, key)
	if err != nil {
		return err
	}

	items := make([]attachmentItem, 0, len(attachments))
	for _, a := range attachments {
		items = append(items, attachmentItem{Filename: a.Filename, MediaType: a.MimeType, FileSize: a.Size})
	}
	return writeList(cmd, outFormat, "attachments", items, "")
}

func runJiraAttachmentsDownload(cmd *cobra.Command, arg string, g *globalFlags, pattern, outDir string, outFormat format.Format) error {
	// Ahead of the transfers for the reason runAttachmentsDownload gives.
	if err := outFormat.Validate(); err != nil {
		return err
	}

	client, key, err := resolveIssueClient(arg, g.profile)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	attachments, err := client.Attachments(ctx, key)
	if err != nil {
		return err
	}

	files := make([]attachmentFile, 0, len(attachments))
	for _, a := range attachments {
		files = append(files, jiraAttachmentFile(client, a))
	}
	matched, err := matchAttachments(files, pattern)
	if err != nil {
		return err
	}
	if len(matched) == 0 {
		return fmt.Errorf("no attachment matches %q (%d on the issue)", pattern, len(attachments))
	}
	if err := refuseCaseInsensitiveDuplicates(matched); err != nil {
		return err
	}

	planned, err := planDownloads(matched, outDir)
	if err != nil {
		return err
	}
	items, err := writeDownloads(ctx, planned, outDir)
	if err != nil {
		return err
	}
	return writeList(cmd, outFormat, "downloaded attachments", items, "")
}

// jiraAttachmentFile adapts an issue attachment to the download paths.
func jiraAttachmentFile(client *jira.Client, a jira.Attachment) attachmentFile {
	return attachmentFile{
		filename: a.Filename,
		fetch: func(ctx context.Context, w io.Writer) (int64, error) {
			return client.DownloadAttachment(ctx, a.ID, w)
		},
	}
}

// refuseCaseInsensitiveDuplicates fails when two matched attachments have
// filenames equal ignoring case.
//
// Jira allows two attachments on an issue to share a filename, and a.png and
// A.png are one file on a case-insensitive filesystem. Either way the second
// write would fail only after the first had landed, so the run is refused
// before any transfer, on every platform, so a collision writes nothing.
// Filenames that differ only in Unicode normalisation are not detected.
//
// The error says which way out applies to each group: a glob can pick one of
// a.png and A.png, but nothing matched against the filename can tell two
// identical ones apart.
func refuseCaseInsensitiveDuplicates(matched []attachmentFile) error {
	var caseOnly, identical []string
	seen := make([]bool, len(matched))
	for i, a := range matched {
		if seen[i] {
			continue
		}
		group := []string{a.filename}
		for j := i + 1; j < len(matched); j++ {
			if !seen[j] && strings.EqualFold(a.filename, matched[j].filename) {
				seen[j] = true
				group = append(group, matched[j].filename)
			}
		}
		switch {
		case len(group) == 1:
		case hasRepeat(group):
			identical = append(identical, strings.Join(group, " and "))
		default:
			caseOnly = append(caseOnly, strings.Join(group, " and "))
		}
	}

	var reasons []string
	if len(caseOnly) > 0 {
		reasons = append(reasons, fmt.Sprintf("attachments %s differ only in case and would be "+
			"written to the same file; narrow --pattern to pick one", strings.Join(caseOnly, ", ")))
	}
	if len(identical) > 0 {
		reasons = append(reasons, fmt.Sprintf("attachments %s share a filename and cannot be told "+
			"apart by --pattern; change it to leave them out", strings.Join(identical, ", ")))
	}
	if len(reasons) > 0 {
		return fmt.Errorf("%s (nothing was downloaded)", strings.Join(reasons, "; "))
	}
	return nil
}

// hasRepeat reports whether any filename occurs more than once in names.
func hasRepeat(names []string) bool {
	for i, n := range names {
		if slices.Contains(names[i+1:], n) {
			return true
		}
	}
	return false
}
