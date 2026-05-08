package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// errShellUnknown signals that --shell was not set and $SHELL could
// not be interpreted.
var errShellUnknown = errors.New("unable to detect shell; pass --shell explicitly")

// completionInstallFlags bundles the install-subcommand flags.
type completionInstallFlags struct {
	Shell     string
	DryRun    bool
	Uninstall bool
}

// newCompletionInstallCmd builds `usbip completion install`. The
// generated script is written to the XDG-appropriate directory so
// interactive shells pick it up on next login. --dry-run prints the
// target path without writing.
func newCompletionInstallCmd(root *cobra.Command) *cobra.Command {
	cf := &completionInstallFlags{}

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write a shell completion script to the XDG-appropriate path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCompletionInstall(cmd, root, cf)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cf.Shell, "shell", "", "shell to target (bash/zsh/fish/pwsh)")
	flags.BoolVar(&cf.DryRun, "dry-run", false, "print target path without writing")
	flags.BoolVar(&cf.Uninstall, "uninstall", false, "remove a previously installed script")

	return cmd
}

// runCompletionInstall dispatches install/uninstall based on flags.
func runCompletionInstall(cmd *cobra.Command, root *cobra.Command, cf *completionInstallFlags) error {
	shell, err := resolveShell(cf.Shell)
	if err != nil {
		return errUsage("%s", err)
	}

	target, err := completionPath(shell)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	switch {
	case cf.Uninstall:
		return runUninstall(out, target)
	case cf.DryRun:
		_, wErr := fmt.Fprintln(out, target)
		if wErr != nil {
			return fmt.Errorf("write dry-run output: %w", wErr)
		}

		return nil
	}

	return writeCompletionScript(out, root, shell, target)
}

// writeCompletionScript renders the cobra script for shell and writes
// it to target, creating parent dirs as needed.
func writeCompletionScript(out ioWriter, root *cobra.Command, shell, target string) error {
	var buf bytes.Buffer

	err := generateScript(root, shell, &buf)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(target), completionDirMode)
	if err != nil {
		return fmt.Errorf("mkdir completion dir: %w", err)
	}

	err = os.WriteFile(target, buf.Bytes(), completionFileMode)
	if err != nil {
		return fmt.Errorf("write completion script: %w", err)
	}

	_, err = fmt.Fprintf(out, "installed %s completion to %s\n", shell, target)
	if err != nil {
		return fmt.Errorf("write status line: %w", err)
	}

	return nil
}

// runUninstall removes the installed script at target. Missing is OK.
func runUninstall(out ioWriter, target string) error {
	err := os.Remove(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove completion script: %w", err)
	}

	_, err = fmt.Fprintf(out, "removed %s\n", target)
	if err != nil {
		return fmt.Errorf("write status line: %w", err)
	}

	return nil
}

// resolveShell returns the target shell name. Priority: explicit
// --shell > basename of $SHELL. An empty result is an error.
func resolveShell(explicit string) (string, error) {
	if explicit != "" {
		return validateShell(explicit)
	}

	env := os.Getenv("SHELL")
	if env == "" {
		return "", errShellUnknown
	}

	return validateShell(filepath.Base(env))
}

// shellNames are the cobra-supported shell names. A small canonical
// set that doubles as validation and goconst-suppression.
const (
	shellBash       = "bash"
	shellZsh        = "zsh"
	shellFish       = "fish"
	shellPwsh       = "pwsh"
	shellPowershell = "powershell"
)

// validateShell ensures the name is one of the cobra-supported shells.
func validateShell(name string) (string, error) {
	switch name {
	case shellBash, shellZsh, shellFish, shellPwsh, shellPowershell:
		return name, nil
	default:
		return "", fmt.Errorf("%w: %q is not bash/zsh/fish/pwsh", errShellUnknown, name)
	}
}

// completionPath returns the XDG-appropriate install path for shell.
// Bash lands in ~/.local/share/bash-completion/completions/usbip.
// Zsh lands in ~/.local/share/zsh/site-functions/_usbip so users can
// prepend the directory to fpath. Other shells emit a conservative
// default in the same share tree.
func completionPath(shell string) (string, error) {
	data, err := xdgDataHome()
	if err != nil {
		return "", err
	}

	switch shell {
	case shellBash:
		return filepath.Join(data, "bash-completion", "completions", "usbip"), nil
	case shellZsh:
		return filepath.Join(data, "zsh", "site-functions", "_usbip"), nil
	case shellFish:
		return filepath.Join(data, "fish", "vendor_completions.d", "usbip.fish"), nil
	case shellPwsh, shellPowershell:
		return filepath.Join(data, "powershell", "Modules", "usbip.ps1"), nil
	default:
		return "", fmt.Errorf("%w: %q", errShellUnknown, shell)
	}
}

// xdgDataHome returns $XDG_DATA_HOME when set; else $HOME/.local/share.
func xdgDataHome() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}

	return filepath.Join(home, ".local", "share"), nil
}

// generateScript invokes the cobra completion generator for shell and
// writes the script bytes to out.
func generateScript(root *cobra.Command, shell string, out ioWriter) error {
	switch shell {
	case shellBash:
		err := root.GenBashCompletion(out)
		if err != nil {
			return fmt.Errorf("generate bash completion: %w", err)
		}
	case shellZsh:
		err := root.GenZshCompletion(out)
		if err != nil {
			return fmt.Errorf("generate zsh completion: %w", err)
		}
	case shellFish:
		err := root.GenFishCompletion(out, true)
		if err != nil {
			return fmt.Errorf("generate fish completion: %w", err)
		}
	case shellPwsh, shellPowershell:
		err := root.GenPowerShellCompletionWithDesc(out)
		if err != nil {
			return fmt.Errorf("generate powershell completion: %w", err)
		}
	default:
		return fmt.Errorf("%w: %q", errShellUnknown, shell)
	}

	return nil
}

const (
	// completionFileMode is the permissions for the written script.
	// 0o644 lets other users read the script (needed for system-wide
	// bash-completion loaders that run out of /etc/bash_completion.d).
	completionFileMode os.FileMode = 0o644

	// completionDirMode matches the umask-friendly default for
	// ~/.local/share subdirs.
	completionDirMode os.FileMode = 0o755
)
