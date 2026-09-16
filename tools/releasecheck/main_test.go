package main

import "testing"

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {"artifacts"}, {"env", "--environment", "prod"}} {
		err := run(args)
		if err == nil || err.Error() != "usage: releasecheck metadata" {
			t.Fatalf("run(%q) = %v, want usage error", args, err)
		}
	}
}
