package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/178inaba/cflio/internal/format"
	"github.com/spf13/cobra"
)

func TestCommandContextTimeout(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "zero means no deadline", timeout: 0, wantDeadline: false},
		{name: "negative means no deadline", timeout: -time.Second, wantDeadline: false},
		{name: "positive sets a deadline", timeout: time.Minute, wantDeadline: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())

			ctx, cancel := commandContext(cmd, tt.timeout)
			defer cancel()

			if _, ok := ctx.Deadline(); ok != tt.wantDeadline {
				t.Errorf("ctx.Deadline() ok = %v, want %v", ok, tt.wantDeadline)
			}
		})
	}
}

// TestFormatFlagRegistration pins down which commands take --format: it is
// registered per command so `auth` and `profile`, which have no output to
// format, reject it rather than silently ignoring it.
func TestFormatFlagRegistration(t *testing.T) {
	want := map[string]bool{
		"read":     true,
		"update":   true,
		"search":   true,
		"children": true,
		"comments": true,
		"auth":     false,
		"profile":  false,
	}

	root, _ := newRootCmd(&globalFlags{})
	got := make(map[string]bool, len(want))
	for _, cmd := range root.Commands() {
		got[cmd.Name()] = cmd.Flags().Lookup("format") != nil
	}

	if !maps.Equal(got, want) {
		t.Errorf("commands with --format = %v, want %v", got, want)
	}
}

// TestFormatFlagHelpLine pins the two halves of the `--format string` help
// line, which agents read as part of the documented contract. Both are taken
// from the value the flag was registered with, so a registration that ran
// before the default was assigned would change the line without failing
// anything else.
func TestFormatFlagHelpLine(t *testing.T) {
	root, _ := newRootCmd(&globalFlags{})
	for _, cmd := range root.Commands() {
		flag := cmd.Flags().Lookup("format")
		if flag == nil {
			continue
		}
		if flag.Value.Type() != "string" {
			t.Errorf("%s: --format placeholder = %q, want %q", cmd.Name(), flag.Value.Type(), "string")
		}
		if want := string(format.Markdown); flag.DefValue != want {
			t.Errorf("%s: --format default = %q, want %q", cmd.Name(), flag.DefValue, want)
		}
	}
}

func TestFormatFlagRejectsAnUnknownValue(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "read", args: []string{"read", "--format", "bogus", "123456"}},
		{name: "search", args: []string{"search", "--format", "bogus", "text ~ x"}},
		{name: "children", args: []string{"children", "--format", "bogus", "123456"}},
		{name: "comments", args: []string{"comments", "--format", "bogus", "123456"}},
		{
			// -f is left off on purpose: cobra parses the flags before it
			// validates the required ones, so the bad format is what gets
			// reported. Supplying -f here would drop that guarantee.
			name: "update, before cobra reports a missing required flag",
			args: []string{"update", "--format", "bogus"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)

			_, err := runCflio(t, tt.args...)
			if err == nil {
				t.Fatalf("%v error = nil, want an error", tt.args)
			}
			if want := `invalid --format "bogus"`; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err, want)
			}
		})
	}
}

// TestWritersRejectAFormatThatBypassedSet covers the guard every writer keeps:
// a Format converted straight from a string never went through Set, so
// without it an unknown value would quietly render as Markdown.
func TestWritersRejectAFormatThatBypassedSet(t *testing.T) {
	const bogus = format.Format("xml")

	tests := []struct {
		name  string
		write func(*cobra.Command) error
	}{
		{
			name:  "writeList",
			write: func(cmd *cobra.Command) error { return writeList(cmd, bogus, "results", []searchItem{}, "") },
		},
		{
			name:  "writeComments",
			write: func(cmd *cobra.Command) error { return writeComments(cmd, bogus, nil) },
		},
		{
			name:  "writeReadResult",
			write: func(cmd *cobra.Command) error { return writeReadResult(cmd, bogus, readResult{}) },
		},
		{
			name:  "writeUpdateResult",
			write: func(cmd *cobra.Command) error { return writeUpdateResult(cmd, bogus, updateResult{}) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			// Discarded rather than captured: nothing should be written, and
			// a buffer would only make a failure print the whole rendering.
			cmd.SetOut(io.Discard)

			err := tt.write(cmd)
			if err == nil {
				t.Fatalf("%s(%q) error = nil, want an error", tt.name, string(bogus))
			}
			if want := string(bogus); !strings.Contains(err.Error(), want) {
				t.Errorf("%s error = %q, want it to name %q", tt.name, err, want)
			}
		})
	}
}

func TestDescribeContextError(t *testing.T) {
	live := context.Background()
	interrupted, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name        string
		signalCtx   context.Context
		err         error
		wantContain string
	}{
		{
			name:        "deadline points at --timeout",
			signalCtx:   live,
			err:         fmt.Errorf("get page: %w", context.DeadlineExceeded),
			wantContain: "--timeout",
		},
		{
			// signal.NotifyContext cancels with a cause, so the transport
			// reports that rather than context.Canceled; the signal
			// context being done is the reliable signal.
			name:        "a cancelled signal context reads as an interrupt",
			signalCtx:   interrupted,
			err:         errors.New("get page: interrupt signal received"),
			wantContain: "interrupted",
		},
		{
			name:        "other errors pass through unchanged",
			signalCtx:   live,
			err:         errors.New("page not found"),
			wantContain: "page not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeContextError(tt.signalCtx, tt.err, defaultTimeout)
			if !strings.Contains(got.Error(), tt.wantContain) {
				t.Errorf("describeContextError() = %q, want it to contain %q", got, tt.wantContain)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("describeContextError() dropped the wrapped error %v", tt.err)
			}
		})
	}
}

// TestGroupCommandRejectsAnUnknownSubcommand covers the case cobra does not
// report: under a group command it resolves a mistyped subcommand to
// flag.ErrHelp, which ExecuteC prints help for and returns nil from. The
// assertions are on the recorded failure and the message rather than on an
// exit code, since the tree produces neither.
func TestGroupCommandRejectsAnUnknownSubcommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "auth",
			args:       []string{"auth", "bogus"},
			wantStderr: "Error: unknown command \"bogus\" for \"cflio auth\"\n",
		},
		{
			// bogus is too far from any subcommand to be suggested, so a
			// table built only on it would never exercise the candidates.
			name:       "auth, close enough to suggest",
			args:       []string{"auth", "loign"},
			wantStderr: "Error: unknown command \"loign\" for \"cflio auth\"\n\nDid you mean this?\n\tlogin\n\n",
		},
		{
			name:       "profile",
			args:       []string{"profile", "bogus"},
			wantStderr: "Error: unknown command \"bogus\" for \"cflio profile\"\n",
		},
		{
			// completion is cobra's own command, added inside ExecuteC, so
			// nothing cflio's constructors set could reach it.
			name:       "completion",
			args:       []string{"completion", "bogus"},
			wantStderr: "Error: unknown command \"bogus\" for \"cflio completion\"\n",
		},
		{
			name:       "auth help",
			args:       []string{"auth", "help"},
			wantStderr: "Error: unknown command \"help\" for \"cflio auth\"\n\nDid you mean this?\n\t--help\n\n",
		},
		{
			name:       "profile help",
			args:       []string{"profile", "help"},
			wantStderr: "Error: unknown command \"help\" for \"cflio profile\"\n\nDid you mean this?\n\t--help\n\n",
		},
		{
			name:       "completion help",
			args:       []string{"completion", "help"},
			wantStderr: "Error: unknown command \"help\" for \"cflio completion\"\n\nDid you mean this?\n\t--help\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, err := runCflio(t, tt.args...)
			if err != nil {
				t.Fatalf("%v error = %v, want nil: cobra reports this path as success", tt.args, err)
			}
			if !run.unknownCommand {
				t.Errorf("%v recorded no failure, want one", tt.args)
			}
			if run.stdout != "" {
				t.Errorf("%v stdout = %q, want nothing where the command's output belongs", tt.args, run.stdout)
			}
			if run.stderr != tt.wantStderr {
				t.Errorf("%v stderr = %q, want %q", tt.args, run.stderr, tt.wantStderr)
			}
		})
	}
}

// TestGroupCommandWithoutArgumentsPrintsHelp pins the invocations the report
// above must leave alone: a group command with nothing after it is a request
// for help, not a typo.
func TestGroupCommandWithoutArgumentsPrintsHelp(t *testing.T) {
	for _, name := range []string{"", "auth", "profile", "completion"} {
		t.Run("cflio "+name, func(t *testing.T) {
			var args []string
			if name != "" {
				args = append(args, name)
			}

			run, err := runCflio(t, args...)
			if err != nil {
				t.Fatalf("%v error = %v, want nil", args, err)
			}
			if run.unknownCommand {
				t.Errorf("%v recorded a failure, want none", args)
			}
			if run.stderr != "" {
				t.Errorf("%v stderr = %q, want nothing", args, run.stderr)
			}
			if !strings.Contains(run.stdout, "Usage:") {
				t.Errorf("%v stdout = %q, want the help text", args, run.stdout)
			}

			// Both spellings render the same help, so pinning them together
			// keeps a change to either from passing unnoticed.
			withFlag, err := runCflio(t, append(args, "--help")...)
			if err != nil {
				t.Fatalf("%v --help error = %v, want nil", args, err)
			}
			if withFlag.stdout != run.stdout {
				t.Errorf("%v --help stdout = %q, want the same help as %v", args, withFlag.stdout, args)
			}
			if withFlag.unknownCommand || withFlag.stderr != "" {
				t.Errorf("%v --help recorded failure = %v and wrote stderr = %q, want neither",
					args, withFlag.unknownCommand, withFlag.stderr)
			}
		})
	}
}

// TestRootRejectsAnUnknownCommand pins the path cobra does handle: at the
// root, Find fails before the help function is ever reached, so the error
// comes back for Execute to print rather than being recorded.
func TestRootRejectsAnUnknownCommand(t *testing.T) {
	run, err := runCflio(t, "bogus")
	if err == nil {
		t.Fatal("cflio bogus error = nil, want an error")
	}
	if want := `unknown command "bogus" for "cflio"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if run.unknownCommand {
		t.Error("cflio bogus recorded a failure, want the returned error to carry it instead")
	}
	if run.stdout != "" || run.stderr != "" {
		t.Errorf("cflio bogus wrote stdout = %q, stderr = %q, want nothing from the tree itself",
			run.stdout, run.stderr)
	}
}
