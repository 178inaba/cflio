package cmd

import (
	"fmt"

	"github.com/178inaba/cflio/internal/config"
	"github.com/spf13/cobra"
)

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage named site profiles",
	}
	cmd.AddCommand(newProfileListCmd(), newProfileUseCmd())
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered profiles",
		Args:  cobra.NoArgs,
		RunE:  runProfileList,
	}
}

func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the default profile",
		Args:  cobra.ExactArgs(1),
		RunE:  runProfileUse,
	}
}

func runProfileList(cmd *cobra.Command, args []string) error {
	file, err := config.Load()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	if len(file.Profiles) == 0 {
		_, err := fmt.Fprintln(out, "No profiles registered. Run `cflio auth login` to add one.")
		return err
	}

	for _, name := range config.SortedProfileNames(file) {
		p := file.Profiles[name]
		var err error
		if name == file.DefaultProfile {
			_, err = fmt.Fprintf(out, "%s\t%s\t%s\t(default)\n", name, p.SiteURL, p.Email)
		} else {
			_, err = fmt.Fprintf(out, "%s\t%s\t%s\n", name, p.SiteURL, p.Email)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func runProfileUse(cmd *cobra.Command, args []string) error {
	name := args[0]

	file, err := config.Load()
	if err != nil {
		return err
	}
	if _, ok := file.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found; registered profiles: %s", name, config.ProfileNames(file))
	}

	file.DefaultProfile = name
	if err := file.Save(); err != nil {
		return err
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Default profile set to %q.\n", name)
	return err
}
