package cmd

import "github.com/spf13/cobra"

// newConfluenceCmd builds the group holding every command that talks to
// Confluence, so the top level names products rather than verbs.
//
// `plantuml` takes no globalFlags: it issues no request and resolves no
// profile, working entirely on the file `confluence read` already downloaded.
func newConfluenceCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "confluence",
		Short: "Read and edit Confluence pages",
	}

	cmd.AddCommand(
		newReadCmd(g),
		newUpdateCmd(g),
		newCreateCmd(g),
		newSearchCmd(g),
		newChildrenCmd(g),
		newCommentsCmd(g),
		newAttachmentsCmd(g),
		newPlantUMLCmd(),
	)

	return cmd
}
