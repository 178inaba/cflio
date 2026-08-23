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

// unknownCommandHint is the Annotations key a command carries to add a line
// to the error a mistyped subcommand under it is reported with. It exists for
// the intent cobra's suggestions cannot reach: they compare the argument
// against subcommand names, so an argument that is not a misspelling of one —
// a page id passed to a command that used to take one — gets nothing offered
// for it.
const unknownCommandHint = "cflio_unknown_command_hint"

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
	// unknownCommand is the failure the help function built for a mistyped
	// subcommand of a group command, or nil if it never fired. cobra
	// resolves that path to flag.ErrHelp, which ExecuteC turns into a nil
	// error, so nothing else would tell Execute the run failed. Execute
	// prints and maps it like any failure that came back the usual way.
	unknownCommand error
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

	// `plantuml` and `profile` take no globalFlags: neither issues a request
	// nor resolves a profile to talk to. `plantuml` works entirely on the
	// file `read` already downloaded.
	cmd.AddCommand(
		newReadCmd(g),
		newUpdateCmd(g),
		newSearchCmd(g),
		newChildrenCmd(g),
		newCommentsCmd(g),
		newAttachmentsCmd(g),
		newPlantUMLCmd(),
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
		if help, _ := c.Flags().GetBool("help"); help || c.Runnable() || c.Flags().NArg() == 0 {
			defaultHelp(c, args)
			return
		}
		res.unknownCommand = unknownCommandError(c, c.Flags().Arg(0))
	})

	// The help command has to be replaced rather than overridden, for the
	// same reason: its message comes from a Run of cobra's own, which the
	// help function above never sees (see newHelpCmd). AddCommand is left to
	// InitDefaultHelpCmd, which ExecuteC calls — adding it here would put
	// `help` in the tree a constructor hands back, where nothing else cobra
	// installs appears.
	cmd.SetHelpCommand(newHelpCmd(cmd))

	return cmd, res
}

// newHelpCmd replaces the help command cobra installs itself. cobra's runs
// through Run, which cannot fail, so a topic that does not resolve exits 0 —
// and `cflio help auth bogus` reports nothing at all, because Find resolves
// `auth` and the leftover `bogus` goes to the blank identifier. RunE lets the
// same failure come back as an error, which the root's SilenceErrors hands to
// Execute to print and end the run non-zero for.
//
// Everything else is cobra's own, kept as it is so `cflio help <TAB>` still
// completes subcommand names. GroupID is the one field dropped: cflio has no
// command groups, so cobra's would be empty here anyway. The substitution is
// in ValidArgsFunction, where cobra keeps `help` in the candidate list by
// comparing against the unexported helpCommand field that IsAvailableCommand
// excludes; a closure over helpCmd does the same from outside the package,
// which is what the declaration ahead of the literal is for.
func newHelpCmd(root *cobra.Command) *cobra.Command {
	var helpCmd *cobra.Command
	helpCmd = &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long: `Help provides help for any command in the application.
Simply type ` + root.DisplayName() + ` help [path to command] for full details.`,
		RunE: func(c *cobra.Command, args []string) error {
			// Find's error is discarded because a leftover argument already
			// subsumes it: the error only ever comes from legacyArgs, which
			// reports nothing unless an argument was left unresolved. Under
			// a group command Find returns no error at all — the quirk this
			// command exists to answer — so the leftovers are what both
			// forms have in common.
			target, rest, _ := c.Root().Find(args)
			if len(rest) > 0 {
				return unknownCommandError(target, rest[0])
			}

			// Flow the context down to be used in help text. cobra assigns
			// it only when the target has none; assigning it unconditionally
			// is the same thing here, since the two targets that already
			// carry one — this command, for `cflio help help`, and the root,
			// for `cflio help` — both carry this very context.
			target.SetContext(c.Context())
			target.InitDefaultHelpFlag()    // make possible 'help' flag to be shown
			target.InitDefaultVersionFlag() // make possible 'version' flag to be shown
			return target.Help()
		},
		ValidArgsFunction: func(
			c *cobra.Command, args []string, toComplete string,
		) ([]cobra.Completion, cobra.ShellCompDirective) {
			var completions []cobra.Completion
			cmd, _, e := c.Root().Find(args)
			if e != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			if cmd == nil {
				// Root help command.
				cmd = c.Root()
			}
			for _, subCmd := range cmd.Commands() {
				if subCmd.IsAvailableCommand() || subCmd == helpCmd {
					if strings.HasPrefix(subCmd.Name(), toComplete) {
						completions = append(completions, cobra.CompletionWithDesc(subCmd.Name(), subCmd.Short))
					}
				}
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
	}

	return helpCmd
}

// unknownCommandError builds the error cobra builds itself when the root
// command is given an unknown subcommand, for the two paths that have to
// report that failure on their own: a mistyped subcommand of a group
// command, which cobra reports as a request for help, and a help topic that
// does not resolve, whose two forms cobra reaches by different routes.
//
// The rendering is assembled the way legacyArgs assembles it, so a typo
// reads the same wherever it was caught — which is also why the help command
// re-derives it rather than passing Find's error through: one of its two
// forms has no error to pass. It stops there rather than following gh's
// nestedSuggestFunc into a usage listing: the root sets SilenceUsage
// precisely because usage on every failure is noise for an agent consumer.
func unknownCommandError(cmd *cobra.Command, arg string) error {
	var suggestions strings.Builder
	if candidates := unknownCommandCandidates(cmd, arg); len(candidates) > 0 {
		suggestions.WriteString("\n\nDid you mean this?\n")
		for _, candidate := range candidates {
			fmt.Fprintf(&suggestions, "\t%v\n", candidate)
		}
	}
	// After the candidates, so the machine-generated guess stays next to the
	// argument it was derived from and the hint reads as the closing word.
	// The candidate block already ends in a newline, so only one more is
	// needed to leave the same blank line either way.
	if hint := cmd.Annotations[unknownCommandHint]; hint != "" {
		if suggestions.Len() == 0 {
			suggestions.WriteString("\n")
		}
		fmt.Fprintf(&suggestions, "\n%s", hint)
	}
	return fmt.Errorf("unknown command %q for %q%s", arg, cmd.CommandPath(), suggestions.String())
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
		// cobra reports a mistyped subcommand under a group command by
		// calling the help function, which has no error to return, so a nil
		// here is the one thing that can still be hiding a failure (see
		// runResult.unknownCommand). cobra's own error wins where both are
		// set, since it is the one that stopped the run.
		err = res.unknownCommand
	}
	if err != nil {
		code, err := describeFailure(err, g.timeout)
		fmt.Fprintln(os.Stderr, "Error:", err)
		return code
	}
	return 0
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
