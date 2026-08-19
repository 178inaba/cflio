//go:build !windows

package cmd

import (
	"os"
	"syscall"
	"time"
)

// deliveryGrace is how long reraise waits for the signal it sent to end the
// process. Measured delivery is tens of microseconds, so this is orders of
// magnitude more than the wait ever needs, and it is only ever spent on a
// process that is about to die anyway.
const deliveryGrace = 100 * time.Millisecond

// reraise ends the process by the signal it is given, so a parent sees
// WIFSIGNALED rather than a normal exit imitating one — which is what a
// shell uses to decide that Ctrl-C should also break the loop cflio was
// running in. It does not return.
//
// The wait is required rather than decorative. raise(3) is synchronous for
// the calling thread, but Go's syscall.Kill is not: the runtime may deliver
// the signal on another thread while this one carries on into the fallback.
//
// Waiting rather than blocking forever covers the one case where the
// re-raise cannot kill: a SIGINT inherited as SIG_IGN, which os/signal
// catches for the guard but signal.Reset then restores to ignored, so the
// Kill succeeds and does nothing. Blocking there would hang the process at
// a prompt the user has already abandoned; exiting keeps the interrupt
// final, at the cost of a normal exit imitating the signal death.
func reraise(sig syscall.Signal) {
	if err := syscall.Kill(syscall.Getpid(), sig); err == nil {
		time.Sleep(deliveryGrace)
	}
	os.Exit(128 + int(sig))
}
