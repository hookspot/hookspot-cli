package cards

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"hookspot/internal/api"
)

// Login is the browser login card. The URL stays outside the box, unwrapped,
// so it can be copied on a machine where no browser opens.
func Login(code, url string, opened bool, width int) string {
	lines := []string{
		box("Log in", "", faintStyle, width, []string{
			faintStyle.Render("Confirm this code in your browser"),
			"",
			"  " + boldStyle.Render(Line(code)),
		}),
		Line(url),
	}
	if !opened {
		lines = append(lines, "Couldn't open a browser; visit the URL above.")
	}
	lines = append(lines, warnStyle.Render("○")+" Waiting for approval… (Ctrl+C to cancel)")
	return strings.Join(lines, "\n")
}

// KeyPrompt asks for a CLI key; the key is typed on the same line.
func KeyPrompt() string {
	return "Enter your hookspot CLI key " + faintStyle.Render("(Account settings > CLI key)") + ": "
}

// Done is a ✓ line: label, then value in bold when there is one.
func Done(label, value string) string {
	line := okStyle.Render("✓") + " " + label
	if value != "" {
		line += " " + boldStyle.Render(Line(value))
	}
	return line
}

// Logout confirms the logout and warns when HOOKSPOT_CLI_KEY still signs
// this shell in.
func Logout(environmentKey bool, width int) string {
	done := Done("Logged out", "")
	if !environmentKey {
		return done
	}
	return done + "\n" + box(warnStyle.Render("Still signed in via env"), "", faintStyle, width, []string{
		"HOOKSPOT_CLI_KEY is set in this shell.",
		"Unset it to finish logging out.",
	})
}

// Update announces a newer release than the running one.
func Update(current, latest string, width int) string {
	return box(warnStyle.Render("Update available"), "", faintStyle, width, []string{
		Line(current) + " → " + boldStyle.Render(Line(strings.TrimPrefix(latest, "v"))),
	})
}

// Projects is the project table, with the active project badged.
func Projects(projects []api.Project, activeUID string, width int) string {
	rows := [][]string{{"UID", "ORGANIZATION", "PROJECT"}}
	for _, project := range projects {
		rows = append(rows, []string{Line(project.UID), Line(project.Organization.Name), Line(project.Name)})
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, cell := range row {
			widths[column] = max(widths[column], lipgloss.Width(cell))
		}
	}
	lines := make([]string, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for column, cell := range row {
			cells[column] = cell + strings.Repeat(" ", widths[column]-lipgloss.Width(cell))
		}
		lines[i] = strings.Join(cells, "  ")
	}

	body := lines[1:]
	for i, project := range projects {
		if activeUID != "" && project.UID == activeUID {
			body[i] += " " + ColorBadge(lipgloss.Green, "active")
		}
	}
	if len(body) == 0 {
		body = []string{faintStyle.Render("No projects")}
	}
	return box("Projects", strconv.Itoa(len(projects)), faintStyle, width, []string{faintStyle.Render(lines[0])}, body)
}

// Error is the fatal error card: the message, then the recovery hint.
func Error(message, hint string, width int) string {
	var lines []string
	for _, line := range textLines(message) {
		lines = append(lines, boldStyle.Render(line))
	}
	if hint != "" {
		lines = append(lines, "")
		lines = append(lines, textLines(hint)...)
	}
	return box(errorStyle.Render("✗ Error"), "", errorStyle, width, lines)
}

// textLines splits sanitized multi-line text for a box, whose width a tab
// would break.
func textLines(text string) []string {
	return strings.Split(strings.ReplaceAll(Sanitize(text), "\t", "    "), "\n")
}

// box frames sections, split by rules, with title in the top border and an
// optional label at its right end. It fits its content up to width columns;
// longer lines wrap. Lines must not contain newlines or tabs.
func box(title, label string, border lipgloss.Style, width int, sections ...[]string) string {
	inner := lipgloss.Width(title) + 3
	if label != "" {
		inner += lipgloss.Width(label) + 2
		label = border.Render(label)
	}
	parts := make([]section, len(sections))
	for i, lines := range sections {
		parts[i] = section{lines: lines}
		for _, line := range lines {
			inner = max(inner, lipgloss.Width(line))
		}
	}
	return frame(border, max(1, min(inner, width-4)), title, label, "", parts...)
}

// section is a run of box lines; its title sits in the rule above it.
type section struct {
	title string
	lines []string
}

// frame draws a box inner columns wide. title and label sit at the ends of
// the top border, footer at the right end of the bottom one; title and footer
// are cut to fit, and a label too wide for the box overflows it. Every section
// after the first opens with a rule. Lines wrap; they must not contain
// newlines or tabs.
func frame(border lipgloss.Style, inner int, title, label, footer string, sections ...section) string {
	var out strings.Builder
	end := border.Render("─╮")
	if label != "" {
		end = " " + label + border.Render(" ─╮")
	}
	title = truncate(title, inner-lipgloss.Width(end)-1)
	out.WriteString(border.Render("╭─ ") + title + border.Render(" "+strings.Repeat("─", max(0, inner-lipgloss.Width(title)-lipgloss.Width(end)))) + end + "\n")
	side := border.Render("│")
	for i, section := range sections {
		if i > 0 && section.title != "" {
			title := truncate(section.title, inner-3)
			out.WriteString(border.Render("├─ ") + title + border.Render(" "+strings.Repeat("─", max(0, inner-lipgloss.Width(title)-2))+"─┤") + "\n")
		} else if i > 0 {
			out.WriteString(border.Render("├"+strings.Repeat("─", inner+2)+"┤") + "\n")
		}
		for _, line := range section.lines {
			for _, wrapped := range strings.Split(lipgloss.Wrap(line, inner, ""), "\n") {
				padding := strings.Repeat(" ", max(0, inner-lipgloss.Width(wrapped)))
				out.WriteString(side + " " + wrapped + padding + " " + side + "\n")
			}
		}
	}
	if footer == "" {
		out.WriteString(border.Render("╰" + strings.Repeat("─", inner+2) + "╯"))
		return out.String()
	}
	footer = truncate(footer, inner-2)
	out.WriteString(border.Render("╰"+strings.Repeat("─", max(0, inner-lipgloss.Width(footer)-1))+" ") + footer + border.Render(" ─╯"))
	return out.String()
}

// truncate cuts s, which may be styled, to width columns, ending it with "…"
// when cut.
func truncate(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 1 {
		return strings.Repeat("…", max(0, width))
	}
	return lipgloss.NewStyle().MaxWidth(width-1).Render(s) + "…"
}
