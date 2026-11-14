package cmd

import (
	"context"
	"fmt"
	"io"
	"path"
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

Jira lets two attachments on one issue share a filename, which --pattern
cannot pick one of. When two matched attachments have filenames that would
be one file -- equal ignoring case and Unicode normalisation, as a.png and
A.png are on a case-insensitive filesystem -- each is written with its
attachment ID before the extension (screen.png becomes screen-10002.png);
the output's filename is still the attachment's own and its path is the
file written.

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
	nameCollisionsByID(matched)

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
		id:       a.ID,
		fetch: func(ctx context.Context, w io.Writer) (int64, error) {
			return client.DownloadAttachment(ctx, a.ID, w)
		},
	}
}

// nameCollisionsByID gives every matched attachment whose filename would be
// one file with another's a name carrying its ID.
//
// Jira allows two attachments on an issue to share a filename, and no glob
// can pick one of them; a.png and A.png are one file on a case-insensitive
// filesystem. Every member of such a group is renamed, not all but one, so
// which file gets which name does not depend on the order Jira lists them
// in. A name that still collides once the IDs are in is planDownloads' to
// refuse.
func nameCollisionsByID(matched []attachmentFile) {
	count := map[string]int{}
	for _, a := range matched {
		count[localFileKey(a.filename)]++
	}
	for i, a := range matched {
		if count[localFileKey(a.filename)] > 1 {
			matched[i].saveAs = withID(a.filename, a.id)
		}
	}
}

// withID inserts id before the filename's extension: screen.png becomes
// screen-10002.png. A name that is all extension, such as .env, gets the ID
// at its end, so the result still opens with the same name.
func withID(filename, id string) string {
	ext := path.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	if stem == "" {
		stem, ext = filename, ""
	}
	return stem + "-" + id + ext
}
