package cmd

import (
	"cmp"
	"context"
	"fmt"
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
		outPath   string
		markdown  bool
		outFormat format.Format
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
only going to be read, and the storage default when it might be edited.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReadPage(cmd, args, g, outPath, markdown, outFormat)
		},
	}

	cmd.Flags().StringVarP(&outPath, "output", "o", "",
		"file to write the body to (default ./<page-id>.xml, or .md with --markdown)")
	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().BoolVar(&markdown, "markdown", false,
		"convert the body to Markdown for reading; the file gets no sidecar and cannot be updated")
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

func runReadPage(cmd *cobra.Command, args []string, g *globalFlags, outPath string, markdown bool, outFormat format.Format) error {
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
		resolved = resolveReferences(ctx, client, creds.SiteURL, pageref.SpaceKeyOf(page.Links.WebUI), body)
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
func resolveReferences(ctx context.Context, client *confluence.Client, siteURL, spaceKey, storage string) references {
	refs := format.References(storage)

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
