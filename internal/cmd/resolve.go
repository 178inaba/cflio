package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/178inaba/atlio/internal/config"
	"github.com/178inaba/atlio/internal/confluence"
	"github.com/178inaba/atlio/internal/jira"
	"github.com/178inaba/atlio/internal/pageref"
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

// resolvePage turns a page argument into the client for the site it names
// and the page's ID. Every command that addresses a page goes through here,
// so the forms a page can be named in, and the profile a URL selects, are the
// same on each of them.
//
// It takes the command's context because a space URL that carries no
// homepageId costs one request to look its homepage up.
func resolvePage(ctx context.Context, arg, profile string) (*confluence.Client, string, error) {
	ref, err := pageref.Parse(arg)
	if err != nil {
		return nil, "", err
	}

	client, err := resolveConfluence(profile, ref.Host)
	if err != nil {
		return nil, "", err
	}

	pageID, err := pageIDOf(ctx, client, ref.Target)
	if err != nil {
		return nil, "", err
	}
	return client, pageID, nil
}

// pageIDOf is the one place a page ID is read out of a reference.
func pageIDOf(ctx context.Context, client *confluence.Client, target pageref.Target) (string, error) {
	switch t := target.(type) {
	case pageref.PageID:
		return string(t), nil
	case pageref.SpaceHomepage:
		return client.SpaceHomepageID(ctx, t.Key)
	default:
		// Falling through with an empty ID would send GET /pages/ instead.
		return "", fmt.Errorf("page reference names no page: %#v", target)
	}
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
