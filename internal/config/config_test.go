package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileHost(t *testing.T) {
	tests := []struct {
		name    string
		siteURL string
		want    string
	}{
		{name: "site base", siteURL: "https://example.atlassian.net/wiki", want: "example.atlassian.net"},
		{name: "no path", siteURL: "https://example.atlassian.net", want: "example.atlassian.net"},
		{name: "unparseable", siteURL: "://nope", want: ""},
		{name: "empty", siteURL: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Profile{SiteURL: tt.siteURL}).Host(); got != tt.want {
				t.Errorf("Host() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	f, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a missing file", err)
	}
	if len(f.Profiles) != 0 {
		t.Errorf("Load() profiles = %v, want empty", f.Profiles)
	}
}

func TestSaveUsesRestrictivePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	f := &File{
		DefaultProfile: "example",
		Profiles: map[string]Profile{
			"example": {SiteURL: "https://example.atlassian.net/wiki", Email: "a@example.com", Token: "t"},
		},
	}
	if err := f.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	dir := filepath.Join(root, "cflio")
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", dir, err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("config directory mode = %o, want 700", got)
	}

	path := filepath.Join(dir, "config.json")
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("config file mode = %o, want 600 (the file holds plaintext tokens)", got)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.DefaultProfile != "example" || reloaded.Profiles["example"].Token != "t" {
		t.Errorf("Load() = %+v, want the saved contents back", reloaded)
	}
}

func TestProfileNames(t *testing.T) {
	empty := &File{Profiles: map[string]Profile{}}
	if got := ProfileNames(empty); got != "(none)" {
		t.Errorf("ProfileNames(empty) = %q, want %q", got, "(none)")
	}

	f := &File{Profiles: map[string]Profile{"zeta": {}, "alpha": {}, "mid": {}}}
	if got, want := ProfileNames(f), "alpha, mid, zeta"; got != want {
		t.Errorf("ProfileNames() = %q, want %q (sorted for stable error messages)", got, want)
	}
}

// testFile is the two-profile fixture the Resolve cases share.
func testFile() *File {
	return &File{
		DefaultProfile: "example",
		Profiles: map[string]Profile{
			"example": {SiteURL: "https://example.atlassian.net/wiki", Email: "a@example.com", Token: "tok-example"},
			"other":   {SiteURL: "https://other.atlassian.net/wiki", Email: "b@other.com", Token: "tok-other"},
		},
	}
}

func noEnv(string) string { return "" }

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestResolvePrecedence(t *testing.T) {
	tests := []struct {
		name        string
		flagProfile string
		urlHost     string
		getenv      func(string) string
		want        Credentials
	}{
		{
			name:        "explicit --profile wins",
			flagProfile: "other",
			getenv:      env(map[string]string{"CFLIO_PROFILE": "example"}),
			want: Credentials{
				Profile: "other", SiteURL: "https://other.atlassian.net/wiki",
				Email: "b@other.com", Token: "tok-other",
			},
		},
		{
			name:    "url host auto-selects the profile",
			urlHost: "other.atlassian.net",
			getenv:  noEnv,
			want: Credentials{
				Profile: "other", SiteURL: "https://other.atlassian.net/wiki",
				Email: "b@other.com", Token: "tok-other",
			},
		},
		{
			name:    "url host beats CFLIO_PROFILE",
			urlHost: "other.atlassian.net",
			getenv:  env(map[string]string{"CFLIO_PROFILE": "example"}),
			want: Credentials{
				Profile: "other", SiteURL: "https://other.atlassian.net/wiki",
				Email: "b@other.com", Token: "tok-other",
			},
		},
		{
			name:   "CFLIO_PROFILE replaces the default profile",
			getenv: env(map[string]string{"CFLIO_PROFILE": "other"}),
			want: Credentials{
				Profile: "other", SiteURL: "https://other.atlassian.net/wiki",
				Email: "b@other.com", Token: "tok-other",
			},
		},
		{
			name:   "falls back to the default profile",
			getenv: noEnv,
			want: Credentials{
				Profile: "example", SiteURL: "https://example.atlassian.net/wiki",
				Email: "a@example.com", Token: "tok-example",
			},
		},
		{
			name:   "CFLIO_TOKEN replaces only the token",
			getenv: env(map[string]string{"CFLIO_TOKEN": "env-token"}),
			want: Credentials{
				Profile: "example", SiteURL: "https://example.atlassian.net/wiki",
				Email: "a@example.com", Token: "env-token",
			},
		},
		{
			name:    "CFLIO_TOKEN does not bypass url-host selection",
			urlHost: "other.atlassian.net",
			getenv:  env(map[string]string{"CFLIO_TOKEN": "env-token"}),
			want: Credentials{
				Profile: "other", SiteURL: "https://other.atlassian.net/wiki",
				Email: "b@other.com", Token: "env-token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(testFile(), tt.flagProfile, tt.urlHost, tt.getenv)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name         string
		file         *File
		flagProfile  string
		urlHost      string
		getenv       func(string) string
		wantContains []string
	}{
		{
			name:         "unknown --profile lists the registered ones",
			file:         testFile(),
			flagProfile:  "nope",
			getenv:       noEnv,
			wantContains: []string{"nope", "example, other"},
		},
		{
			name:         "explicit --profile conflicting with the url host",
			file:         testFile(),
			flagProfile:  "example",
			urlHost:      "other.atlassian.net",
			getenv:       noEnv,
			wantContains: []string{"example", "example.atlassian.net", "other.atlassian.net"},
		},
		{
			name:         "unregistered url host names the host, the profiles and the hint",
			file:         testFile(),
			urlHost:      "unknown.atlassian.net",
			getenv:       noEnv,
			wantContains: []string{"unknown.atlassian.net", "example, other", "cflio auth login"},
		},
		{
			name:         "unregistered url host does not fall back to the default profile",
			file:         testFile(),
			urlHost:      "unknown.atlassian.net",
			getenv:       env(map[string]string{"CFLIO_PROFILE": "example"}),
			wantContains: []string{"unknown.atlassian.net"},
		},
		{
			name:         "unknown CFLIO_PROFILE",
			file:         testFile(),
			getenv:       env(map[string]string{"CFLIO_PROFILE": "nope"}),
			wantContains: []string{"CFLIO_PROFILE", "nope", "example, other"},
		},
		{
			name:         "no profiles registered",
			file:         &File{Profiles: map[string]Profile{}},
			getenv:       noEnv,
			wantContains: []string{"cflio auth login"},
		},
		{
			name: "default profile points at a missing entry",
			file: &File{DefaultProfile: "gone", Profiles: map[string]Profile{
				"example": {SiteURL: "https://example.atlassian.net/wiki"},
			}},
			getenv:       noEnv,
			wantContains: []string{"gone", "example"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.file, tt.flagProfile, tt.urlHost, tt.getenv)
			if err == nil {
				t.Fatal("Resolve() error = nil, want an error")
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Resolve() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestResolveCFLIOTokenDoesNotCreateAProfile(t *testing.T) {
	// CFLIO_TOKEN overrides a resolved profile's token; it is not a way to
	// run without any profile at all, since the site and email still have
	// to come from somewhere.
	_, err := Resolve(&File{Profiles: map[string]Profile{}}, "", "",
		env(map[string]string{"CFLIO_TOKEN": "env-token"}))
	if err == nil {
		t.Fatal("Resolve() error = nil, want an error when only CFLIO_TOKEN is set")
	}
}

func TestLoadReportsCorruptConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	dir := filepath.Join(root, "cflio")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want a parse error")
	}
	if _, ok := errors.AsType[*os.PathError](err); ok {
		t.Errorf("Load() error = %v, want a parse error rather than a path error", err)
	}
}
