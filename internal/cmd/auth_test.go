package cmd

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/config"
)

// runLogin drives auth login with the given prompt answers.
func runLogin(t *testing.T, answers string, handler http.HandlerFunc) (cflioRun, error) {
	t.Helper()

	startAPI(t, handler)
	return runCflioWithStdin(t, answers, "auth", "login")
}

func okCurrentUser(w http.ResponseWriter, r *http.Request) {
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

			_, err := runLogin(t, input+"\n", func(w http.ResponseWriter, r *http.Request) {
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
		func(w http.ResponseWriter, r *http.Request) {
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

			_, err := runLogin(t, tt.answers, func(w http.ResponseWriter, r *http.Request) {
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
