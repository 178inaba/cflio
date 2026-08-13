package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/178inaba/cflio/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const apiTokenURL = "https://id.atlassian.com/manage-profile/security/api-tokens"

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication",
	}
	cmd.AddCommand(newAuthLoginCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Register a Confluence Cloud site interactively",
		Args:  cobra.NoArgs,
		RunE:  runAuthLogin,
	}
}

func runAuthLogin(cmd *cobra.Command, args []string) error {
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
	ctx, cancel, err := commandContext(cmd)
	if err != nil {
		return err
	}
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
	secret, err := term.ReadPassword(int(f.Fd()))
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
