package main

import (
	"os"

	"hookspot/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if exitCode := cmd.HandleError(os.Stderr, err); exitCode != 0 {
			os.Exit(exitCode)
		}
	}
}
