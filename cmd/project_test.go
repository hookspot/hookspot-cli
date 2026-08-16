package cmd

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/AlecAivazis/survey/v2/terminal"

	"hookspot/internal/api"
)

func TestSelectProject_PromptsWithOrganizationAndProjectNames(t *testing.T) {
	projects := []api.Project{
		{
			UID:          "proj_storefront",
			Name:         "Storefront",
			Organization: api.Organization{Name: "Acme"},
		},
		{
			UID:          "proj_payments",
			Name:         "Payments",
			Organization: api.Organization{Name: "Acme"},
		},
	}

	prompt := func(options []string, defaultIndex int) (int, error) {
		wantOptions := []string{"Acme | Storefront", "Acme | Payments"}
		if !reflect.DeepEqual(options, wantOptions) {
			t.Fatalf("options = %v, want %v", options, wantOptions)
		}
		if defaultIndex != 0 {
			t.Fatalf("default index = %d, want 0", defaultIndex)
		}
		return 1, nil
	}

	selected, err := selectProject(projects, "proj_storefront", prompt)
	if err != nil {
		t.Fatalf("selectProject() error = %v", err)
	}
	if selected.UID != "proj_payments" {
		t.Fatalf("selected UID = %q, want %q", selected.UID, "proj_payments")
	}
}

func TestSelectProject_SelectsOnlyProjectWithoutPrompt(t *testing.T) {
	projects := []api.Project{
		{
			UID:          "proj_payments",
			Name:         "Payments",
			Organization: api.Organization{Name: "Acme"},
		},
	}

	promptCalled := false
	prompt := func(options []string, defaultIndex int) (int, error) {
		promptCalled = true
		return 0, nil
	}

	selected, err := selectProject(projects, "", prompt)
	if err != nil {
		t.Fatalf("selectProject() error = %v", err)
	}
	if promptCalled {
		t.Fatal("prompt was called for a single project")
	}
	if selected.UID != "proj_payments" {
		t.Fatalf("selected UID = %q, want %q", selected.UID, "proj_payments")
	}
}

func TestSelectProject_HasNoDefaultWithoutCurrentProject(t *testing.T) {
	projects := []api.Project{
		{UID: "proj_storefront", Name: "Storefront", Organization: api.Organization{Name: "Acme"}},
		{UID: "proj_payments", Name: "Payments", Organization: api.Organization{Name: "Acme"}},
	}
	prompt := func(options []string, defaultIndex int) (int, error) {
		if defaultIndex != -1 {
			t.Fatalf("default index = %d, want -1", defaultIndex)
		}
		return 0, nil
	}

	if _, err := selectProject(projects, "", prompt); err != nil {
		t.Fatalf("selectProject() error = %v", err)
	}
}

func TestSelectProject_ReturnsErrorWhenNoProjectsExist(t *testing.T) {
	_, err := selectProject(nil, "", nil)
	if err == nil {
		t.Fatal("selectProject() returned nil error")
	}
	if got, want := err.Error(), "no projects found"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSelectProject_PropagatesInterrupt(t *testing.T) {
	projects := []api.Project{
		{UID: "proj_storefront", Name: "Storefront", Organization: api.Organization{Name: "Acme"}},
		{UID: "proj_payments", Name: "Payments", Organization: api.Organization{Name: "Acme"}},
	}
	prompt := func(options []string, defaultIndex int) (int, error) {
		return 0, fmt.Errorf("select project: %w", terminal.InterruptErr)
	}

	_, err := selectProject(projects, "", prompt)
	if !errors.Is(err, terminal.InterruptErr) {
		t.Fatalf("selectProject() error = %v, want interrupt", err)
	}
}

func TestProjectDisplayName_OnlyShowsOrganizationAndProject(t *testing.T) {
	project := api.Project{
		UID:          "proj_payments",
		Name:         "Payments",
		Slug:         "payments",
		Organization: api.Organization{UID: "org_acme", Name: "Acme", Slug: "acme"},
	}

	if got, want := projectDisplayName(project), "Acme | Payments"; got != want {
		t.Fatalf("projectDisplayName() = %q, want %q", got, want)
	}
}
