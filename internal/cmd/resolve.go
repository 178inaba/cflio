package cmd

import (
	"os"

	"github.com/178inaba/atlio/internal/config"
	"github.com/178inaba/atlio/internal/confluence"
)

// defaultConfluenceFactory builds a production client. Tests override
// confluenceFactory to point at an httptest server instead.
func defaultConfluenceFactory(siteURL, email, token string) (*confluence.Client, error) {
	return confluence.New(siteURL, email, token)
}

var confluenceFactory = defaultConfluenceFactory

// resolveCredentials picks the profile for this invocation. urlHost is the
// host of the page the command addresses, or "" for commands that name no
// particular site (search, bare page IDs).
func resolveCredentials(profile, urlHost string) (config.Credentials, error) {
	file, err := config.Load()
	if err != nil {
		return config.Credentials{}, err
	}
	return config.Resolve(file, profile, urlHost, os.Getenv)
}

// resolveConfluence picks the profile for this invocation and builds a
// Confluence client for its site.
func resolveConfluence(profile, urlHost string) (*confluence.Client, error) {
	creds, err := resolveCredentials(profile, urlHost)
	if err != nil {
		return nil, err
	}
	return confluenceFactory(creds.SiteURL, creds.Email, creds.Token)
}
