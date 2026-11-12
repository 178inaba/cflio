package cmd

import (
	"os"

	"github.com/178inaba/atlio/internal/config"
	"github.com/178inaba/atlio/internal/confluence"
	"github.com/178inaba/atlio/internal/jira"
)

// defaultConfluenceFactory builds a production client. Tests override
// confluenceFactory to point at an httptest server instead.
func defaultConfluenceFactory(siteURL, email, token string) (*confluence.Client, error) {
	return confluence.New(siteURL, email, token)
}

var confluenceFactory = defaultConfluenceFactory

// defaultJiraFactory builds a production client. Tests override jiraFactory
// to point at an httptest server instead.
func defaultJiraFactory(siteURL, email, token string) (*jira.Client, error) {
	return jira.New(siteURL, email, token)
}

var jiraFactory = defaultJiraFactory

// resolveCredentials picks the profile for this invocation. urlHost is the
// host of the page or issue the command addresses, or "" for commands that
// name no particular site (search, bare page IDs and issue keys).
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

// resolveJira picks the profile for this invocation and builds a Jira client
// for its site.
func resolveJira(profile, urlHost string) (*jira.Client, error) {
	creds, err := resolveCredentials(profile, urlHost)
	if err != nil {
		return nil, err
	}
	return jiraFactory(creds.SiteURL, creds.Email, creds.Token)
}
