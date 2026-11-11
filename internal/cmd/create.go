package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/178inaba/atlio/internal/confluence"
	"github.com/178inaba/atlio/internal/format"
	"github.com/178inaba/atlio/internal/pageref"
	"github.com/178inaba/atlio/internal/sidecar"
	"github.com/spf13/cobra"
)

func newCreateCmd(g *globalFlags) *cobra.Command {
	var (
		parent    string
		title     string
		file      string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "create --parent <page-url|page-id> --title <title> -f <file>",
		Short: "Create a page under a parent from a file",
		Long: `Create a published page under --parent, titled --title, with the contents of
the file given with -f as its body.

The file is sent as the storage representation, byte for byte: nothing is
converted on the way, so Markdown written into it posts as the literal
characters you typed, not as formatting. An empty file creates an empty page.

The page is created in the parent's space, as a published classic page:
not a draft, not a live doc, and never at the root of a space. --parent
takes a page URL, a short link or a page ID, and a URL selects the profile.

On success the file becomes a checkout of the new page: a sidecar is
written next to it, as ` + "`atlio confluence read`" + ` would, so the file can be
edited and written back with ` + "`atlio confluence update`" + ` straight away. A file
that already has a sidecar is refused before anything is sent: it is
already a checkout of a page, so use ` + "`atlio confluence update`" + ` to write to that
page, or pass a file with no sidecar to create a new one. -f - is refused
too: the sidecar has to be written beside a file.

Exit code 124 does not mean the page was not created: the deadline can
expire after the server has made it. Before running create again, check with
` + "`atlio confluence children <parent>`" + `; if the page is there, run
` + "`atlio confluence read`" + ` on it to get a checkout instead of creating a second
one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCreatePage(cmd, g, parent, title, file, outFormat)
		},
	}

	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().StringVar(&parent, "parent", "",
		"page to create the new page under, as a URL, short link or page ID")
	cmd.Flags().StringVar(&title, "title", "", "title of the new page")
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"file holding the body as storage XHTML; the sidecar is written next to it")
	for _, name := range []string{"parent", "title", "file"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// createResult is what create prints on success. The title and the URL are
// the ones the server returned.
type createResult struct {
	PageID  string `json:"page_id"`
	Title   string `json:"title"`
	PageURL string `json:"page_url"`
}

func runCreatePage(cmd *cobra.Command, g *globalFlags, parent, title, file string, outFormat format.Format) error {
	// Every check that can fail runs before the first request: a page cannot
	// be taken back, so failing on the output format or the file after the
	// create would leave one behind with nothing to show for it.
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if title == "" {
		return errors.New("--title cannot be empty: a published page needs a title")
	}
	if file == "-" {
		return errors.New("-f - is not accepted: the body must be a file, " +
			"because the new page's sidecar is written beside it")
	}

	body, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}

	exists, err := sidecar.Exists(file)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%s already exists, so %s is already a checkout of a page: "+
			"use `atlio confluence update` to write to that page, "+
			"or pass a file with no sidecar to create a new one", sidecar.Path(file), file)
	}

	ref, err := pageref.Parse(parent)
	if err != nil {
		return err
	}

	client, err := resolveConfluence(g.profile, ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	parentPage, err := client.GetPage(ctx, ref.PageID, false)
	if err != nil {
		return err
	}

	created, err := client.CreatePage(ctx, confluence.NewCreatePageRequest(
		parentPage.SpaceID, ref.PageID, statusCurrent, title, string(body)))
	if err != nil {
		return err
	}

	meta := sidecar.Meta{
		PageID:  created.ID,
		Version: created.Version.Number,
		Title:   created.Title,
		Status:  created.Status,
		PageURL: pageref.PageURL(client.BaseURL(), created.Links.WebUI, created.ID),
		Subtype: &created.Subtype,
	}
	if err := sidecar.Create(file, meta); err != nil {
		// No -o in the advice: a read into this file would remove whatever
		// is at the sidecar's path, which may be the entry Create just
		// refused to replace.
		return fmt.Errorf("page %s was created at %s, but %s is not a checkout of it: %w; "+
			"run `atlio confluence read %s` to get one rather than running create again",
			created.ID, meta.PageURL, file, err, meta.PageURL)
	}

	return writeCreateResult(cmd, outFormat, createResult{
		PageID:  meta.PageID,
		Title:   meta.Title,
		PageURL: meta.PageURL,
	})
}

// writeCreateResult renders one result per --format. It does not validate the
// format: its caller hoists that check ahead of creating the page.
func writeCreateResult(cmd *cobra.Command, outFormat format.Format, result createResult) error {
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Created:  %s\nURL:      %s\n", result.Title, result.PageURL)
	return err
}
