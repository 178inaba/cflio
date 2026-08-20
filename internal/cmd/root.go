// Package cmd wires up cflio's cobra command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultTimeout = 90 * time.Second

// timeoutExitCode is what a run ends with when its --timeout deadline expired.
// 124 is the code GNU timeout reports for the same thing, so an agent can tell
// "raise --timeout and run it again" from every other failure without reading
// stderr. Sibling repos use it for the same purpose.
const timeoutExitCode = 124

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

// runResult carries what the command tree records outside cobra's return
// value. It is per-tree rather than package state so tests can build
// independent trees in one process.
type runResult struct {
	// unknownCommand reports that the help function rejected a mistyped
	// subcommand of a group command. cobra resolves that path to
	// flag.ErrHelp, which ExecuteC turns into a nil error, so nothing else
	// would tell Execute the run failed.
	unknownCommand bool
}

// newRootCmd builds the command tree. globalFlags travels in — pflag fills
// it in as it parses — and runResult travels back out, filled in as the tree
// runs.
func newRootCmd(g *globalFlags) (*cobra.Command, *runResult) {
	res := &runResult{}
	cmd := &cobra.Command{
		Use:   "cflio",
		Short: "Confluence CLI for AI coding agents",
		Long: `cflio reads a Confluence page's storage-format body into a local file and
writes the edited file back, so an AI coding agent can edit pages with its
regular file-editing tools instead of regenerating the whole body as tokens.`,
		SilenceUsage:  true,
		SilenceErrors: true,
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
		newAttachmentsCmd(g),
		newAuthCmd(g),
		newProfileCmd(),
	)

	// cobra only reports a mistyped subcommand for the root: under a group
	// command, execute returns flag.ErrHelp on the !Runnable() check before
	// ValidateArgs ever runs, and ExecuteC answers that by printing help and
	// returning nil. Overriding the help function is the one hook on that
	// path, and since HelpFunc walks to the parent, registering it here
	// covers every group — including the completion command cobra adds to
	// the tree inside ExecuteC, which no field set in a constructor of ours
	// could reach.
	//
	// The default rendering has to be captured before SetHelpFunc: after it,
	// Help() resolves to the override, so the fall-through branch would
	// recurse.
	defaultHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if help, _ := c.Flags().GetBool("help"); !help && !c.Runnable() && len(c.Flags().Args()) > 0 {
			reportUnknownCommand(c, c.Flags().Args()[0])
			res.unknownCommand = true
			return
		}
		defaultHelp(c, args)
	})

	return cmd, res
}

// reportUnknownCommand writes, for a group command, the message cobra writes
// itself when the root command is given an unknown subcommand.
//
// The rendering is assembled the way legacyArgs assembles it and printed the
// way Execute prints it, so a typo under a group reads exactly like a typo at
// the top level. It stops there rather than following gh's nestedSuggestFunc
// into a usage listing: the root sets SilenceUsage precisely because usage on
// every failure is noise for an agent consumer.
//
// PrintErrf rather than a bare Fprintf: it writes to ErrOrStderr — which the
// tests redirect, unlike os.Stderr — and returns nothing, which is the only
// sane contract for a help function that cannot propagate a write error.
func reportUnknownCommand(cmd *cobra.Command, arg string) {
	var suggestions strings.Builder
	if candidates := unknownCommandCandidates(cmd, arg); len(candidates) > 0 {
		suggestions.WriteString("\n\nDid you mean this?\n")
		for _, candidate := range candidates {
			fmt.Fprintf(&suggestions, "\t%v\n", candidate)
		}
	}
	cmd.PrintErrf("Error: unknown command %q for %q%s\n", arg, cmd.CommandPath(), suggestions.String())
}

// unknownCommandCandidates reproduces the candidate list cobra builds in its
// unexported findSuggestions — minus the DisableSuggestions escape hatch,
// which nothing here sets — plus the one case cobra cannot cover: `help` is
// not a subcommand of a group command, and SuggestionsFor only ever looks at
// registered subcommands, so nothing would be offered for the argument whose
// intent is least ambiguous.
func unknownCommandCandidates(cmd *cobra.Command, arg string) []string {
	if arg == "help" {
		return []string{"--help"}
	}
	// cobra applies this default in findSuggestions, which is unexported;
	// the exported SuggestionsFor does not. Without it every Levenshtein
	// candidate is dropped and only prefix matches survive, so `loign`
	// would stop suggesting `login`.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	return cmd.SuggestionsFor(arg)
}

// Execute runs the root command and returns the process exit code: 0 when it
// succeeded, 124 when the --timeout deadline expired, and 1 for every other
// failure. Those numbers are the contract a caller reads, so they are spelled
// out here rather than named. The error itself is printed rather than
// returned, since main has nothing left to do with it.
//
// The code comes back from a function that returns normally because os.Exit
// skips deferred functions, so main can do nothing but pass it straight on.
//
// Nothing here catches a signal: an interrupt ends the process through the
// Go default, so no error unwinds the stack and this never runs for one — an
// interrupted run has no exit code at all, only a termination status. The one
// place that does catch is the masked token read, where the terminal has to be
// put back first (see terminalGuard in auth.go).
//
// The command context is left unset: cobra defaults a nil one to
// context.Background(), which is all commandContext needs to derive from.
func Execute() int {
	g := &globalFlags{}
	root, res := newRootCmd(g)
	err := root.Execute()
	if err == nil {
		// The help function has already written the message for a mistyped
		// subcommand (see runResult.unknownCommand), so this only has to
		// make the run fail.
		if res.unknownCommand {
			return 1
		}
		return 0
	}

	code, err := describeFailure(err, g.timeout)
	fmt.Fprintln(os.Stderr, "Error:", err)
	return code
}

// describeFailure maps a failure to the exit code it ends the process with and
// the error to print for it, replacing a bare context error with one that says
// what to do about it. Success never reaches here — Execute answers that
// itself — so every path returns a non-zero code.
//
// Deciding both here is the point: errors.Is(err, DeadlineExceeded) is the
// only thing separating timeoutExitCode from 1, and splitting the message and
// the code across two functions would let a third failure class be added to
// one and missed in the other, with nothing failing loudly when they disagree.
// That covers every failure that carries an error, which is all of them but
// one: a mistyped subcommand under a group command is reported by the help
// function and never reaches here (see Execute).
//
// Only the deadline gets rewritten, and it is detected from the error: it
// comes from a context derived per command, so the error is what carries it,
// and net/http wraps it in a *url.Error that errors.Is sees through.
func describeFailure(err error, timeout time.Duration) (int, error) {
	if errors.Is(err, context.DeadlineExceeded) {
		return timeoutExitCode, fmt.Errorf(
			"timed out after %s: raise the deadline with --timeout (0 disables it): %w", timeout, err)
	}
	return 1, err
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
