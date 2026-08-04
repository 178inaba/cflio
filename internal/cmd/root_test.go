package cmd

import (
	"context"
	"errors"
	"fmt"
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
			setTimeoutFlag(t, tt.timeout)

			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			ctx, cancel := commandContext(cmd)
			defer cancel()

			if _, ok := ctx.Deadline(); ok != tt.wantDeadline {
				t.Errorf("ctx.Deadline() ok = %v, want %v", ok, tt.wantDeadline)
			}
		})
	}
}

func TestDescribeContextError(t *testing.T) {
	setTimeoutFlag(t, 90*time.Second)

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
			got := describeContextError(tt.signalCtx, tt.err)
			if !strings.Contains(got.Error(), tt.wantContain) {
				t.Errorf("describeContextError() = %q, want it to contain %q", got, tt.wantContain)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("describeContextError() dropped the wrapped error %v", tt.err)
			}
		})
	}
}

// setTimeoutFlag overrides the package-level --timeout binding for one test
// and restores it afterwards.
func setTimeoutFlag(t *testing.T, d time.Duration) {
	t.Helper()
	original := timeoutFlag
	timeoutFlag = d
	t.Cleanup(func() { timeoutFlag = original })
}
