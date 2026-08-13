// Package cmd wires up cflio's cobra command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const defaultTimeout = 90 * time.Second

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cflio",
		Short: "Confluence CLI for AI coding agents",
		Long: `cflio reads a Confluence page's storage-format body into a local file and
writes the edited file back, so an AI coding agent can edit pages with its
regular file-editing tools instead of regenerating the whole body as tokens.`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: validateFormatFlag,
	}

	cmd.PersistentFlags().String("profile", "",
		"profile to use, overriding URL-based auto-selection and CFLIO_PROFILE")
	cmd.PersistentFlags().Duration("timeout", defaultTimeout,
		"overall deadline for the invocation, as a Go duration (0 = no deadline)")

	cmd.AddCommand(
		newReadCmd(),
		newUpdateCmd(),
		newSearchCmd(),
		newChildrenCmd(),
		newCommentsCmd(),
		newAuthCmd(),
		newProfileCmd(),
	)
	return cmd
}

// Execute runs the root command. The returned error has already been printed;
// main only needs it to pick an exit code.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return nil
	}

	// Read off the persistent flag set rather than root.Flags(): the
	// subcommand that parsed --timeout wrote through to the same flag, but
	// cobra only merges the persistent flags into root.Flags() when there
	// were arguments to strip. The lookup itself cannot fail for a flag
	// registered in newRootCmd.
	timeout, _ := root.PersistentFlags().GetDuration("timeout")

	err = describeContextError(ctx, err, timeout)
	fmt.Fprintln(os.Stderr, "Error:", err)
	return err
}

// describeContextError replaces a bare context error with one that says what
// to do about it.
//
// The interrupt case is detected from the signal context rather than from
// the returned error: signal.NotifyContext cancels with a cause, which the
// transport surfaces instead of context.Canceled, so errors.Is would miss
// it. The deadline case is the other way round — it comes from a context
// derived per command, so only the error carries it, and net/http wraps it
// in a *url.Error that errors.Is sees through.
func describeContextError(signalCtx context.Context, err error, timeout time.Duration) error {
	switch {
	case signalCtx.Err() != nil:
		return fmt.Errorf("interrupted: %w", err)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("timed out after %s: raise the deadline with --timeout (0 disables it): %w",
			timeout, err)
	default:
		return err
	}
}

// commandContext returns a context bound to --timeout. It must be called at
// the point the first request is about to be issued, not at the top of RunE:
// `auth login` prompts for credentials first, and starting the clock before
// those prompts would fail a user who merely types slowly.
//
// A zero timeout means no deadline: context.WithTimeout(ctx, 0) would expire
// immediately, so that case returns the command's context unchanged.
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc, error) {
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return nil, nil, err
	}
	if timeout <= 0 {
		return cmd.Context(), func() {}, nil
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	return ctx, cancel, nil
}
