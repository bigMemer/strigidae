package userinterface

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

const helpKeyWidth = 9

// helpBody lays out the help's scrolling part, one entry per line: global
// bindings on the left and the active view's on the right (stacked when the
// terminal is narrow).
func helpBody(activeTheme theme.Theme, title string, global, viewBindings [][]key.Binding, width int) []string {
	globalColumn := helpColumn(activeTheme, "Global", global)
	viewColumn := helpColumn(activeTheme, title, viewBindings)

	var body string
	switch {
	case viewColumn == "":
		body = globalColumn
	case lipgloss.Width(globalColumn)+lipgloss.Width(viewColumn)+4 <= width:
		body = lipgloss.JoinHorizontal(lipgloss.Top, globalColumn, "    ", viewColumn)
	default:
		body = globalColumn + "\n\n" + viewColumn
	}
	return strings.Split(body, "\n")
}

// helpBodyRows is how many body lines fit under the fixed title and blank
// line; the body gets at least one row, though the window may crop it away
// when the height is under 3.
func helpBodyRows(height int) int { return max(1, height-2) }

// clampHelpOffset keeps the top body line where the last full window ends at
// the last line.
func clampHelpOffset(offset, lineCount, rows int) int {
	return max(0, min(offset, lineCount-rows))
}

// renderHelp draws the title line, a blank line and the window of body lines
// that starts at offset. When the body does not fit, the title line also
// carries the visible range.
func renderHelp(activeTheme theme.Theme, lines []string, height, offset int) string {
	rows := helpBodyRows(height)
	offset = clampHelpOffset(offset, len(lines), rows)
	head := activeTheme.HelpTitle.Render("Help") + activeTheme.Muted.Render("  ? / esc to close")
	if len(lines) > rows {
		last := min(offset+rows, len(lines))
		head += activeTheme.Muted.Render(fmt.Sprintf("  %d\u2013%d of %d", offset+1, last, len(lines)))
	}
	return head + "\n\n" + strings.Join(lines[offset:min(offset+rows, len(lines))], "\n")
}

func helpColumn(activeTheme theme.Theme, heading string, groups [][]key.Binding) string {
	var blocks []string
	for _, bindings := range groups {
		var lines []string
		for _, binding := range bindings {
			help := binding.Help()
			if !binding.Enabled() || help.Desc == "" {
				continue
			}
			pad := strings.Repeat(" ", max(0, helpKeyWidth-lipgloss.Width(help.Key)))
			lines = append(lines, activeTheme.HelpKey.Render(help.Key)+pad+activeTheme.HelpDescription.Render(help.Desc))
		}
		if len(lines) > 0 {
			blocks = append(blocks, strings.Join(lines, "\n"))
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return activeTheme.HelpSection.Render(heading) + "\n" + strings.Join(blocks, "\n\n")
}
