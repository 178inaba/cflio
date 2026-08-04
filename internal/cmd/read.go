package cmd

import (
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

	body := page.Body.Storage.Value
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

// writeBody writes the page body verbatim, with no trailing newline added:
// `update` sends the file back as-is, so a single extra byte would turn a
// no-op update into a content change on the page.
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

func writeReadResult(cmd *cobra.Command, result readResult) error {
	if formatFlag == "json" {
		return writeJSON(cmd, result)
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"Title:    %s\nVersion:  %d\nStatus:   %s\nURL:      %s\nBody:     %s (%d bytes)\nSidecar:  %s\n",
		result.Title, result.Version, result.Status, result.PageURL,
		result.BodyPath, result.Bytes, result.SidecarPath)
	return err
}
