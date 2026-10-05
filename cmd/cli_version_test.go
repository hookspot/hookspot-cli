package cmd

import (
	"runtime"
	"testing"
)

// TestListenNamesItsReleaseInTheUserAgent covers the API calls and the
// websocket handshake of a release and of a dev build.
func TestListenNamesItsReleaseInTheUserAgent(t *testing.T) {
	for name, metadata := range map[string]map[string]string{"release": releaseMetadata("1.2.3"), "dev": developmentMetadata("")} {
		t.Run(name, func(t *testing.T) {
			hookspot := startFakeHookspot(t, fakeHookspotSources)
			hookspot.joinReplies <- notFoundReply
			metadata["server_url"] = hookspot.url
			runCommandProcess(t, "", metadata, hookspot.listen()...)

			want := "hookspot-cli/" + metadata["version"] + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
			// The project, its sources, then the websocket.
			if got := len(hookspot.userAgents); got != 3 {
				t.Fatalf("requests = %d, want 3", got)
			}
			for range 3 {
				if got := <-hookspot.userAgents; got != want {
					t.Errorf("User-Agent = %q, want %q", got, want)
				}
			}
		})
	}
}
