package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect AWO configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Print the effective AWO configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			cfg, source, err := config.LoadOrDefault(filepath.Join(cwd, "awo.config.json"))
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "# source: %s\n%s\n", source, string(b))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "# resolved per-role argv (what AWO will actually invoke)")
			fmt.Fprintln(out, "# AWO runs every agent non-interactively; these are the args used as-is.")
			roles := []domain.AgentRole{domain.RoleWriter, domain.RoleReviewer, domain.RoleCompetitor}
			for _, role := range roles {
				fmt.Fprintf(out, "  claude %s: %s\n", role, strings.Join(cfg.Agents.Claude.RoleArgs(role), " "))
			}
			for _, role := range roles {
				fmt.Fprintf(out, "  codex  %s: %s\n", role, strings.Join(cfg.Agents.Codex.RoleArgs(role), " "))
			}
			return nil
		},
	})
	return cmd
}
