package cmd

import (
	"fmt"
	"os"

	"github.com/178inaba/cflio/internal/confluence"
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

func newUpdateCmd() *cobra.Command {
	var (
		file      string
		message   string
		outFormat string
	)

	cmd := &cobra.Command{
		Use:   "update -f <file>",
		Short: "Write an edited page body back to Confluence",
		Long: `Write a file previously downloaded with ` + "`cflio read`" + ` back to its page.

The page, the profile and the expected version all come from the file's
sidecar, so an update can never target the wrong page. If the page changed
on the server since it was read, the update is refused: re-read the page and
re-apply the edits.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdatePage(cmd, file, message, outFormat)
		},
	}

	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	cmd.Flags().StringVarP(&file, "file", "f", "",
		"file holding the edited body, as downloaded by cflio read")
	cmd.Flags().StringVar(&message, "message", defaultVersionMessage,
		"version message recorded in the page's history")
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

func runUpdatePage(cmd *cobra.Command, file, message, outFormat string) error {
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

	client, _, err := resolveClient(cmd, pageref.HostOf(meta.PageURL))
	if err != nil {
		return err
	}

	ctx, cancel, err := commandContext(cmd)
	if err != nil {
		return err
	}
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

	req := confluence.NewUpdatePageRequest(
		meta.PageID, meta.Status, meta.Title, string(body), meta.Version+1, message)
	updated, err := client.UpdatePage(ctx, req)
	if err != nil {
		return err
	}

	// Record the version the server actually assigned, so the next
	// edit-update cycle works without a fresh read.
	meta.Version = updated.Version.Number
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

func writeUpdateResult(cmd *cobra.Command, outFormat string, result updateResult) error {
	if outFormat == "json" {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Updated:  %s\nVersion:  %d\nMessage:  %s\nURL:      %s\n",
		result.Title, result.Version, result.Message, result.PageURL)
	return err
}
