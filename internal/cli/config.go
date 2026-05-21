package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/awo-dev/awo/internal/config"
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
			fmt.Fprintf(cmd.OutOrStdout(), "# source: %s\n%s\n", source, string(b))
			return nil
		},
	})
	return cmd
}
