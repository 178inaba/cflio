package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/atlassian"
	"github.com/178inaba/atlio/internal/config"
	"github.com/178inaba/atlio/internal/confluence"
	"github.com/178inaba/atlio/internal/jira"
)

const (
	// testSite is the site URL a profile stores.
	testSite = "https://example.atlassian.net"

	// testWikiBase is the Confluence base URL the client derives from
	// testSite, which every URL a Confluence command prints starts with. It
	// is spelled out rather than built from testSite so that an output URL
	// missing /wiki fails the expectations built from it.
	testWikiBase = "https://example.atlassian.net/wiki"

	// testPageWebUI is the relative link the API returns for the test page,
	// i.e. testPageURL without the base prefix.
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

// writeBodyFile writes a page or comment body to a fresh file and returns its
// path.
func writeBodyFile(t *testing.T, body string) string {
	t.Helper()
	return writeTempFile(t, "body.xml", body)
}

// writeTempFile writes content to name under a fresh temporary directory and
// returns its path. name may carry subdirectories; its base name is kept as
// given because commands derive things from it — a default output filename, an
// attachment's name.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// neverCalled is the handler for the cases a command must reject before it
// reaches the API. reason completes "the API was called ...".
func neverCalled(t *testing.T, reason string) http.HandlerFunc {
	t.Helper()

	return func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("the API was called %s: %s %s", reason, r.Method, r.URL.Path)
	}
}

// startAPI serves handler and sends every request the commands make to it,
// whichever site the resolved profile names. The clients keep that site as
// their own, so the URLs a command prints are the ones it would print against
// the real site.
func startAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	httpClient := &http.Client{Transport: redirectTransport{target: target}}

	original := confluenceFactory
	confluenceFactory = func(site, email, token string) (*confluence.Client, error) {
		return confluence.New(site, email, token, atlassian.WithHTTPClient(httpClient))
	}
	t.Cleanup(func() { confluenceFactory = original })

	originalJira := jiraFactory
	jiraFactory = func(site, email, token string) (*jira.Client, error) {
		return jira.New(site, email, token, atlassian.WithHTTPClient(httpClient))
	}
	t.Cleanup(func() { jiraFactory = originalJira })

	return srv
}

// redirectTransport sends every request to target's scheme and host, keeping
// its path and query.
type redirectTransport struct {
	target *url.URL
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// A RoundTripper must not modify the request it was handed.
	out := req.Clone(req.Context())
	out.URL.Scheme = rt.target.Scheme
	out.URL.Host = rt.target.Host
	out.Host = ""
	return http.DefaultTransport.RoundTrip(out)
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
