package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/178inaba/cflio/internal/sidecar"
	"github.com/spf13/cobra"
)

func newReadCmd(g *globalFlags) *cobra.Command {
	var (
		outPath        string
		markdown       bool
		attachmentsDir string
		outFormat      format.Format
	)

	cmd := &cobra.Command{
		Use:   "read <page-url|page-id>",
		Short: "Download a page's body to a file",
		Long: `Download a page's body, exactly as the API returns it in the storage
representation, to a local file plus a metadata sidecar.

The body is never printed: edit the downloaded file with your regular file
editing tools and write it back with ` + "`cflio update`" + `.

` + "`--markdown`" + ` converts the body to Markdown for reading instead. That file
carries no sidecar and cannot be written back, so use it when the page is
only going to be read, and the storage default when it might be edited.

` + "`--attachments <dir>`" + ` additionally downloads the attachments the body's images
point at into that directory and links to them, so the page's images can be
read as files. It requires ` + "`--markdown`" + `, only fetches the attachments the body
actually references, and keeps a file already sitting at the destination
rather than replacing it. An attachment it cannot fetch leaves the image
rendered as its filename and is counted in Unchecked.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReadPage(cmd, args, g, outPath, markdown, attachmentsDir, outFormat)
		},
	}

	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"file to write the body to (default ./<page-id>.xml, or .md with --markdown)")
	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().BoolVar(&markdown, "markdown", false,
		"convert the body to Markdown for reading; the file gets no sidecar and cannot be updated")
	cmd.Flags().StringVar(&attachmentsDir, "attachments", "",
		"directory to download the attachments the body's images reference into, linking to them; requires --markdown")
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// readResult is what read prints: metadata and file paths, never the body.
type readResult struct {
	PageID   string `json:"page_id"`
	Version  int    `json:"version"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	PageURL  string `json:"page_url"`
	BodyPath string `json:"body_path"`
	// SidecarPath is empty in Markdown mode, where no sidecar is written.
	SidecarPath string `json:"sidecar_path,omitempty"`
	Bytes       int    `json:"bytes"`
	// Unsupported and UnsupportedCount describe what the Markdown conversion
	// could not represent, so the caller can tell a lossy rendering from a
	// faithful one without opening the file.
	Unsupported      []string `json:"unsupported,omitempty"`
	UnsupportedCount int      `json:"unsupported_count,omitempty"`
	// UncheckedCount counts the references whose lookup produced no answer:
	// the request failed, or it was never attempted for want of a space key.
	// A lookup that ran and matched nothing is not counted — that reference is
	// genuinely unresolvable, and the fallback is the final answer. Both render
	// identically, so without this count a caller cannot tell a rendering that
	// is missing names and links that do exist from one that is complete.
	UncheckedCount int `json:"unchecked_count,omitempty"`
}

func runReadPage(cmd *cobra.Command, args []string, g *globalFlags, outPath string, markdown bool, attachmentsDir string, outFormat format.Format) error {
	// Refused rather than ignored: a storage body is written back verbatim,
	// so there would be nothing for the downloaded files to be linked from,
	// and silently writing them would leave the caller waiting for links
	// that are never coming.
	if attachmentsDir != "" && !markdown {
		return errors.New("--attachments requires --markdown: a storage body is never rewritten, " +
			"so it cannot link the downloaded files")
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

	page, err := client.GetPage(ctx, ref.PageID, true)
	if err != nil {
		return err
	}

	bodyPath := outPath
	if bodyPath == "" {
		// The two modes default to different names, so reading a page both
		// ways leaves two files rather than one clobbering the other.
		bodyPath = page.ID + ".xml"
		if markdown {
			bodyPath = page.ID + ".md"
		}
	}

	// Any sidecar already sitting next to the output path describes whatever
	// was read there before, so it is dropped first. Writing the body while
	// a stale sidecar survives would leave a pair that names one page and
	// holds another's content, and a later `update` would happily write this
	// body to that page. Failing here instead leaves no sidecar at all,
	// which `update` refuses outright.
	if err := sidecar.Remove(bodyPath); err != nil {
		return err
	}

	body := page.Body.Storage.Value
	var (
		converted format.Result
		resolved  references
	)
	if markdown {
		// The request above still asked for the storage representation, which
		// is the only one that carries the macros and the code bodies intact.
		// Converting it is purely local; what the converter cannot do itself
		// is turn the identifiers a body names into names and URLs, so that
		// much is looked up first and handed in.
		refs := format.References(body)
		resolved = resolveReferences(ctx, client, creds.SiteURL, pageref.SpaceKeyOf(page.Links.WebUI), refs)
		if attachmentsDir != "" {
			paths, unchecked := downloadReferencedAttachments(ctx, client, page.ID, attachmentsDir, refs.Attachments)
			resolved.opts.AttachmentPaths = paths
			resolved.unchecked += unchecked
		}
		converted = format.ToMarkdown(body, resolved.opts)
		body = converted.Markdown
	}
	if err := writeBody(bodyPath, body); err != nil {
		return err
	}

	meta := sidecar.Meta{
		PageID:  page.ID,
		Version: page.Version.Number,
		Title:   page.Title,
		Status:  page.Status,
		PageURL: pageref.PageURL(creds.SiteURL, page.Links.WebUI, page.ID),
	}

	result := readResult{
		PageID:           meta.PageID,
		Version:          meta.Version,
		Title:            meta.Title,
		Status:           meta.Status,
		PageURL:          meta.PageURL,
		BodyPath:         bodyPath,
		Bytes:            len(body),
		Unsupported:      converted.Unsupported,
		UnsupportedCount: converted.UnsupportedCount,
		UncheckedCount:   resolved.unchecked,
	}

	// A converted body is not a checkout: it carries no version lock and can
	// never be written back, so it gets no sidecar. That the sidecar exists
	// if and only if the file beside it is updatable is what `update` relies
	// on to refuse this file.
	if !markdown {
		if err := sidecar.Write(bodyPath, meta); err != nil {
			return err
		}
		result.SidecarPath = sidecar.Path(bodyPath)
	}

	return writeReadResult(cmd, outFormat, result)
}

// references is a resolution pass and how much of it went unanswered, the
// counterpart to format.Result for the lookups that precede the conversion.
type references struct {
	opts format.Options
	// unchecked counts the distinct references whose lookup produced no
	// answer, either because the request failed or because there was nothing
	// to query. A lookup that ran and came back empty is not one of them —
	// that reference is unresolvable, which is a settled answer rather than a
	// missing one.
	unchecked int
}

// resolveReferences looks up the names and URLs the converter cannot derive
// from the body alone: a mention carries an account ID, and a page link
// carries a space key and a title but no address.
//
// spaceKey is the space the page itself is in, which is what a link to a page
// in the same space resolves against — storage writes those without a key.
//
// No lookup failure is propagated. A reference to a deleted account, or to a
// page this token cannot see, is an ordinary thing to find in a page body,
// and the rendering already degrades to the identifier; failing the read over
// it would deny the caller the other 99% of the page over a detail.
//
// The failures are counted instead and reported alongside the resolution, the
// way ToMarkdown reports what it could not represent alongside the Markdown.
// downloadReferencedAttachments answers the third reference kind on the same
// terms, and adds its count to this one's.
func resolveReferences(ctx context.Context, client *confluence.Client, siteURL, spaceKey string, refs format.Refs) references {
	var opts format.Options
	var unchecked int
	if len(refs.AccountIDs) > 0 {
		// A failed batch takes the whole lookup down with it: Users discards
		// the chunks that did succeed, so none of these account IDs has an
		// answer.
		if users, err := client.Users(ctx, refs.AccountIDs); err == nil {
			opts.UserNames = make(map[string]string, len(users))
			for _, user := range users {
				if user.DisplayName != "" {
					opts.UserNames[user.AccountID] = user.DisplayName
				}
			}
		} else {
			unchecked += len(refs.AccountIDs)
		}
	}

	if len(refs.Pages) > 0 {
		opts.PageURLs = make(map[format.PageRef]string, len(refs.Pages))
	}
	groups, unkeyed := pagesBySpace(refs.Pages, spaceKey)
	unchecked += unkeyed
	for key, group := range groups {
		titles := make([]string, 0, len(group))
		for _, ref := range group {
			titles = append(titles, ref.Title)
		}

		matches, err := client.PagesByTitle(ctx, key, titles)
		if err != nil {
			unchecked += len(group)
			continue
		}

		ids := make(map[string]string, len(matches))
		for _, match := range matches {
			// The search API hands titles back HTML-escaped and marked up for
			// highlighting, while the title in the body arrives decoded, so
			// the two only compare after the same normalisation `search` uses.
			if title := format.StripHighlightMarkers(match.Title); ids[title] == "" {
				ids[title] = match.ID
			}
		}

		for _, ref := range group {
			if id := ids[ref.Title]; id != "" {
				// Keyed by the reference exactly as the body wrote it, empty
				// space key and all: that is what the converter looks up.
				opts.PageURLs[ref] = pageref.SpacePageURL(siteURL, key, id)
			}
		}
	}

	return references{opts: opts, unchecked: unchecked}
}

// pagesBySpace groups page references by the space each one resolves in,
// which for a reference that names no space is the page's own. References
// that resolve in no space at all are dropped: with no key there is nothing
// to query. How many were dropped is returned as unkeyed, because a reference
// that was never looked up leaves the same question open as one whose lookup
// failed.
func pagesBySpace(refs []format.PageRef, pageSpaceKey string) (groups map[string][]format.PageRef, unkeyed int) {
	groups = make(map[string][]format.PageRef)
	for _, ref := range refs {
		key := cmp.Or(ref.SpaceKey, pageSpaceKey)
		if key == "" {
			unkeyed++
			continue
		}
		groups[key] = append(groups[key], ref)
	}
	return groups, unkeyed
}

// downloadReferencedAttachments fetches the attachments the body's images are
// sourced from into dir, and returns the destination each one is linked from,
// keyed by filename, plus how many references it could not answer.
//
// Only the referenced filenames are fetched. The page's other attachments are
// `cflio attachments download`'s job, and pulling a 4 MB PDF because it
// happens to be attached is exactly the cost its --pattern exists to make
// deliberate.
//
// No failure is propagated, on the same terms as resolveReferences: an image
// that could not be fetched renders as its filename, which is what it renders
// without the flag at all. The count draws the distinction Unchecked already
// draws — a filename the listing does not hold is a settled answer, since the
// attachment is genuinely not on the page, while a listing or a transfer that
// failed is no answer at all.
func downloadReferencedAttachments(
	ctx context.Context, client *confluence.Client, pageID, dir string, filenames []string,
) (map[string]string, int) {
	if len(filenames) == 0 {
		// A flag with nothing to do leaves nothing behind: no request, and
		// no directory either.
		return nil, 0
	}

	// maxLimit rather than a --limit of its own, for the reason
	// runAttachmentsDownload gives: a referenced attachment outside the
	// window would be a wrong answer, not a truncated listing.
	attachments, hasMore, err := client.PageAttachments(ctx, pageID, maxLimit)
	if err != nil {
		// The listing is what says which of these filenames the page holds,
		// so without it none of them has an answer — not even the ones a
		// file is already sitting at, since nothing confirms that file is
		// this page's attachment.
		return nil, len(filenames)
	}

	byFilename := make(map[string]confluence.Attachment, len(attachments))
	for _, a := range attachments {
		byFilename[a.Title] = a
	}

	unchecked := 0
	var planned []plannedDownload
	for _, filename := range filenames {
		a, ok := byFilename[filename]
		if !ok {
			// Not attached to the page — settled, unless the listing was cut
			// short, in which case it may simply not have been read.
			if hasMore {
				unchecked++
			}
			continue
		}

		dest, err := attachmentDest(a.Title, dir)
		if err != nil {
			unchecked++
			continue
		}
		planned = append(planned, plannedDownload{attachment: a, dest: dest})
	}
	if len(planned) == 0 {
		return nil, unchecked
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, unchecked + len(planned)
	}

	paths := make(map[string]string, len(planned))
	for _, p := range planned {
		if !fetchAttachment(ctx, client, p) {
			unchecked++
			continue
		}
		paths[p.attachment.Title] = attachmentLink(dir, p.attachment.Title)
	}
	return paths, unchecked
}

// fetchAttachment writes one attachment to its destination unless a file is
// already there, reporting whether the destination now holds a file to link.
//
// An existing file is kept rather than replaced or re-downloaded, including
// one that appears between the check and the create — which downloadToFile's
// O_EXCL turns into an fs.ErrExist. This is a deliberate departure from
// `attachments download`, which refuses its whole run on a collision: a read
// must not fail over the state of a directory, or reading the same page twice
// would stop working the second time.
func fetchAttachment(ctx context.Context, client *confluence.Client, p plannedDownload) bool {
	// Lstat rather than Stat, so a dangling symlink counts as an entry here —
	// which is what the O_EXCL create would say about it too.
	if _, err := os.Lstat(p.dest); err == nil {
		return true
	}
	if _, err := downloadToFile(ctx, client, p.attachment.DownloadLink, p.dest); err != nil {
		return errors.Is(err, fs.ErrExist)
	}
	return true
}

// attachmentLink builds the destination an image links its downloaded file
// from. The directory goes in exactly as it was given, so the link resolves
// from the working directory — the same place --attachments and -o are
// themselves interpreted from — and it is joined with a forward slash,
// because what is being built is a link rather than a filesystem path.
func attachmentLink(dir, filename string) string {
	return strings.TrimRight(dir, "/") + "/" + filename
}

// writeBody writes the page body verbatim, with no trailing newline added:
// `update` sends the file back as-is, so a single extra byte would turn a
// no-op update into a content change on the page. That constraint is the
// storage path's; a converted body arrives with its own trailing newline.
//
// WriteString rather than os.WriteFile([]byte(body)): converting the body to
// a byte slice would copy the whole page, which for the large pages this
// tool exists to handle is the copy worth avoiding.
func writeBody(path, body string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() // safety net for the early return below

	if _, err := f.WriteString(body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Closed explicitly as well, so a flush failure is reported rather than
	// silently swallowed by the deferred close.
	if err := f.Close(); err != nil {
		return fmt.Errorf("finalize %s: %w", path, err)
	}
	return nil
}

func writeReadResult(cmd *cobra.Command, outFormat format.Format, result readResult) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Title:    %s\nVersion:  %d\nStatus:   %s\nURL:      %s\nBody:     %s (%d bytes)\n",
		result.Title, result.Version, result.Status, result.PageURL, result.BodyPath, result.Bytes)
	if result.SidecarPath != "" {
		fmt.Fprintf(&out, "Sidecar:  %s\n", result.SidecarPath)
	}
	if result.UnsupportedCount > 0 {
		fmt.Fprintf(&out, "Degraded: %d (%s)\n",
			result.UnsupportedCount, strings.Join(result.Unsupported, ", "))
	}
	// "not looked up" rather than "lookup failed": the same count covers the
	// references that were never queried at all.
	if result.UncheckedCount > 0 {
		fmt.Fprintf(&out, "Unchecked: %d (not looked up; names and links may be missing)\n",
			result.UncheckedCount)
	}

	_, err := fmt.Fprint(cmd.OutOrStdout(), out.String())
	return err
}
