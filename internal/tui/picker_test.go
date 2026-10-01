package tui

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"hookspot/internal/api"
)

// pickerProjects are n projects; every third belongs to Globex, the rest to
// Acme.
func pickerProjects(n int) []api.Project {
	projects := make([]api.Project, n)
	for i := range projects {
		org := "Acme"
		if i%3 == 2 {
			org = "Globex"
		}
		projects[i] = api.Project{UID: "proj_" + strconv.Itoa(i), Name: "Project " + strconv.Itoa(i+1), Organization: api.Organization{Name: org}}
	}
	return projects
}

func newPickerTest(t *testing.T, projects []api.Project, current, width, height int) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, newPicker(projects, current), teatest.WithInitialTermSize(width, height))
}

func TestPickerChooses(t *testing.T) {
	tests := []struct {
		name    string
		current int
		keys    []tea.Msg
		want    int
	}{
		{name: "move and select", current: -1, keys: []tea.Msg{down, down, up, down, enter}, want: 2},
		{name: "up wraps to the last", current: 0, keys: []tea.Msg{up, enter}, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tm := newPickerTest(t, pickerProjects(5), test.current, 80, 24)
			for _, key := range test.keys {
				tm.Send(key)
			}
			got, err := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(picker).result()
			if got != test.want || err != nil {
				t.Fatalf("result = %d, %v; want %d", got, err, test.want)
			}
		})
	}
}

func TestPickerView(t *testing.T) {
	long := []api.Project{
		{Name: "Payments platform for the EU region", Organization: api.Organization{Name: "Acme Incorporated International Holdings"}},
		{Name: "Ops\x1b[31m", Organization: api.Organization{Name: "Acme\tLabs"}},
	}
	tests := []struct {
		name          string
		projects      []api.Project
		current       int
		width, height int
		keys          []tea.Msg
	}{
		{name: "current marked", projects: pickerProjects(5), current: 1, width: 80, height: 24, keys: []tea.Msg{down}},
		{name: "no current project", projects: pickerProjects(3), current: -1, width: 80, height: 24},
		{name: "long list opens at the current project", projects: pickerProjects(30), current: 20, width: 80, height: 12},
		{name: "long list scrolls", projects: pickerProjects(30), current: 7, width: 80, height: 12, keys: []tea.Msg{down, down, down, down, down}},
		{name: "narrow", projects: long, current: 0, width: 40, height: 24, keys: []tea.Msg{down}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tm := newPickerTest(t, test.projects, test.current, test.width, test.height)
			for _, key := range test.keys {
				tm.Send(key)
			}
			golden.RequireEqual(t, []byte(final(t, tm)))
		})
	}
}

func TestPickProjectStopsWhenCancelled(t *testing.T) {
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })
	out := &output{}
	ctx, cancel := context.WithCancel(context.Background())
	picked := make(chan error, 1)
	go func() {
		_, err := PickProject(ctx, input, out, pickerProjects(3), -1)
		picked <- err
	}()
	// Output means the picker runs; without a terminal's size it draws no box.
	waitFor(t, out, func(drawn []byte) bool { return len(drawn) > 0 })
	cancel()
	if err := receive(t, picked); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
