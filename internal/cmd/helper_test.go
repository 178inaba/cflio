package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/config"
	"github.com/178inaba/atlio/internal/confluence"
)

const (
	testSite = "https://example.atlassian.net/wiki"

	// testPageWebUI is the relative link the API returns for the test page,
	// i.e. testPageURL without the site prefix.
	testPageWebUI = "/spaces/DEV/pages/123456/Some+Page"
)

// isolateConfig points the config package at a temporary directory and
// clears the environment overrides, so tests never touch the real config.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ATLIO_PROFILE", "")
	t.Setenv("ATLIO_TOKEN", "")
}

// seedProfile registers a default profile for the given site.
func seedProfile(t *testing.T, name, siteURL string) {
	t.Helper()

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	file.Profiles[name] = config.Profile{SiteURL: siteURL, Email: "a@example.com", Token: "tok"}
	if file.DefaultProfile == "" {
		file.DefaultProfile = name
	}
	if err := file.Save(); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}
}

// neverCalled is the handler for the cases a command must reject before it
// reaches the API. reason completes "the API was called ...".
func neverCalled(t *testing.T, reason string) http.HandlerFunc {
	t.Helper()

	return func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("the API was called %s: %s %s", reason, r.Method, r.URL.Path)
	}
}

// startAPI serves handler and redirects every client the commands build to
// it, regardless of the site URL the resolved profile carries.
func startAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	original := clientFactory
	clientFactory = func(_, email, token string) (*confluence.Client, error) {
		return confluence.New(srv.URL+"/wiki", email, token)
	}
	t.Cleanup(func() { clientFactory = original })

	return srv
}

// cliRun is what one invocation of the command tree produced. The two
// streams are kept apart so a test can assert which one a message reached.
type cliRun struct {
	stdout, stderr string
}

// runCLI builds a fresh command tree and runs args through it, returning
// what it wrote. Every case gets its own tree, so no flag value survives
// into the next test.
func runCLI(t *testing.T, args ...string) (cliRun, error) {
	t.Helper()
	return runCLIWithStdin(t, "", args...)
}

// runCLIWithStdin is runCLI for the commands that prompt.
func runCLIWithStdin(t *testing.T, stdin string, args ...string) (cliRun, error) {
	t.Helper()

	root, res := newRootCmd(&globalFlags{})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetIn(strings.NewReader(stdin))
	// Never nil: cobra falls back to os.Args[1:] for a nil argument list,
	// which under `go test` means the -test.* flags. A caller that passes
	// no arguments at all — to exercise bare `atlio` — would hit exactly
	// that, so the variadic's nil is normalised here.
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)

	err := root.ExecuteContext(t.Context())
	if err == nil {
		// The merge Execute does, so a test observes a mistyped subcommand
		// under a group command the way the process does: as the returned
		// error rather than out of band (see runResult.unknownCommand).
		err = res.unknownCommand
	}
	return cliRun{stdout: out.String(), stderr: errOut.String()}, err
}

// runLimitCmd runs one of the listing commands with an explicit --limit. The
// name is split on spaces, so a subcommand of a group ("confluence comments
// list") is named the way it is typed.
//
// --limit=N rather than --limit N: the range check is tested with negative
// values, which would otherwise be read as flags rather than as the value.
func runLimitCmd(t *testing.T, name, arg string, limit int, extra ...string) (cliRun, error) {
	t.Helper()

	args := append(strings.Fields(name), fmt.Sprintf("--limit=%d", limit))
	args = append(args, extra...)
	return runCLI(t, append(args, arg)...)
}
