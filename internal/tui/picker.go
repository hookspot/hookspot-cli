package tui

import (
	"context"
	"io"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"hookspot/internal/api"
	"hookspot/internal/cards"
)

// PickProject shows projects in the picker box on output, reads keys from
// input and returns the chosen project's index. The project at current, none
// when -1, is marked and selected first. Esc, Ctrl-C and ctx ending cancel
// with context.Canceled.
func PickProject(ctx context.Context, input io.Reader, output io.Writer, projects []api.Project, current int) (int, error) {
	final, err := tea.NewProgram(newPicker(projects, current),
		tea.WithContext(ctx),
		// main.go stays the only signal handler.
		tea.WithoutSignalHandler(),
		tea.WithInput(input),
		tea.WithOutput(output),
	).Run()
	if err != nil {
		return 0, err
	}
	return final.(picker).result()
}

// pickerKeys sit under the picker's rows.
var pickerKeys = cards.Badge("↑↓") + faintStyle.Render(" move  ") +
	cards.Badge("↵") + faintStyle.Render(" select  ") +
	cards.Badge("esc") + faintStyle.Render(" cancel")

// picker lists projects, organization and project in columns, scrolling to
// keep the selection in view. Once chosen or cancelled it draws nothing, so
// the box leaves the screen.
type picker struct {
	projects []api.Project
	current  int
	selected int
	// offset is the index of the first row shown.
	offset            int
	width, height     int
	chosen, cancelled bool
}

func newPicker(projects []api.Project, current int) picker {
	return picker{projects: projects, current: current, selected: max(0, current)}
}

func (m picker) Init() tea.Cmd { return nil }

func (m picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up":
			m.selected = (m.selected - 1 + len(m.projects)) % len(m.projects)
		case "down":
			m.selected = (m.selected + 1) % len(m.projects)
		case "enter":
			m.chosen = true
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	// Scrolling waits for the size, so the first window ends at the current
	// project.
	if m.height > 0 {
		m.offset = min(max(m.offset, m.selected-m.rows()+1), m.selected, len(m.projects)-m.rows())
	}
	return m, nil
}

// rows is how many projects fit under the box's border, rule and keys.
func (m picker) rows() int {
	return min(len(m.projects), max(1, m.height-4))
}

func (m picker) result() (int, error) {
	if !m.chosen {
		return 0, context.Canceled
	}
	return m.selected, nil
}

func (m picker) View() tea.View {
	if m.chosen || m.cancelled || m.width == 0 {
		return tea.NewView("")
	}

	const current = "  current"
	tag := ""
	if m.current >= 0 {
		tag = current
	}
	org, name := 0, 0
	for _, p := range m.projects {
		org = max(org, lipgloss.Width(cards.Line(p.Organization.Name)))
		name = max(name, lipgloss.Width(cards.Line(p.Name)))
	}
	// The marker and the gap between columns take 4 columns.
	inner := max(1, min(m.width-4, max(4+org+name+len(tag), lipgloss.Width(pickerKeys))))
	if spare := inner - 4 - len(tag); org+name > spare {
		org = max(0, min(org, max(spare/2, spare-name)))
		name = max(0, spare-org)
	}

	rows := m.rows()
	lines := make([]string, 0, rows+2)
	for i := m.offset; i < m.offset+rows; i++ {
		p := m.projects[i]
		cells := pad(ansi.Truncate(cards.Line(p.Organization.Name), org, "…"), org) + "  " +
			pad(ansi.Truncate(cards.Line(p.Name), name, "…"), name)
		mark := strings.Repeat(" ", len(tag))
		if i == m.current {
			mark = current
		}
		if i == m.selected {
			lines = append(lines, "› "+selectedStyle.Render(pad(cells+mark, inner-2)))
		} else {
			lines = append(lines, "  "+cells+faintStyle.Render(mark))
		}
	}
	lines = append(lines, "", pickerKeys)

	label := ""
	if rows < len(m.projects) {
		label = strconv.Itoa(m.selected+1) + "/" + strconv.Itoa(len(m.projects))
	}
	box := panel("Select a project", label, inner+4, rows+4, lines)
	// A rule, not a blank line, separates the keys.
	box[len(box)-3] = faintStyle.Render("├" + strings.Repeat("─", inner+2) + "┤")
	return tea.NewView(strings.Join(box, "\n"))
}
