package cmd

import (
	"github.com/178inaba/cflio/internal/confluence"
)

// defaultClientFactory builds a production client. Tests override
// clientFactory to point at an httptest server instead.
func defaultClientFactory(siteURL, email, token string) (*confluence.Client, error) {
	return confluence.New(siteURL, email, token)
}

var clientFactory = defaultClientFactory
