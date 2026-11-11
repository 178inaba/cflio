package cmd

import (
	"strings"
	"testing"

	"github.com/178inaba/atlio/internal/config"
)

func TestProfileListShowsSitesAndMarksTheDefault(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net")

	run, err := runCLI(t, "profile", "list")
	if err != nil {
		t.Fatalf("profile list error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(run.stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q, want one line per profile", run.stdout)
	}
	// Sorted, so the listing is stable between runs.
	if !strings.HasPrefix(lines[0], "example\t") || !strings.HasPrefix(lines[1], "other\t") {
		t.Errorf("output = %q, want the profiles sorted by name", run.stdout)
	}
	if want := "example\thttps://example.atlassian.net\ta@example.com\t(default)"; lines[0] != want {
		t.Errorf("line = %q, want %q: the site, the account email and the default mark", lines[0], want)
	}
	if strings.Contains(lines[1], "(default)") {
		t.Errorf("line = %q, want only one profile marked as default", lines[1])
	}
}

func TestProfileListWithoutProfilesPointsAtAuthLogin(t *testing.T) {
	isolateConfig(t)

	run, err := runCLI(t, "profile", "list")
	if err != nil {
		t.Fatalf("profile list error = %v", err)
	}
	if !strings.Contains(run.stdout, "atlio auth login") {
		t.Errorf("output = %q, want it to point at `atlio auth login`", run.stdout)
	}
}

func TestProfileListNeverPrintsTokens(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	run, err := runCLI(t, "profile", "list")
	if err != nil {
		t.Fatalf("profile list error = %v", err)
	}
	if strings.Contains(run.stdout, "tok") {
		t.Errorf("output = %q, want the stored token kept out of the listing", run.stdout)
	}
}

func TestProfileUseSwitchesTheDefault(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net")

	if _, err := runCLI(t, "profile", "use", "other"); err != nil {
		t.Fatalf("profile use error = %v", err)
	}

	file, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if file.DefaultProfile != "other" {
		t.Errorf("default profile = %q, want other", file.DefaultProfile)
	}
}

func TestProfileUseUnknownProfileListsTheRegisteredOnes(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	_, err := runCLI(t, "profile", "use", "nope")
	if err == nil {
		t.Fatal("profile use error = nil, want an error")
	}
	for _, want := range []string{"nope", "example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
