package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

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
