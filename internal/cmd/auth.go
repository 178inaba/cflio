package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/178inaba/cflio/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const apiTokenURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

func newAuthCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
	}
	cmd.AddCommand(newAuthLoginCmd(g))
	return cmd
}

func newAuthLoginCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Register a Confluence Cloud site interactively",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthLogin(cmd, g)
		},
	}
}

func runAuthLogin(cmd *cobra.Command, g *globalFlags) error {
	in := bufio.NewReader(cmd.InOrStdin())
	out := cmd.OutOrStdout()

	siteURL, err := prompt(out, in, "Confluence site URL (e.g. https://example.atlassian.net): ")
	if err != nil {
		return err
	}
	site, err := normalizeSiteURL(siteURL)
	if err != nil {
		return err
	}

	email, err := prompt(out, in, "Atlassian account email: ")
	if err != nil {
		return err
	}
	if email == "" {
		return fmt.Errorf("account email is required")
	}

	token, err := promptSecret(out, in, cmd.InOrStdin(), "API token (create one at "+apiTokenURL+"): ")
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("api token is required")
	}

	client, err := clientFactory(site, email, token)
	if err != nil {
		return err
	}

	// The deadline starts here rather than at the top of RunE: the prompts
	// above are human-paced, and a slow typist should not hit --timeout.
	ctx, cancel := commandContext(cmd, g.timeout)
	defer cancel()

	user, err := client.CurrentUser(ctx)
	if err != nil {
		return fmt.Errorf("verify credentials against %s: %w", site, err)
	}

	file, err := config.Load()
	if err != nil {
		return err
	}

	name, aborted, err := chooseProfileName(out, in, file, site)
	if err != nil || aborted {
		return err
	}

	file.Profiles[name] = config.Profile{SiteURL: site, Email: email, Token: token}
	setDefault := file.DefaultProfile == ""
	if setDefault {
		file.DefaultProfile = name
	}
	if err := file.Save(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "Registered profile %q for %s as %s.\n",
		name, site, displayName(user.DisplayName, email)); err != nil {
		return err
	}
	if setDefault {
		if _, err := fmt.Fprintln(out, "Set as the default profile."); err != nil {
			return err
		}
	}
	return nil
}

// chooseProfileName settles on the profile name to write to, confirming
// first whenever that would overwrite stored credentials. aborted reports
// that the user declined.
func chooseProfileName(out io.Writer, in *bufio.Reader, file *config.File, site string) (name string, aborted bool, err error) {
	for _, existing := range config.SortedProfileNames(file) {
		if file.Profiles[existing].SiteURL != site {
			continue
		}
		// Re-login for a registered site is the token rotation path, so it
		// keeps the existing name rather than proposing a new one.
		aborted, err := confirmOrAbort(out, in, fmt.Sprintf(
			"Profile %q is already registered for %s. Overwrite the stored token? [y/N]: ", existing, site))
		if err != nil || aborted {
			return "", true, err
		}
		return existing, false, nil
	}

	proposed := proposedProfileName(site)
	if _, err := fmt.Fprintf(out, "Register %s as profile %q? "+
		"Press Enter to accept, or type a different name: ", site, proposed); err != nil {
		return "", false, err
	}
	typed, err := readLine(in)
	if err != nil {
		return "", false, fmt.Errorf("read profile name: %w", err)
	}
	name = proposed
	if typed != "" {
		name = typed
	}

	if other, ok := file.Profiles[name]; ok {
		aborted, err := confirmOrAbort(out, in, fmt.Sprintf(
			"Profile %q is already registered for a different site (%s). Overwrite it? [y/N]: ", name, other.SiteURL))
		if err != nil || aborted {
			return "", true, err
		}
	}
	return name, false, nil
}

// normalizeSiteURL reduces anything pasted from a browser to the site's
// Confluence base URL. Only the scheme and host are kept and /wiki is
// appended: cflio targets Confluence Cloud, where that path is fixed, so
// deriving it beats asking the user to get it right.
func normalizeSiteURL(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("site url is required")
	}
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}

	u, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("parse site url %q: %w", input, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("site url %q has no host; pass something like https://example.atlassian.net", input)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("site url %q must use https", input)
	}
	return "https://" + u.Host + "/wiki", nil
}

// proposedProfileName derives a profile name from a site URL, e.g.
// "https://example.atlassian.net/wiki" -> "example".
func proposedProfileName(site string) string {
	u, err := url.Parse(site)
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(u.Hostname(), ".")
	return name
}

func displayName(name, email string) string {
	if name != "" {
		return name
	}
	return email
}

func prompt(out io.Writer, in *bufio.Reader, message string) (string, error) {
	if _, err := fmt.Fprint(out, message); err != nil {
		return "", err
	}
	line, err := readLine(in)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return line, nil
}

// interruptSignals lists the signals the terminal guard catches. It is a
// function so tests can assert the registered set without a second copy of
// the list to keep in step.
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// terminalGuard puts the terminal back and then re-raises, so an interrupt
// during the masked token read ends the process by the signal the user sent
// rather than through an error report.
//
// Nothing else in cflio needs the signal caught: dying immediately is
// already the wanted behaviour everywhere else, and the modified terminal
// is the only thing that would outlive the process badly.
//
// reset, restore and reraise are fields rather than direct calls because
// none of them can run under test — restore needs a real terminal, and
// reraise ends the process. newTerminalGuard fills them with the real ones.
type terminalGuard struct {
	out     io.Writer
	fd      int
	state   *term.State
	reset   func(...os.Signal)
	restore func(int, *term.State) error
	reraise func(syscall.Signal) // does not return
}

func newTerminalGuard(out io.Writer, fd int, state *term.State) *terminalGuard {
	return &terminalGuard{
		out:     out,
		fd:      fd,
		state:   state,
		reset:   signal.Reset,
		restore: term.Restore,
		reraise: reraise,
	}
}

// arm starts catching the interrupt signals and returns the call that stops
// it again. The guard handles at most one signal: the process is meant to
// die of it, so there is no second one to serve.
//
// Disarming is best-effort against a signal that has already been taken,
// and losing that race is harmless — the user pressed Ctrl-C, and the
// process dying is the wanted outcome either way.
func (g *terminalGuard) arm() (disarm func()) {
	// Buffered, as signal.Notify requires: the runtime drops a signal
	// rather than blocking on the send.
	received := make(chan os.Signal, 1)
	signal.Notify(received, interruptSignals()...)

	disarmed := make(chan struct{})
	go func() {
		select {
		case <-disarmed:
		case sig := <-received:
			g.handle(sig)
		}
	}()

	return func() {
		signal.Stop(received)
		close(disarmed)
	}
}

// handle runs the clean-up the guard exists for. The order is load-bearing:
// resetting the handlers first is what lets a second signal end the process
// at once instead of queueing behind the restore, which the Command Line
// Interface Guidelines ask for. The cost is that a second signal landing in
// the microseconds before the restore leaves echo off; `stty sane` recovers
// it, and re-arming to close that window would reinstate the very problem
// the reset is here to avoid.
//
// Every signal interruptSignals returns is reset, not just the one that
// arrived: signal.Reset is variadic and resets only what it is given, so a
// SIGTERM following a SIGINT would otherwise land in a channel nobody reads.
func (g *terminalGuard) handle(sig os.Signal) {
	g.reset(interruptSignals()...)

	// Both errors are dropped rather than reported: an interrupted run
	// prints no failure report, and there is no branch left to take with
	// the process about to die of the signal.
	_ = g.restore(g.fd, g.state)
	// Nothing echoes the interrupt with ECHO cleared, so this newline is
	// what ends the prompt's line.
	_, _ = fmt.Fprintln(g.out)

	// A signal that is not a syscall.Signal carries no number to build a
	// status from. interruptSignals yields only ones that are, so this
	// cannot happen in practice. The check sits after the restore, so even
	// the impossible path leaves the terminal usable.
	s, ok := sig.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}
	// The guard runs on its own goroutine rather than on the path that
	// returns from Execute, so this ends the process instead of a code.
	g.reraise(s)
}

// promptSecret reads a value that must not be left in the terminal's
// scrollback. On a real terminal it echoes nothing; anywhere else — a pipe,
// or a test's scripted stdin — it falls back to a plain line read, since
// there is no terminal to hide the input from.
func promptSecret(out io.Writer, in *bufio.Reader, raw io.Reader, message string) (string, error) {
	f, isTerminal := terminalFile(raw)
	if !isTerminal {
		return prompt(out, in, message)
	}

	if _, err := fmt.Fprint(out, message); err != nil {
		return "", err
	}

	// ReadPassword clears ECHO and restores the terminal from its own
	// deferred call, which only runs once its read returns — never, when a
	// signal ends the process mid-read. Capturing the state here is what
	// lets the guard put echo back instead of leaving the user's shell
	// silent.
	fd := int(f.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}

	// term.GetState only reads, so arming after it still leaves the guarded
	// window a strict superset of the modified one: a signal arriving
	// before ReadPassword clears ECHO restores a state that was never
	// changed, which is a no-op. There is no window in which the terminal
	// is modified and unguarded.
	//
	// Deferred rather than called after the read, so the error path out of
	// ReadPassword disarms too.
	disarm := newTerminalGuard(out, fd, state).arm()
	defer disarm()

	secret, err := term.ReadPassword(fd)
	// The user's Enter is swallowed along with the echo, so the following
	// output would otherwise continue on the prompt's line.
	if _, printErr := fmt.Fprintln(out); printErr != nil && err == nil {
		return "", printErr
	}
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return strings.TrimSpace(string(secret)), nil
}

// terminalFile reports the underlying *os.File of an input stream and
// whether it is a real terminal. Anything that is not an *os.File — the
// readers tests script stdin with — reports (nil, false).
func terminalFile(in io.Reader) (*os.File, bool) {
	f, ok := in.(*os.File)
	if !ok {
		return nil, false
	}
	return f, term.IsTerminal(int(f.Fd()))
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func confirm(out io.Writer, in *bufio.Reader, message string) (bool, error) {
	if _, err := fmt.Fprint(out, message); err != nil {
		return false, err
	}
	line, err := readLine(in)
	if err != nil {
		return false, err
	}
	line = strings.ToLower(line)
	return line == "y" || line == "yes", nil
}

// confirmOrAbort wraps confirm with the "declined -> print Aborted. and
// report aborted=true" flow shared by both overwrite prompts.
func confirmOrAbort(out io.Writer, in *bufio.Reader, message string) (aborted bool, err error) {
	ok, err := confirm(out, in, message)
	if err != nil {
		return false, err
	}
	if ok {
		return false, nil
	}
	if _, err := fmt.Fprintln(out, "Aborted."); err != nil {
		return false, err
	}
	return true, nil
}
