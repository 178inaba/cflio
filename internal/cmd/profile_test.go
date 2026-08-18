package cmd

import (
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/config"
)

func TestProfileListShowsSitesAndMarksTheDefault(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	run, err := runCflio(t, "profile", "list")
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
	if !strings.Contains(lines[0], testSite) || !strings.Contains(lines[0], "a@example.com") {
		t.Errorf("line = %q, want it to carry the site and the account email", lines[0])
	}
	if !strings.Contains(lines[0], "(default)") {
		t.Errorf("line = %q, want the default profile marked", lines[0])
	}
	if strings.Contains(lines[1], "(default)") {
		t.Errorf("line = %q, want only one profile marked as default", lines[1])
	}
}

func TestProfileListWithoutProfilesPointsAtAuthLogin(t *testing.T) {
	isolateConfig(t)

	run, err := runCflio(t, "profile", "list")
	if err != nil {
		t.Fatalf("profile list error = %v", err)
	}
	if !strings.Contains(run.stdout, "cflio auth login") {
		t.Errorf("output = %q, want it to point at `cflio auth login`", run.stdout)
	}
}

func TestProfileListNeverPrintsTokens(t *testing.T) {
	isolateConfig(t)
	seedProfile(t, "example", testSite)

	run, err := runCflio(t, "profile", "list")
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
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	if _, err := runCflio(t, "profile", "use", "other"); err != nil {
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

	_, err := runCflio(t, "profile", "use", "nope")
	if err == nil {
		t.Fatal("profile use error = nil, want an error")
	}
	for _, want := range []string{"nope", "example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
