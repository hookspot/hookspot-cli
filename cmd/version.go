package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var versionJSON bool

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the hookspot-cli version",
	RunE: func(cmd *cobra.Command, args []string) error {
		info := CurrentBuildInfo()
		if versionJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
		}
		executable, err := os.Executable()
		if err != nil {
			executable = cmd.Root().Name()
		}
		return writeHumanVersion(cmd.OutOrStdout(), filepath.Base(executable), info)
	},
}

func writeHumanVersion(out io.Writer, executable string, info BuildInfo) error {
	server := info.ServerURL
	if server == "" {
		server = "not configured"
	}
	_, err := fmt.Fprintf(out, "%s %s\nenvironment: %s\nserver: %s\nsource: %s (%s)\nbuild: %s\nplatform: %s %s/%s\n",
		executable, info.Version, info.Environment, server, info.Commit, info.SourceDate,
		info.BuildKind, info.GoVersion, info.OS, info.Arch)
	return err
}

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "print stable JSON build information")
	rootCmd.AddCommand(versionCmd)
}
