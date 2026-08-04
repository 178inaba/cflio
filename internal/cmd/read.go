package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/178inaba/cflio/internal/pageref"
	"github.com/178inaba/cflio/internal/sidecar"
	"github.com/spf13/cobra"
)

var readOutputFlag string

var readCmd = &cobra.Command{
	Use:   "read <page-url|page-id>",
	Short: "Download a page's storage-format body to a file",
	Long: `Download a page's body, exactly as the API returns it in the storage
representation, to a local file plus a metadata sidecar.

The body is never printed: edit the downloaded file with your regular file
editing tools and write it back with ` + "`cflio update`" + `.`,
	Args: cobra.ExactArgs(1),
	RunE: runReadPage,
}

func init() {
	readCmd.Flags().StringVarP(&readOutputFlag, "output", "o", "",
		"file to write the body to (default ./<page-id>.xml)")
}

// readResult is what read prints: metadata and file paths, never the body.
type readResult struct {
	PageID      string `json:"page_id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	PageURL     string `json:"page_url"`
	BodyPath    string `json:"body_path"`
	SidecarPath string `json:"sidecar_path"`
	Bytes       int    `json:"bytes"`
}

func runReadPage(cmd *cobra.Command, args []string) error {
	ref, err := pageref.Parse(args[0])
	if err != nil {
		return err
	}

	client, creds, err := resolveClient(ref.Host)
	if err != nil {
		return err
	}

	ctx, cancel := commandContext(cmd)
	defer cancel()

	page, err := client.GetPage(ctx, ref.PageID, true)
	if err != nil {
		return err
	}

	bodyPath := readOutputFlag
	if bodyPath == "" {
		bodyPath = page.ID + ".xml"
	}

	// Written verbatim, with no trailing newline added: `update` sends the
	// file back as-is, so a single extra byte would turn a no-op update
	// into a content change.
	body := page.Body.Storage.Value
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", bodyPath, err)
	}

	meta := sidecar.Meta{
		PageID:  page.ID,
		Version: page.Version.Number,
		Title:   page.Title,
		Status:  page.Status,
		PageURL: pageref.PageURL(creds.SiteURL, page.Links.WebUI, page.ID),
	}
	if err := sidecar.Write(bodyPath, meta); err != nil {
		return err
	}

	return writeReadResult(cmd, readResult{
		PageID:      meta.PageID,
		Version:     meta.Version,
		Title:       meta.Title,
		Status:      meta.Status,
		PageURL:     meta.PageURL,
		BodyPath:    bodyPath,
		SidecarPath: sidecar.Path(bodyPath),
		Bytes:       len(body),
	})
}

func writeReadResult(cmd *cobra.Command, result readResult) error {
	out := cmd.OutOrStdout()

	if formatFlag == "json" {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(encoded))
		return err
	}

	_, err := fmt.Fprintf(out, "Title:    %s\nVersion:  %d\nStatus:   %s\nURL:      %s\nBody:     %s (%d bytes)\nSidecar:  %s\n",
		result.Title, result.Version, result.Status, result.PageURL,
		result.BodyPath, result.Bytes, result.SidecarPath)
	return err
}
