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

	root := newRootCmd(&globalFlags{})
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
	root := newRootCmd(&globalFlags{})
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
