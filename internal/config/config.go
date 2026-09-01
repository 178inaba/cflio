// Package config manages cflio's on-disk configuration (registered
// Confluence site profiles and their API tokens) and resolves which
// credentials an invocation should use.
package config

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Profile holds the stored credentials for a single registered site.
type Profile struct {
	// SiteURL is the Confluence base URL including the /wiki path, e.g.
	// "https://example.atlassian.net/wiki". The host is derived from it
	// rather than stored separately, so there is no second copy to drift.
	SiteURL string `json:"site_url"`
	Email   string `json:"email"`
	Token   string `json:"token"`
}

// Host returns the profile's site host, or "" if SiteURL cannot be parsed.
// `auth login` validates SiteURL before storing it, so an unparseable value
// means a hand-edited config; the empty host then simply fails to match any
// URL, which surfaces as the regular unregistered-host error.
func (p Profile) Host() string {
	u, err := url.Parse(p.SiteURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// File is the on-disk representation of ~/.config/cflio/config.json.
type File struct {
	DefaultProfile string             `json:"default_profile"`
	Profiles       map[string]Profile `json:"profiles"`
}

// Dir returns the directory cflio's config file lives in, honoring
// XDG_CONFIG_HOME.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "cflio"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cflio"), nil
}

// Path returns the full path to the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file. A missing file is not an error: it returns an
// empty File, since that's the normal state before the first `auth login`.
func Load() (*File, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Profile{}
	}
	return &f, nil
}

// Save writes the config file, creating its directory if needed. The
// directory and file permissions are restricted (0700/0600) since the file
// holds plaintext API tokens.
func (f *File) Save() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Deterministic because Profiles is a map and v2 emits map members in
	// whatever order the runtime hands them over: without it, every save
	// reshuffles a file people read and hand-edit.
	data, err := json.Marshal(f, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

// Credentials is the resolved outcome of Resolve: everything a single
// invocation needs to authenticate, plus the name of the profile it came
// from (needed for error messages and status output).
type Credentials struct {
	Profile string
	SiteURL string
	Email   string
	Token   string
}

// Resolve implements cflio's credential precedence for a single invocation:
//
//  1. --profile flag (flagProfile): uses that profile. Errors if the profile
//     doesn't exist, or if urlHost is set and doesn't match the profile's
//     host — talking to the wrong site would yield an indistinguishable
//     "page not found".
//  2. urlHost auto-selection: the profile whose host matches urlHost. Errors
//     if no profile is registered for that host; there is deliberately no
//     fallback to the default profile, for the same reason as above.
//  3. CFLIO_PROFILE, which stands in for the default profile. It does not
//     override URL-host auto-selection, and it is not checked for host
//     conflicts — only an explicit --profile is.
//  4. The default profile.
//
// CFLIO_TOKEN, if set, then replaces the resolved profile's token; the site
// and email still come from the profile, so a profile must resolve either
// way. Shared-namespace variables such as ATLASSIAN_API_TOKEN are
// deliberately never read.
func Resolve(f *File, flagProfile, urlHost string, getenv func(string) string) (Credentials, error) {
	creds, err := resolveProfile(f, flagProfile, urlHost, getenv)
	if err != nil {
		return Credentials{}, err
	}
	if token := getenv("CFLIO_TOKEN"); token != "" {
		creds.Token = token
	}
	return creds, nil
}

func resolveProfile(f *File, flagProfile, urlHost string, getenv func(string) string) (Credentials, error) {
	if flagProfile != "" {
		p, ok := f.Profiles[flagProfile]
		if !ok {
			return Credentials{}, fmt.Errorf("profile %q not found; registered profiles: %s",
				flagProfile, ProfileNames(f))
		}
		if urlHost != "" && urlHost != p.Host() {
			return Credentials{}, fmt.Errorf(
				"--profile %q (site %s) conflicts with the URL's host %s; pass a matching --profile or omit it",
				flagProfile, p.Host(), urlHost)
		}
		return credentials(flagProfile, p), nil
	}

	if urlHost != "" {
		for _, name := range SortedProfileNames(f) {
			if f.Profiles[name].Host() == urlHost {
				return credentials(name, f.Profiles[name]), nil
			}
		}
		return Credentials{}, fmt.Errorf(
			"no profile registered for %s; registered profiles: %s; run `cflio auth login` to register it",
			urlHost, ProfileNames(f))
	}

	if name := getenv("CFLIO_PROFILE"); name != "" {
		p, ok := f.Profiles[name]
		if !ok {
			return Credentials{}, fmt.Errorf("CFLIO_PROFILE %q not found; registered profiles: %s",
				name, ProfileNames(f))
		}
		return credentials(name, p), nil
	}

	if f.DefaultProfile == "" {
		return Credentials{}, fmt.Errorf("no profiles registered; run `cflio auth login` first")
	}
	p, ok := f.Profiles[f.DefaultProfile]
	if !ok {
		return Credentials{}, fmt.Errorf("default profile %q not found; registered profiles: %s",
			f.DefaultProfile, ProfileNames(f))
	}
	return credentials(f.DefaultProfile, p), nil
}

func credentials(name string, p Profile) Credentials {
	return Credentials{Profile: name, SiteURL: p.SiteURL, Email: p.Email, Token: p.Token}
}

// ProfileNames returns the registered profile names, sorted and
// comma-joined, for use in error messages. Returns "(none)" if none are
// registered.
func ProfileNames(f *File) string {
	if len(f.Profiles) == 0 {
		return "(none)"
	}
	return strings.Join(SortedProfileNames(f), ", ")
}

// SortedProfileNames returns the registered profile names in a stable
// order. Every listing and lookup that walks the profiles goes through it,
// so error messages, `profile list` and the host-matching scan can never
// disagree about ordering the way raw map iteration would.
func SortedProfileNames(f *File) []string {
	names := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
