package cmd

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/178inaba/cflio/internal/config"
	"golang.org/x/term"
)

// runLogin drives auth login with the given prompt answers.
func runLogin(t *testing.T, answers string, handler http.HandlerFunc) (cflioRun, error) {
	t.Helper()

	startAPI(t, handler)
	return runCflioWithStdin(t, answers, "auth", "login")
}

func okCurrentUser(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{"accountId":"acc-1","displayName":"Ada Lovelace","email":"a@example.com"}`))
}

func TestAuthLoginRegistersAProfile(t *testing.T) {
	isolateConfig(t)

	var gotPath, gotAuth string
	run, err := runLogin(t, "https://example.atlassian.net\na@example.com\napi-token\n\n",
		func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
			okCurrentUser(w, r)
		})
	if err != nil {
		t.Fatalf("auth login error = %v", err)
	}

	if gotPath != "/wiki/rest/api/user/current" {
		t.Errorf("validation call = %q, want /wiki/rest/api/user/current", gotPath)
	}
	if gotAuth == "" {
		t.Error("validation call carried no Authorization header")
	}

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	want := config.Profile{SiteURL: testSite, Email: "a@example.com", Token: "api-token"}
	if got := file.Profiles["example"]; got != want {
		t.Errorf("stored profile = %+v, want %+v", got, want)
	}
	if file.DefaultProfile != "example" {
		t.Errorf("default profile = %q, want the first registered profile", file.DefaultProfile)
	}
	if !strings.Contains(run.stdout, "Ada Lovelace") {
		t.Errorf("output = %q, want it to confirm who the credentials belong to", run.stdout)
	}
}

func TestAuthLoginNormalizesThePastedSiteURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "bare host", input: "example.atlassian.net"},
		{name: "host with scheme", input: "https://example.atlassian.net"},
		{name: "wiki base", input: "https://example.atlassian.net/wiki"},
		{name: "a page url pasted from the browser", input: "https://example.atlassian.net/wiki/spaces/DEV/pages/1/T"},
		{name: "trailing slash", input: "https://example.atlassian.net/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)

			if _, err := runLogin(t, tt.input+"\na@example.com\napi-token\n\n", okCurrentUser); err != nil {
				t.Fatalf("auth login error = %v", err)
			}

			file, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load() error = %v", err)
			}
			if got := file.Profiles["example"].SiteURL; got != testSite {
				t.Errorf("stored site = %q, want %q", got, testSite)
			}
		})
	}
}

func TestAuthLoginRejectsInvalidSiteURLs(t *testing.T) {
	for _, input := range []string{"", "   ", "http://example.atlassian.net", "https:///wiki"} {
		t.Run(input, func(t *testing.T) {
			isolateConfig(t)

			_, err := runLogin(t, input+"\n", func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("the API was called despite an invalid site url")
			})
			if err == nil {
				t.Fatalf("auth login error = nil for site %q, want an error", input)
			}
			assertNoConfigWritten(t)
		})
	}
}

func TestAuthLoginSavesNothingWhenCredentialsAreRejected(t *testing.T) {
	isolateConfig(t)

	run, err := runLogin(t, "https://example.atlassian.net\na@example.com\nbad-token\n\n",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Basic auth with password is not allowed"}`))
		})
	if err == nil {
		t.Fatal("auth login error = nil, want an error for rejected credentials")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want it to surface the status", err)
	}
	if strings.Contains(run.stdout, "Registered") {
		t.Errorf("output = %q, want no success message", run.stdout)
	}
	assertNoConfigWritten(t)
}

func TestAuthLoginRequiresEmailAndToken(t *testing.T) {
	tests := []struct {
		name    string
		answers string
	}{
		{name: "empty email", answers: "https://example.atlassian.net\n\n"},
		{name: "empty token", answers: "https://example.atlassian.net\na@example.com\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)

			_, err := runLogin(t, tt.answers, func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("the API was called despite missing credentials")
			})
			if err == nil {
				t.Fatal("auth login error = nil, want an error")
			}
			assertNoConfigWritten(t)
		})
	}
}

func TestAuthLoginAcceptsACustomProfileName(t *testing.T) {
	isolateConfig(t)

	if _, err := runLogin(t, "https://example.atlassian.net\na@example.com\napi-token\nwork\n",
		okCurrentUser); err != nil {
		t.Fatalf("auth login error = %v", err)
	}

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if _, ok := file.Profiles["work"]; !ok {
		t.Errorf("profiles = %v, want one named work", file.Profiles)
	}
	if _, ok := file.Profiles["example"]; ok {
		t.Error("the proposed name was stored even though a different one was typed")
	}
}

func TestAuthLoginRotatesTheTokenAfterConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		answer    string
		wantToken string
	}{
		{name: "confirmed", answer: "y", wantToken: "new-token"},
		{name: "declined", answer: "n", wantToken: "tok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			seedProfile(t, "example", testSite)

			if _, err := runLogin(t, "https://example.atlassian.net\na@example.com\nnew-token\n"+tt.answer+"\n",
				okCurrentUser); err != nil {
				t.Fatalf("auth login error = %v", err)
			}

			file, err := config.Load()
			if err != nil {
				t.Fatalf("config.Load() error = %v", err)
			}
			if got := file.Profiles["example"].Token; got != tt.wantToken {
				t.Errorf("token = %q, want %q", got, tt.wantToken)
			}
			if len(file.Profiles) != 1 {
				t.Errorf("profiles = %v, want the re-login to reuse the existing name", file.Profiles)
			}
		})
	}
}

func TestAuthLoginConfirmsBeforeReusingANameFromAnotherSite(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", "https://other.atlassian.net/wiki")

	if _, err := runLogin(t, "https://example.atlassian.net\na@example.com\napi-token\nexample\nn\n",
		okCurrentUser); err != nil {
		t.Fatalf("auth login error = %v", err)
	}

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if got := file.Profiles["example"].SiteURL; got != "https://other.atlassian.net/wiki" {
		t.Errorf("site = %q, want the declined overwrite to leave the other site in place", got)
	}
}

func TestAuthLoginKeepsTheExistingDefaultProfile(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	if _, err := runLogin(t, "https://example.atlassian.net\na@example.com\napi-token\n\n",
		okCurrentUser); err != nil {
		t.Fatalf("auth login error = %v", err)
	}

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if file.DefaultProfile != "other" {
		t.Errorf("default profile = %q, want the pre-existing default kept", file.DefaultProfile)
	}
}

func TestTerminalFile(t *testing.T) {
	// A scripted reader is not an *os.File, so the masked prompt has to
	// fall back to a plain line read — that fallback is what every other
	// auth login test in this file depends on.
	if _, ok := terminalFile(strings.NewReader("x")); ok {
		t.Error("terminalFile(strings.Reader) reported a terminal")
	}

	// A regular file is an *os.File but not a terminal.
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, ok := terminalFile(f); ok {
		t.Error("terminalFile(regular file) reported a terminal")
	}
}

func TestPromptSecretFallsBackToAPlainReadOffTerminal(t *testing.T) {
	var out bytes.Buffer
	raw := strings.NewReader("api-token\n")
	in := bufio.NewReader(raw)

	got, err := promptSecret(&out, in, raw, "API token: ")
	if err != nil {
		t.Fatalf("promptSecret() error = %v", err)
	}
	if got != "api-token" {
		t.Errorf("promptSecret() = %q, want %q", got, "api-token")
	}
	if !strings.Contains(out.String(), "API token: ") {
		t.Errorf("output = %q, want the prompt written", out.String())
	}
}

// assertNoConfigWritten fails if a config file exists, which is how "on
// failure nothing is saved" is enforced.
func assertNoConfigWritten(t *testing.T) {
	t.Helper()

	path, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file %s exists, want nothing written on failure", path)
	}
}

// guardTestFd stands in for the terminal's descriptor. The guard only hands
// it to the restore it is given, which these tests replace, so it never has
// to name anything open.
const guardTestFd = 42

// guardEffectTimeout bounds how long a test waits for the guard to act on a
// signal it was sent. The guard's own work is a handful of calls, so this
// only has to outlast a loaded machine's scheduling.
const guardEffectTimeout = 10 * time.Second

func selfProcess(t *testing.T) *os.Process {
	t.Helper()

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("os.FindProcess() error = %v", err)
	}
	return self
}

// guardRecorder records what the guard did, in order. The guard acts on its
// own goroutine, so every field is read behind the mutex — except reraised,
// which is the channel a test waits on, and whose receive also orders the
// writes that came before it.
type guardRecorder struct {
	mu           sync.Mutex
	events       []string
	resetSignals []os.Signal
	restoreFd    int
	restoreState *term.State

	reraised chan syscall.Signal
}

func (r *guardRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *guardRecorder) snapshot() (events []string, resetSignals []os.Signal, restoreFd int, restoreState *term.State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events), slices.Clone(r.resetSignals), r.restoreFd, r.restoreState
}

// newRecordingGuard builds a guard whose three process-level effects are
// recorded instead of performed. Stubbing them is what makes the ordering
// observable at all: term.Restore needs a real terminal, and the real
// re-raise ends the process.
func newRecordingGuard(out io.Writer, state *term.State) (*terminalGuard, *guardRecorder) {
	rec := &guardRecorder{reraised: make(chan syscall.Signal, 1)}

	g := newTerminalGuard(out, guardTestFd, state)
	g.reset = func(signals ...os.Signal) {
		rec.mu.Lock()
		rec.resetSignals = signals
		rec.mu.Unlock()
		rec.record("reset")
	}
	g.restore = func(fd int, state *term.State) error {
		rec.mu.Lock()
		rec.restoreFd, rec.restoreState = fd, state
		rec.mu.Unlock()
		rec.record("restore")
		return nil
	}
	g.reraise = func(sig syscall.Signal) {
		rec.record("reraise")
		rec.reraised <- sig
	}
	return g, rec
}

// TestTerminalGuardReRaisesAfterRestoring covers the guard's whole reason to
// exist: a signal arriving while the masked read has the terminal modified
// has to put the terminal back before the process dies of that signal.
// Re-raising first would leave the user's shell with echo off.
func TestTerminalGuardReRaisesAfterRestoring(t *testing.T) {
	// Pinning the list rather than only iterating it: iterating alone would
	// still pass if the guard stopped registering SIGTERM.
	signals := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if got := interruptSignals(); !slices.Equal(got, signals) {
		t.Fatalf("interruptSignals() = %v, want %v", got, signals)
	}

	self := selfProcess(t)

	for _, sig := range signals {
		t.Run(sig.String(), func(t *testing.T) {
			var out bytes.Buffer
			state := &term.State{}
			g, rec := newRecordingGuard(&out, state)

			disarm := g.arm()
			t.Cleanup(disarm)

			if err := self.Signal(sig); err != nil {
				t.Fatalf("sending %v to the test process: %v", sig, err)
			}

			var reraised syscall.Signal
			select {
			case reraised = <-rec.reraised:
			case <-time.After(guardEffectTimeout):
				t.Fatal("the guard did not re-raise the signal")
			}

			if reraised != sig {
				t.Errorf("re-raised %v, want %v", reraised, sig)
			}
			events, resetSignals, restoreFd, restoreState := rec.snapshot()
			if want := []string{"reset", "restore", "reraise"}; !slices.Equal(events, want) {
				t.Errorf("guard did %v, want %v", events, want)
			}
			// Resetting only the signal that arrived would leave the other
			// one delivered to a channel nobody reads any more.
			if !slices.Equal(resetSignals, interruptSignals()) {
				t.Errorf("reset %v, want %v", resetSignals, interruptSignals())
			}
			if restoreFd != guardTestFd || restoreState != state {
				t.Errorf("restored (%d, %p), want (%d, %p)",
					restoreFd, restoreState, guardTestFd, state)
			}
			// Nothing echoes the interrupt with ECHO cleared, so the guard
			// owes the prompt its closing newline — and nothing else.
			if got := out.String(); got != "\n" {
				t.Errorf("guard wrote %q, want a single newline", got)
			}
		})
	}
}

// TestTerminalGuardDisarmStopsDelivery covers the other end of the guard's
// life: once the masked read is over the terminal is unmodified again, and a
// signal has to reach the process default rather than the guard.
func TestTerminalGuardDisarmStopsDelivery(t *testing.T) {
	self := selfProcess(t)

	var out bytes.Buffer
	g, rec := newRecordingGuard(&out, &term.State{})
	disarm := g.arm()

	// Taking delivery over before disarming: with no handler left, the
	// signal below would terminate the test binary rather than prove
	// anything.
	delivered := make(chan os.Signal, 1)
	signal.Notify(delivered, os.Interrupt)
	t.Cleanup(func() { signal.Stop(delivered) })

	disarm()

	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatalf("sending %v to the test process: %v", os.Interrupt, err)
	}
	select {
	case <-delivered:
	case <-time.After(guardEffectTimeout):
		t.Fatal("the signal was never delivered")
	}

	// A goroutine that has returned leaves nothing to observe directly, so
	// the proof is that none of the guard's effects run.
	select {
	case sig := <-rec.reraised:
		t.Errorf("the guard re-raised %v after being disarmed", sig)
	case <-time.After(100 * time.Millisecond):
	}
	if events, _, _, _ := rec.snapshot(); len(events) > 0 {
		t.Errorf("guard did %v after being disarmed, want nothing", events)
	}
	if out.Len() > 0 {
		t.Errorf("guard wrote %q after being disarmed, want nothing", out.String())
	}
}
