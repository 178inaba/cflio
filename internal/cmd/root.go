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

var (
	profileFlag string
	formatFlag  string
	timeoutFlag time.Duration
)

var rootCmd = &cobra.Command{
	Use:   "cflio",
	Short: "Confluence CLI for AI coding agents",
	Long: `cflio reads a Confluence page's storage-format body into a local file and
writes the edited file back, so an AI coding agent can edit pages with its
regular file-editing tools instead of regenerating the whole body as tokens.`,
	SilenceUsage:      true,
	SilenceErrors:     true,
	PersistentPreRunE: validateFormatFlag,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&profileFlag, "profile", "",
		"profile to use, overriding URL-based auto-selection and CFLIO_PROFILE")
	rootCmd.PersistentFlags().StringVar(&formatFlag, "format", "md",
		`output format: "md" or "json"`)
	rootCmd.PersistentFlags().DurationVar(&timeoutFlag, "timeout", defaultTimeout,
		"overall deadline for the invocation, as a Go duration (0 = no deadline)")

	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(profileCmd)
}

func validateFormatFlag(cmd *cobra.Command, args []string) error {
	if formatFlag != "md" && formatFlag != "json" {
		return fmt.Errorf(`invalid --format %q: must be "md" or "json"`, formatFlag)
	}
	return nil
}

// Execute runs the root command. The returned error has already been printed;
// main only needs it to pick an exit code.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return nil
	}

	err = describeContextError(err)
	fmt.Fprintln(os.Stderr, "Error:", err)
	return err
}

// describeContextError replaces a bare context error with one that says what
// to do about it. net/http wraps the context error in a *url.Error, so the
// deadline and the interrupt are only recognizable through errors.Is.
func describeContextError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("timed out after %s: raise the deadline with --timeout (0 disables it): %w",
			timeoutFlag, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("interrupted: %w", err)
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
func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	if timeoutFlag <= 0 {
		return cmd.Context(), func() {}
	}
	return context.WithTimeout(cmd.Context(), timeoutFlag)
}
