// Package cli wires the cobra command tree for the single `push` binary.
package cli

import "github.com/spf13/cobra"

// Execute builds and runs the root command.
func Execute(version string) error {
	root := &cobra.Command{
		Use:           "push",
		Short:         "Deploy to a self-hosted server without SSH or a public port",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(
		newAgentCmd(),
		newDeployCmd(),
		newStatusCmd(),
		newEnvCmd(),
		newLogsCmd(),
		newEnableCmd(),
		newInitCmd(version),
		&cobra.Command{
			Use:   "version",
			Short: "Print the push version",
			Run:   func(c *cobra.Command, _ []string) { c.Println("push", version) },
		},
	)
	return root.Execute()
}
