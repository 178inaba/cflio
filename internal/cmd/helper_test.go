package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/config"
	"github.com/178inaba/cflio/internal/confluence"
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
	t.Setenv("CFLIO_PROFILE", "")
	t.Setenv("CFLIO_TOKEN", "")
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

// cflioRun is what one invocation of the command tree produced. The two
// streams are kept apart so a test can assert which one a message reached.
type cflioRun struct {
	stdout, stderr string
}

// runCflio builds a fresh command tree and runs args through it, returning
// what it wrote. Every case gets its own tree, so no flag value survives
// into the next test.
func runCflio(t *testing.T, args ...string) (cflioRun, error) {
	t.Helper()
	return runCflioWithStdin(t, "", args...)
}

// runCflioWithStdin is runCflio for the commands that prompt.
func runCflioWithStdin(t *testing.T, stdin string, args ...string) (cflioRun, error) {
	t.Helper()

	root := newRootCmd(&globalFlags{})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetIn(strings.NewReader(stdin))
	// Never nil: cobra falls back to os.Args[1:] for a nil argument list,
	// which under `go test` means the -test.* flags. A caller that passes
	// no arguments at all — to exercise bare `cflio` — would hit exactly
	// that, so the variadic's nil is normalised here.
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)

	err := root.ExecuteContext(t.Context())
	return cflioRun{stdout: out.String(), stderr: errOut.String()}, err
}

// runLimitCmd runs one of the listing commands with an explicit --limit.
//
// --limit=N rather than --limit N: the range check is tested with negative
// values, which would otherwise be read as flags rather than as the value.
func runLimitCmd(t *testing.T, name, arg string, limit int, extra ...string) (cflioRun, error) {
	t.Helper()

	args := append([]string{name, fmt.Sprintf("--limit=%d", limit)}, extra...)
	return runCflio(t, append(args, arg)...)
}
