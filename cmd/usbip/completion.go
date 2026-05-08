package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// withCompletionInstall replaces cobra's auto-generated `completion`
// command with our own. Cobra's default captures the parent's writer at
// build time, which breaks tests that SetOut after command construction.
// We keep the same subcommand layout (bash/zsh/fish/pwsh) and dispatch
// to the cobra Gen* helpers at runtime through cmd.OutOrStdout().
func withCompletionInstall(root *cobra.Command) {
	// Drop cobra's default to avoid two `completion` commands.
	root.CompletionOptions.DisableDefaultCmd = true

	comp := &cobra.Command{
		Use:   "completion",
		Short: "Generate shell completion scripts",
	}

	comp.AddCommand(newShellCompletionCmd(shellBash, root))
	comp.AddCommand(newShellCompletionCmd(shellZsh, root))
	comp.AddCommand(newShellCompletionCmd(shellFish, root))
	comp.AddCommand(newShellCompletionCmd(shellPwsh, root))
	comp.AddCommand(newCompletionInstallCmd(root))

	root.AddCommand(comp)
}

// newShellCompletionCmd builds a single `completion <shell>` leaf that
// writes to cmd.OutOrStdout() at run time.
func newShellCompletionCmd(shell string, root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   shell,
		Short: fmt.Sprintf("Generate the %s completion script", shell),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return generateScript(root, shell, cmd.OutOrStdout())
		},
	}
}
