package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"hookspot/internal/api"
)

var (
	migrationSource      string
	migrationEnvironment string
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage environment-specific configuration",
}

var configMigrateCmd = &cobra.Command{
	Use:         "migrate",
	Short:       "Import one legacy configuration into this binary's environment",
	Annotations: commandAnnotations(true),
	RunE: func(cmd *cobra.Command, args []string) error {
		if migrationSource == "" {
			return newCommandError("migration source is required", "Pass the legacy file with --from PATH.")
		}
		environment := CurrentBuildInfo().Environment
		if migrationEnvironment != environment {
			return newCommandError(
				fmt.Sprintf("migration confirmation must be %q for this binary", environment),
				fmt.Sprintf("Run again with --confirm-environment %s after checking the selected backend.", environment),
			)
		}

		legacy, err := readLegacyConfig(migrationSource)
		if err != nil {
			return wrapCommandError("read legacy configuration", "Check --from and the legacy TOML fields, then try again.", err)
		}
		client := api.New(activeEndpoint, legacy.CLIKey)
		if _, err := client.Me(cmd.Context()); err != nil {
			return fmt.Errorf("validate legacy CLI key: %w", err)
		}
		if _, err := client.GetProject(cmd.Context(), legacy.Project); err != nil {
			return fmt.Errorf("validate legacy project: %w", err)
		}
		if err := store.Import(legacy.CLIKey, legacy.Project); err != nil {
			return wrapCommandError("import legacy configuration", "Choose an empty environment-specific destination; existing configuration is never overwritten.", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Configuration migrated.")
		return nil
	},
}

type legacyConfig struct {
	CLIKey  string `toml:"cli_key"`
	Project string `toml:"project"`
}

func readLegacyConfig(path string) (legacyConfig, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return legacyConfig{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return legacyConfig{}, errors.New("migration source must be a regular file, not a symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return legacyConfig{}, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return legacyConfig{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return legacyConfig{}, errors.New("migration source changed while it was being opened")
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return legacyConfig{}, err
	}
	var legacy legacyConfig
	if err := toml.Unmarshal(contents, &legacy); err != nil {
		return legacyConfig{}, fmt.Errorf("parse legacy config: %w", err)
	}
	if legacy.CLIKey == "" || legacy.Project == "" {
		return legacyConfig{}, errors.New("legacy config must contain nonempty cli_key and project fields")
	}
	return legacy, nil
}

func init() {
	configMigrateCmd.Flags().StringVar(&migrationSource, "from", "", "legacy config file to read exactly")
	configMigrateCmd.Flags().StringVar(&migrationEnvironment, "confirm-environment", "", "confirm the destination environment")
	configCmd.AddCommand(configMigrateCmd)
	rootCmd.AddCommand(configCmd)
}
