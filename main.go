// Command atlio is an Atlassian Cloud CLI for AI coding agents: it reads Jira
// issues, and reads, creates and updates Confluence pages, through local
// files, without the body passing through the model.
package main

import (
	"os"

	"github.com/178inaba/atlio/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
