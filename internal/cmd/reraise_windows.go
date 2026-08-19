//go:build windows

package cmd

import (
	"os"
	"syscall"
)

// reraise ends the process with the status a shell reports for a death by
// this signal, which is the closest Windows gets: it delivers os.Interrupt
// on Ctrl-C but has no signal to re-raise, and syscall.Kill does not exist
// there. The same call is the Unix fallback when the re-raise fails.
func reraise(sig syscall.Signal) {
	os.Exit(128 + int(sig))
}
