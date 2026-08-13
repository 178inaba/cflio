package cmd

import (
	"os"

	"github.com/178inaba/cflio/internal/config"
	"github.com/178inaba/cflio/internal/confluence"
)

// defaultClientFactory builds a production client. Tests override
// clientFactory to point at an httptest server instead.
func defaultClientFactory(siteURL, email, token string) (*confluence.Client, error) {
	return confluence.New(siteURL, email, token)
}

var clientFactory = defaultClientFactory

// resolveClient picks the profile for this invocation and builds a client
// for it. urlHost is the host of the page the command addresses, or "" for
// commands that name no particular site (search, bare page IDs).
func resolveClient(profile, urlHost string) (*confluence.Client, config.Credentials, error) {
	file, err := config.Load()
	if err != nil {
		return nil, config.Credentials{}, err
	}

	creds, err := config.Resolve(file, profile, urlHost, os.Getenv)
	if err != nil {
		return nil, config.Credentials{}, err
	}

	client, err := clientFactory(creds.SiteURL, creds.Email, creds.Token)
	if err != nil {
		return nil, config.Credentials{}, err
	}
	return client, creds, nil
}
