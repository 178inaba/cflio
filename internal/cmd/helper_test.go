package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/178inaba/cflio/internal/config"
	"github.com/178inaba/cflio/internal/confluence"
	"github.com/spf13/cobra"
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

// newTestCommand returns a command whose output is captured, along with the
// buffer holding it.
func newTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}

// setFlags overrides the package-level flag bindings for one test.
func setFlags(t *testing.T, profile, format string) {
	t.Helper()

	originalProfile, originalFormat := profileFlag, formatFlag
	profileFlag, formatFlag = profile, format
	t.Cleanup(func() { profileFlag, formatFlag = originalProfile, originalFormat })
}
