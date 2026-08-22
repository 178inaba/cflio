package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/178inaba/cflio/internal/confluence"
	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/178inaba/cflio/internal/sidecar"
	"github.com/spf13/cobra"
)

// defaultVersionMessage labels every version cflio writes, so page history
// shows which edits came from an agent.
const defaultVersionMessage = "Updated via cflio"

// statusCurrent is the only page status cflio updates. The API also accepts
// "draft", but drafts number their versions differently, so writing one
// from a sidecar captured against a published page would be wrong.
const statusCurrent = "current"

func newUpdateCmd(g *globalFlags) *cobra.Command {
	var (
		file      string
		message   string
		title     string
		outFormat format.Format
	)

	cmd := &cobra.Command{
		Use:   "update -f <file>",
		Short: "Write an edited page body back to Confluence",
		Long: `Write a file previously downloaded with ` + "`cflio read`" + ` back to its page.

The page, the profile and the expected version all come from the file's
sidecar, so an update can never target the wrong page. If the page changed
on the server since it was read, the update is refused: re-read the page and
re-apply the edits.

--title renames the page in the same request. The body still travels, so a
rename on an untouched file resends the same bytes and only the title
changes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdatePage(cmd, g, file, message, title, outFormat)
		},
	}

	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"file holding the edited body, as downloaded by cflio read")
	cmd.Flags().StringVar(&message, "message", defaultVersionMessage,
		"version message recorded in the page's history")
	cmd.Flags().StringVar(&title, "title", "",
		"rename the page to this title; omit to keep the title the page has")
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}
	addFormatFlag(cmd, &outFormat)

	return cmd
}

// updateResult is what update prints on success.
type updateResult struct {
	PageID  string `json:"page_id"`
	Version int    `json:"version"`
	Title   string `json:"title"`
	PageURL string `json:"page_url"`
	Message string `json:"message"`
}

func runUpdatePage(cmd *cobra.Command, g *globalFlags, file, message, title string, outFormat format.Format) error {
	// An empty --title is rejected rather than ignored: the API requires a
	// title, and a blank one is far more likely to be a shell mistake than an
	// intent. Whether the flag was given at all is what tells that apart from
	// the flag being absent, which means the page's current title.
	if cmd.Flags().Changed("title") && title == "" {
		return errors.New("--title cannot be empty: pass the new title, " +
			"or leave the flag off to keep the page's current one")
	}

	meta, err := sidecar.Load(file)
	if err != nil {
		return err
	}
	if meta.Status != statusCurrent {
		return fmt.Errorf("page %s was read with status %q; only %q pages can be updated",
			meta.PageID, meta.Status, statusCurrent)
	}

	body, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}

	client, creds, err := resolveClient(g.profile, pageref.HostOf(meta.PageURL))
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	// Optimistic lock: the expected version is the one captured at read
	// time, never the server's current one. Fetching the current version
	// and writing current+1 would silently overwrite anything committed
	// between the read and the update.
	current, err := client.GetPage(ctx, meta.PageID, false)
	if err != nil {
		return err
	}
	if current.Version.Number != meta.Version {
		return fmt.Errorf("page %s changed since it was read (read at version %d, now at version %d): "+
			"run `cflio read` again and re-apply the edits",
			meta.PageID, meta.Version, current.Version.Number)
	}
	if current.Status != statusCurrent {
		return fmt.Errorf("page %s is now %q rather than %q; restore it in Confluence before updating",
			meta.PageID, current.Status, statusCurrent)
	}

	// An explicitly empty --message still gets the default: Confluence
	// shows the version message in page history, and a blank one there says
	// nothing about where the edit came from.
	if message == "" {
		message = defaultVersionMessage
	}

	// Without --title the sidecar's title travels, which is what leaves the
	// page's name alone: the API takes the title as a required field, so
	// there is no way to send an update that does not name one.
	if title == "" {
		title = meta.Title
	}

	req := confluence.NewUpdatePageRequest(
		meta.PageID, meta.Status, title, string(body), meta.Version+1, message)
	updated, err := client.UpdatePage(ctx, req)
	if err != nil {
		return err
	}

	// Record what the server confirmed, so the next edit-update cycle works
	// without a fresh read. The title and the web link move with a rename,
	// and the link is what the stored URL is built from, so all three are
	// taken from the response rather than from what was sent.
	meta.Version = updated.Version.Number
	meta.Title = updated.Title
	meta.PageURL = pageref.PageURL(creds.SiteURL, updated.Links.WebUI, meta.PageID)
	if err := sidecar.Write(file, meta); err != nil {
		return err
	}

	return writeUpdateResult(cmd, outFormat, updateResult{
		PageID:  meta.PageID,
		Version: meta.Version,
		Title:   meta.Title,
		PageURL: meta.PageURL,
		Message: message,
	})
}

func writeUpdateResult(cmd *cobra.Command, outFormat format.Format, result updateResult) error {
	if err := outFormat.Validate(); err != nil {
		return err
	}
	if outFormat == format.JSON {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Updated:  %s\nVersion:  %d\nMessage:  %s\nURL:      %s\n",
		result.Title, result.Version, result.Message, result.PageURL)
	return err
}
