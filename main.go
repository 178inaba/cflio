package main

import (
	"os"

	"github.com/178inaba/cflio/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
