package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
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

// walkCommands calls fn for every command in the tree below the root, keyed by
// its path with the root's own name dropped ("attachments list").
//
// The whole tree rather than root.Commands(): a group command carries no
// --format of its own, so a walk one level deep would pin `attachments` as a
// command without the flag and never look at the two subcommands that have it.
// The prefix is taken from Root() rather than from the argument, which is the
// current parent once the recursion is under way.
func walkCommands(parent *cobra.Command, fn func(name string, cmd *cobra.Command)) {
	prefix := parent.Root().Name() + " "
	for _, cmd := range parent.Commands() {
		fn(strings.TrimPrefix(cmd.CommandPath(), prefix), cmd)
		walkCommands(cmd, fn)
	}
}

// TestFormatFlagRegistration pins down which commands take --format: it is
// registered per command so `auth` and `profile`, which have no output to
// format, reject it rather than silently ignoring it. Group commands are not
// runnable and so take none either; their subcommands are listed on their own.
func TestFormatFlagRegistration(t *testing.T) {
	want := map[string]bool{
		"read":                 true,
		"update":               true,
		"search":               true,
		"children":             true,
		"comments":             true,
		"attachments":          false,
		"attachments list":     true,
		"attachments download": true,
		"plantuml":             false,
		"plantuml list":        true,
		"plantuml get":         true,
		"plantuml set":         true,
		"plantuml add":         true,
		"auth":                 false,
		"auth login":           false,
		"profile":              false,
		"profile list":         false,
		"profile use":          false,
	}

	root, _ := newRootCmd(&globalFlags{})
	got := make(map[string]bool, len(want))
	walkCommands(root, func(name string, cmd *cobra.Command) {
		got[name] = cmd.Flags().Lookup("format") != nil
	})

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
	walkCommands(root, func(name string, cmd *cobra.Command) {
		flag := cmd.Flags().Lookup("format")
		if flag == nil {
			return
		}
		if flag.Value.Type() != "string" {
			t.Errorf("%s: --format placeholder = %q, want %q", name, flag.Value.Type(), "string")
		}
		if want := string(format.Markdown); flag.DefValue != want {
			t.Errorf("%s: --format default = %q, want %q", name, flag.DefValue, want)
		}
	})
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
			name: "attachments list",
			args: []string{"attachments", "list", "--format", "bogus", "123456"},
		},
		{
			// --pattern is left off on purpose, for the same reason -f is
			// left off `update` below.
			name: "attachments download, before cobra reports a missing required flag",
			args: []string{"attachments", "download", "--format", "bogus", "123456"},
		},
		{
			// -f is left off on purpose: cobra parses the flags before it
			// validates the required ones, so the bad format is what gets
			// reported. Supplying -f here would drop that guarantee.
			name: "update, before cobra reports a missing required flag",
			args: []string{"update", "--format", "bogus"},
		},
		{
			name: "plantuml list, before cobra reports a missing required flag",
			args: []string{"plantuml", "list", "--format", "bogus"},
		},
		{
			name: "plantuml get, before cobra reports a missing required flag",
			args: []string{"plantuml", "get", "--format", "bogus"},
		},
		{
			name: "plantuml set, before cobra reports a missing required flag",
			args: []string{"plantuml", "set", "--format", "bogus"},
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

// TestDescribeFailure pins the exit code alongside the message, since the two
// come out of the same call. The codes are written as literals rather than as
// timeoutExitCode: they are the contract a caller reads, so a test taking them
// from the constant would agree with any value the constant happened to hold.
func TestDescribeFailure(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantCode    int
		wantContain string
	}{
		{
			name:        "deadline points at --timeout",
			err:         fmt.Errorf("get page: %w", context.DeadlineExceeded),
			wantCode:    124,
			wantContain: "--timeout",
		},
		{
			name:        "other errors pass through unchanged",
			err:         errors.New("page not found"),
			wantCode:    1,
			wantContain: "page not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, got := describeFailure(tt.err, defaultTimeout)
			if gotCode != tt.wantCode {
				t.Errorf("describeFailure() code = %d, want %d", gotCode, tt.wantCode)
			}
			if !strings.Contains(got.Error(), tt.wantContain) {
				t.Errorf("describeFailure() = %q, want it to contain %q", got, tt.wantContain)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("describeFailure() dropped the wrapped error %v", tt.err)
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
			name:       "attachments",
			args:       []string{"attachments", "bogus"},
			wantStderr: "Error: unknown command \"bogus\" for \"cflio attachments\"\n",
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
	for _, name := range []string{"", "attachments", "auth", "profile", "completion"} {
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

// TestHelpFlagWinsOverAnUnknownSubcommand pins the half of the guard the
// cases above cannot reach: they all leave the flag off, so the leftover
// argument check decides them either way. Asking for help is a request for
// help even when the arguments carry a typo alongside it.
func TestHelpFlagWinsOverAnUnknownSubcommand(t *testing.T) {
	run, err := runCflio(t, "auth", "bogus", "--help")
	if err != nil {
		t.Fatalf("cflio auth bogus --help error = %v, want nil", err)
	}
	if run.unknownCommand {
		t.Error("cflio auth bogus --help recorded a failure, want the help request answered")
	}
	if run.stderr != "" {
		t.Errorf("cflio auth bogus --help stderr = %q, want nothing", run.stderr)
	}
	if !strings.Contains(run.stdout, "Usage:") {
		t.Errorf("cflio auth bogus --help stdout = %q, want the help text", run.stdout)
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

// TestHelpRejectsAnUnresolvableTopic covers what cobra's own help command
// does not: its Run cannot fail, so a topic that does not resolve exits 0 —
// and `cflio help auth bogus` reports nothing at all, since Find resolves
// `auth` and discards the leftover argument. The assertions are on the
// returned error rather than on an exit code, which does not exist at this
// level, and the message is matched whole so a usage listing sneaking back
// in would fail here.
func TestHelpRejectsAnUnresolvableTopic(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "topic the root cannot resolve",
			args:    []string{"help", "bogus"},
			wantErr: `unknown command "bogus" for "cflio"`,
		},
		{
			// bogus is too far from any command to be suggested, so a table
			// built only on it would never exercise the candidates.
			name:    "topic close enough to suggest",
			args:    []string{"help", "raed"},
			wantErr: "unknown command \"raed\" for \"cflio\"\n\nDid you mean this?\n\tread\n",
		},
		{
			name:    "topic a group command cannot resolve",
			args:    []string{"help", "auth", "bogus"},
			wantErr: `unknown command "bogus" for "cflio auth"`,
		},
		{
			name:    "topic under a group close enough to suggest",
			args:    []string{"help", "auth", "loign"},
			wantErr: "unknown command \"loign\" for \"cflio auth\"\n\nDid you mean this?\n\tlogin\n",
		},
		{
			name:    "several leftovers report the first",
			args:    []string{"help", "bogus1", "bogus2"},
			wantErr: `unknown command "bogus1" for "cflio"`,
		},
		{
			name:    "several leftovers under a group report the first",
			args:    []string{"help", "auth", "bogus", "extra"},
			wantErr: `unknown command "bogus" for "cflio auth"`,
		},
		{
			// `help` is not a subcommand of a group command, so nothing but
			// unknownCommandCandidates' own case offers anything for it.
			name:    "help under a group points at the flag",
			args:    []string{"help", "auth", "help"},
			wantErr: "unknown command \"help\" for \"cflio auth\"\n\nDid you mean this?\n\t--help\n",
		},
		{
			// The third shape: a command that resolves and has no
			// subcommands at all, with an argument left over anyway. Neither
			// Find's error nor the target having subcommands separates it
			// from `cflio help read`, so a condition built on either would
			// still render the help here and exit 0, as this did before.
			name:    "leftover after a command that takes arguments",
			args:    []string{"help", "read", "123456"},
			wantErr: `unknown command "123456" for "cflio read"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, err := runCflio(t, tt.args...)
			if err == nil {
				t.Fatalf("%v error = nil, want an error", tt.args)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("%v error = %q, want %q", tt.args, err, tt.wantErr)
			}
			if run.unknownCommand {
				t.Errorf("%v recorded a failure, want the returned error to carry it instead", tt.args)
			}
			if run.stdout != "" || run.stderr != "" {
				t.Errorf("%v wrote stdout = %q, stderr = %q, want nothing from the tree itself",
					tt.args, run.stdout, run.stderr)
			}
		})
	}
}

// TestHelpPrintsHelpForAResolvingTopic pins the invocations the report above
// must leave alone. Each is compared against the flag spelling of the same
// request, so a replacement help command that rendered something else would
// fail here rather than pass on a substring match.
func TestHelpPrintsHelpForAResolvingTopic(t *testing.T) {
	for _, topic := range [][]string{nil, {"auth"}, {"auth", "login"}, {"read"}} {
		t.Run("cflio help "+strings.Join(topic, " "), func(t *testing.T) {
			run, err := runCflio(t, append([]string{"help"}, topic...)...)
			if err != nil {
				t.Fatalf("help %v error = %v, want nil", topic, err)
			}
			if run.unknownCommand {
				t.Errorf("help %v recorded a failure, want none", topic)
			}
			if run.stderr != "" {
				t.Errorf("help %v stderr = %q, want nothing", topic, run.stderr)
			}

			withFlag, err := runCflio(t, append(topic, "--help")...)
			if err != nil {
				t.Fatalf("%v --help error = %v, want nil", topic, err)
			}
			if run.stdout != withFlag.stdout {
				t.Errorf("help %v stdout = %q, want the same help as %v --help", topic, run.stdout, topic)
			}
		})
	}
}

// TestHelpTopicCompletion pins the completion the replacement help command
// has to keep working. `help` itself is the case worth having: cobra keeps
// it in the list by comparing against its unexported helpCommand field,
// which IsAvailableCommand excludes, so a replacement that dropped that
// comparison would still list everything else.
//
// stderr is not asserted here, unlike the tests above: cobra ends every
// __complete run with a "Completion ended with directive" line on it.
func TestHelpTopicCompletion(t *testing.T) {
	want := []string{
		"attachments", "auth", "children", "comments", "completion",
		"help", "plantuml", "profile", "read", "search", "update",
	}

	run, err := runCflio(t, "__complete", "help", "")
	if err != nil {
		t.Fatalf("cflio __complete help \"\" error = %v, want nil", err)
	}

	var got []string
	for _, line := range strings.Split(strings.TrimSuffix(run.stdout, "\n"), "\n") {
		// The listing ends with cobra's ":<directive>" line.
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, _, _ := strings.Cut(line, "\t")
		got = append(got, name)
	}

	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("cflio __complete help \"\" listed %v, want %v", got, want)
	}
}
