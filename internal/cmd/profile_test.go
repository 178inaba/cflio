package cmd

import (
	"strings"
	"testing"

	"github.com/178inaba/cflio/internal/config"
)

func TestProfileListShowsSitesAndMarksTheDefault(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	cmd, out := newTestCommand(t)
	if err := runProfileList(cmd, nil); err != nil {
		t.Fatalf("runProfileList() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("output = %q, want one line per profile", out.String())
	}
	// Sorted, so the listing is stable between runs.
	if !strings.HasPrefix(lines[0], "example\t") || !strings.HasPrefix(lines[1], "other\t") {
		t.Errorf("output = %q, want the profiles sorted by name", out.String())
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
	setFlags(t, "", "md")

	cmd, out := newTestCommand(t)
	if err := runProfileList(cmd, nil); err != nil {
		t.Fatalf("runProfileList() error = %v", err)
	}
	if !strings.Contains(out.String(), "cflio auth login") {
		t.Errorf("output = %q, want it to point at `cflio auth login`", out.String())
	}
}

func TestProfileListNeverPrintsTokens(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	cmd, out := newTestCommand(t)
	if err := runProfileList(cmd, nil); err != nil {
		t.Fatalf("runProfileList() error = %v", err)
	}
	if strings.Contains(out.String(), "tok") {
		t.Errorf("output = %q, want the stored token kept out of the listing", out.String())
	}
}

func TestProfileUseSwitchesTheDefault(t *testing.T) {
	isolateConfig(t)
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)
	seedProfile(t, "other", "https://other.atlassian.net/wiki")

	cmd, _ := newTestCommand(t)
	if err := runProfileUse(cmd, []string{"other"}); err != nil {
		t.Fatalf("runProfileUse() error = %v", err)
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
	setFlags(t, "", "md")
	seedProfile(t, "example", testSite)

	cmd, _ := newTestCommand(t)
	err := runProfileUse(cmd, []string{"nope"})
	if err == nil {
		t.Fatal("runProfileUse() error = nil, want an error")
	}
	for _, want := range []string{"nope", "example"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
