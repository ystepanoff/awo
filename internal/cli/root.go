package cli

import (
	"github.com/spf13/cobra"
)

const Version = "0.1.0-dev"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "awo",
		Short:         "Agent Worktree Orchestrator",
		Long:          "AWO coordinates Claude Code and Codex across isolated git worktrees with deterministic verification.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(newDoctorCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newConfigCmd())
	return root
}
