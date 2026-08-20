package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/spf13/cobra"
)

const defaultAttachmentsLimit = 100

func newAttachmentsCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachments",
		Short: "List and download a page's attachments",
	}
	cmd.AddCommand(newAttachmentsListCmd(g), newAttachmentsDownloadCmd(g))
	return cmd
}

func newAttachmentsListCmd(g *globalFlags) *cobra.Command {
	var (
		limit     int
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "list <page-url|page-id>",
		Short: "List the files attached to a page",
		Long: `List a page's attachments with their filename, media type and size.

Download the ones you want with ` + "`cflio attachments download`" + `: the filename
shown here is what its --pattern matches against.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttachmentsList(cmd, args, g, limit, outFormat)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", defaultAttachmentsLimit,
		"maximum number of attachments to fetch")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

func newAttachmentsDownloadCmd(g *globalFlags) *cobra.Command {
	var (
		pattern   string
		outDir    string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "download <page-url|page-id> --pattern <glob>",
		Short: "Download a page's attachments matching a filename glob",
		Long: `Download the attachments whose filename matches a glob, so an image on a
page can be read with your regular file-reading tools.

--pattern is required and case-sensitive: there is no "download everything"
form, so pulling a large PDF while reaching for a screenshot has to be asked
for on purpose (--pattern '*' does it). A pattern matching nothing is an
error rather than a silent success.

An existing file is never replaced. If any attachment would overwrite one,
the command fails before downloading anything and names the file, so a run
cannot leave some files replaced and others not.

Read-only against Confluence: cflio never uploads an attachment.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttachmentsDownload(cmd, args, g, pattern, outDir, outFormat)
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

// attachmentItem is one rendered attachment. Filename is the API's title,
// named for what it is. FileSize is the server's own byte count, printed raw
// the way `read` prints the size of the file it wrote.
type attachmentItem struct {
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	FileSize  int64  `json:"file_size"`
}

func (a attachmentItem) markdown() string {
	return fmt.Sprintf("- **%s** (%s, %d bytes)", a.Filename, a.MediaType, a.FileSize)
}

func runAttachmentsList(cmd *cobra.Command, args []string, g *globalFlags, limit int, outFormat format.Format) error {
	if err := validateLimit(limit); err != nil {
		return err
	}

	client, pageID, err := resolvePageClient(args[0], g.profile)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	attachments, hasMore, err := client.PageAttachments(ctx, pageID, limit)
	if err != nil {
		return err
	}

	items := make([]attachmentItem, 0, len(attachments))
	for _, a := range attachments {
		items = append(items, attachmentItem{Filename: a.Title, MediaType: a.MediaType, FileSize: a.FileSize})
	}

	// v2 pages with opaque cursors and reports no total, so the notice
	// cannot say how many attachments were left out.
	notice := ""
	if hasMore {
		notice = "More attachments available; raise --limit to fetch them."
	}

	return writeList(cmd, outFormat, "attachments", items, notice)
}

// downloadedItem is one file `download` wrote. Bytes is what was actually
// written, not the size the listing reported for it.
type downloadedItem struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
}

func (d downloadedItem) markdown() string {
	return fmt.Sprintf("- %s (%d bytes)", d.Path, d.Bytes)
}

func runAttachmentsDownload(cmd *cobra.Command, args []string, g *globalFlags, pattern, outDir string, outFormat format.Format) error {
	// Validated before the request rather than left to writeList, so a typo in
	// --format is reported without a download having already happened.
	if err := outFormat.Validate(); err != nil {
		return err
	}

	client, pageID, err := resolvePageClient(args[0], g.profile)
	if err != nil {
		return err
	}

	// One context for the listing and every transfer: --timeout is the
	// deadline for the invocation, not for each request in it.
	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	// maxLimit rather than a --limit of its own: a matching attachment left
	// outside the window would be a wrong answer, not a truncated listing, and
	// there would be no flag to raise. If the page holds more than this, the
	// messages below say so instead of claiming what was not read.
	attachments, hasMore, err := client.PageAttachments(ctx, pageID, maxLimit)
	if err != nil {
		return err
	}

	matched, err := matchAttachments(attachments, pattern)
	if err != nil {
		return err
	}
	if len(matched) == 0 {
		msg := fmt.Sprintf("no attachment matches %q (%d on the page)", pattern, len(attachments))
		if hasMore {
			msg += "; the page holds more attachments than this command read"
		}
		return errors.New(msg)
	}

	destinations, err := planDestinations(matched, outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}

	items := make([]downloadedItem, 0, len(matched))
	for i, a := range matched {
		n, err := downloadToFile(ctx, client, a.DownloadLink, destinations[i])
		if err != nil {
			return err
		}
		items = append(items, downloadedItem{Filename: a.Title, Path: destinations[i], Bytes: n})
	}

	notice := ""
	if hasMore {
		notice = "The page holds more attachments than this command read, so the match may be incomplete."
	}

	return writeList(cmd, outFormat, "downloaded attachments", items, notice)
}

// resolvePageClient parses the page argument and builds the client for the site
// it names. Both subcommands go through here, so a page URL and a bare page ID
// are interchangeable on each of them with the same profile resolution.
func resolvePageClient(arg, profile string) (*confluence.Client, string, error) {
	ref, err := pageref.Parse(arg)
	if err != nil {
		return nil, "", err
	}

	client, _, err := resolveClient(profile, ref.Host)
	if err != nil {
		return nil, "", err
	}
	return client, ref.PageID, nil
}

// matchAttachments keeps the attachments whose filename matches pattern.
//
// path.Match rather than filepath.Match: an attachment's title is a filename,
// and filepath's meaning of a backslash is platform-dependent, so the same
// pattern would select differently on Windows. Matching is case-sensitive, as
// in `gh release download`.
//
// A malformed pattern is reported rather than left to match nothing, which
// would be indistinguishable from a pattern that is merely too specific.
func matchAttachments(attachments []confluence.Attachment, pattern string) ([]confluence.Attachment, error) {
	var matched []confluence.Attachment
	for _, a := range attachments {
		ok, err := path.Match(pattern, a.Title)
		if err != nil {
			return nil, fmt.Errorf("invalid --pattern %q: %w", pattern, err)
		}
		if ok {
			matched = append(matched, a)
		}
	}
	return matched, nil
}

// planDestinations resolves where each matched attachment will be written, and
// refuses the whole run if any of those files already exists.
//
// Checking every destination up front is what keeps the refusal from being
// half-applied: finding the collision after two of three transfers would leave
// a directory neither the caller nor a re-run can reason about. Lstat rather
// than Stat, so a dangling symlink counts as an existing entry here — which is
// what the O_EXCL create would say about it too.
//
// The filename comes from the server, so it is also what pins the write inside
// outDir: a title carrying a path would otherwise land somewhere the caller
// never named. Base is the local filesystem's idea of a separator, which is
// the right one — this is a path being built, not a pattern being matched.
func planDestinations(matched []confluence.Attachment, outDir string) ([]string, error) {
	destinations := make([]string, 0, len(matched))
	var collisions []string

	for _, a := range matched {
		if filepath.Base(a.Title) != a.Title || a.Title == "." || a.Title == ".." {
			return nil, fmt.Errorf("attachment %q is not a plain filename; "+
				"refusing to write it outside %s", a.Title, outDir)
		}

		dest := filepath.Join(outDir, a.Title)
		if _, err := os.Lstat(dest); err == nil {
			collisions = append(collisions, dest)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("check %s: %w", dest, err)
		}
		destinations = append(destinations, dest)
	}

	if len(collisions) > 0 {
		return nil, fmt.Errorf("refusing to overwrite %s; delete them or pass a different -o "+
			"(nothing was downloaded)", strings.Join(collisions, ", "))
	}
	return destinations, nil
}

// downloadToFile streams one attachment to dest and reports how many bytes it
// wrote.
//
// O_EXCL rather than a plain create: planDestinations has already ruled out a
// collision, and this is what keeps a file that appeared in between from being
// clobbered anyway. A transfer that fails part-way takes its file with it, so
// no truncated file is left at a name that looks complete.
func downloadToFile(ctx context.Context, client *confluence.Client, downloadLink, dest string) (int64, error) {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", dest, err)
	}

	n, err := client.DownloadAttachment(ctx, downloadLink, f)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(dest)
		return 0, err
	}
	// Closed explicitly as well, so a flush failure is reported rather than
	// silently swallowed — and the partial file it left goes with it.
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("finalize %s: %w", dest, err)
	}
	return n, nil
}
