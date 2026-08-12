package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/178inaba/cflio/internal/format"
	"github.com/178inaba/cflio/internal/pageref"
	"github.com/178inaba/cflio/internal/sidecar"
	"github.com/spf13/cobra"
)

var (
	readOutputFlag   string
	readMarkdownFlag bool
)

var readCmd = &cobra.Command{
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
	RunE: runReadPage,
}

func init() {
	readCmd.Flags().StringVarP(&readOutputFlag, "output", "o", "",
		"file to write the body to (default ./<page-id>.xml, or .md with --markdown)")
	// No backticks in flag usage strings: cobra reads the first backtick
	// pair as the flag's argument placeholder.
	readCmd.Flags().BoolVar(&readMarkdownFlag, "markdown", false,
		"convert the body to Markdown for reading; the file gets no sidecar and cannot be updated")
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
		// The two modes default to different names, so reading a page both
		// ways leaves two files rather than one clobbering the other.
		bodyPath = page.ID + ".xml"
		if readMarkdownFlag {
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
	var converted format.Result
	if readMarkdownFlag {
		// Purely local: the request above still asked for the storage
		// representation, which is the only one that carries the macros and
		// the code bodies intact.
		converted = format.ToMarkdown(body, format.Options{})
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
	}

	// A converted body is not a checkout: it carries no version lock and can
	// never be written back, so it gets no sidecar. That the sidecar exists
	// if and only if the file beside it is updatable is what `update` relies
	// on to refuse this file.
	if !readMarkdownFlag {
		if err := sidecar.Write(bodyPath, meta); err != nil {
			return err
		}
		result.SidecarPath = sidecar.Path(bodyPath)
	}

	return writeReadResult(cmd, result)
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

func writeReadResult(cmd *cobra.Command, result readResult) error {
	if formatFlag == "json" {
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

	_, err := fmt.Fprint(cmd.OutOrStdout(), out.String())
	return err
}
