package main

import (
	"os"

	"github.com/178inaba/cflio/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
