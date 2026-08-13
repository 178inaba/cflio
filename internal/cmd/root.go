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

// globalFlags holds the root's persistent flags. Every subcommand that reads
// one takes this pointer, so the flag names stay type-checked instead of
// being looked up by string at each site.
//
// pflag fills the fields in as it parses, which is after the tree is built:
// a constructor that copied a field would capture the zero value, so RunE
// must read them when it runs.
type globalFlags struct {
	profile string
	timeout time.Duration
}

func newRootCmd(g *globalFlags) *cobra.Command {
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

	cmd.PersistentFlags().StringVar(&g.profile, "profile", "",
		"profile to use, overriding URL-based auto-selection and CFLIO_PROFILE")
	cmd.PersistentFlags().DurationVar(&g.timeout, "timeout", defaultTimeout,
		"overall deadline for the invocation, as a Go duration (0 = no deadline)")

	// `profile` takes no globalFlags: it neither issues a request nor
	// resolves a profile to talk to.
	cmd.AddCommand(
		newReadCmd(g),
		newUpdateCmd(g),
		newSearchCmd(g),
		newChildrenCmd(g),
		newCommentsCmd(g),
		newAuthCmd(g),
		newProfileCmd(),
	)
	return cmd
}

// Execute runs the root command. The returned error has already been printed;
// main only needs it to pick an exit code.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	g := &globalFlags{}
	err := newRootCmd(g).ExecuteContext(ctx)
	if err == nil {
		return nil
	}

	err = describeContextError(ctx, err, g.timeout)
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
func commandContext(cmd *cobra.Command, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return cmd.Context(), func() {}
	}
	return context.WithTimeout(cmd.Context(), timeout)
}
