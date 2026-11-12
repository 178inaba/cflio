//go:build unix

package cmd

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestAttachmentsUploadAcceptsOnlyRegularFiles covers the files whose open or
// read never ends: a FIFO blocks the open until a writer appears, before any
// deadline is running, and a device may stream without end, so /dev/null
// stands in for every device. A regression on the FIFO hangs rather than
// fails, which the test timeout reports.
func TestAttachmentsUploadAcceptsOnlyRegularFiles(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}

	for _, p := range []string{fifo, "/dev/null"} {
		t.Run(p, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)
			startAPI(t, neverCalled(t, "despite a file that is not a regular one"))

			_, err := runCLI(t, "confluence", "attachments", "upload", testPageURL, p)
			if err == nil {
				t.Fatal("attachments upload error = nil, want the file rejected")
			}
			if want := p + " is not a regular file"; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want %q", err, want)
			}
		})
	}
}
