// Command cflio is a Confluence Cloud CLI built for AI coding agents.
package main

import (
	"os"

	"github.com/178inaba/cflio/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
