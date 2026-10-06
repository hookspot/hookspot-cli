package cards

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"

	"hookspot/internal/api"
)

// noColor writes card the way a command does to a pipe.
func noColor(t *testing.T, card string) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := lipgloss.Fprintln(&out, card); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestLogin(t *testing.T) {
	url := "https://hookspot.invalid/cli/login/browser-token"
	t.Run("opened", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Login("ABCD-1234", url, true, 80)))
	})
	t.Run("hostile input without browser", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Login("AB\x1b[2JCD", url+"\n\x07", false, 80)))
	})
}

func TestDone(t *testing.T) {
	golden.RequireEqual(t, noColor(t, strings.Join([]string{
		Done("Logged in as", "dev@example.com"),
		Done("Logged out", ""),
		Done("Logged in as", "evil\x1b]0;title\x07\n@example.com"),
	}, "\n")))
}

// cmd's tests pin the saved-key-only logout, the key prompt and the update card.
func TestLogout(t *testing.T) {
	golden.RequireEqual(t, noColor(t, Logout(true, 80)))
}

func TestProjects(t *testing.T) {
	projects := []api.Project{
		{UID: "proj_storefront", Name: "Storefront", Organization: api.Organization{Name: "Acme Inc."}},
		{UID: "proj_payments", Name: "Payments", Organization: api.Organization{Name: "Acme Inc."}},
		{UID: "proj_evil", Name: "Bill\x1b[31ming\n", Organization: api.Organization{Name: "Other\tOrg"}},
	}
	t.Run("active project", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Projects(projects, "proj_payments", 80)))
	})
	t.Run("no projects", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Projects(nil, "", 80)))
	})
}

func TestError(t *testing.T) {
	message := "resolve project: GET https://example.test/cli/projects/proj_1: 404 Not Found: not_found"
	hint := "The project may have been deleted or your access removed. " +
		"Select a project with 'hookspot project use', 'hookspot login', --project, or HOOKSPOT_ORGANIZATION_SLUG and HOOKSPOT_PROJECT_SLUG."
	for _, width := range []int{80, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			golden.RequireEqual(t, noColor(t, Error(message, hint, width)))
		})
	}
	t.Run("hostile without hint", func(t *testing.T) {
		golden.RequireEqual(t, noColor(t, Error("bad\x1b[31m\rvalue\nsecond\tline", "", 80)))
	})
}
