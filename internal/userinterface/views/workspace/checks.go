package workspace

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/picker"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// pickerLabelWidth is the widest label that fits the picker's popup in a
// terminal width cells wide: the popup is at most 64 cells, and its border,
// cursor and padding take 6.
func pickerLabelWidth(width int) int { return min(64, width) - 6 }

// sortedChecks orders checks failing first, then running or queued, then
// passing, then skipped, neutral or cancelled, alphabetically within each group. It
// returns a copy.
func sortedChecks(checks []github.Check) []github.Check {
	out := slices.Clone(checks)
	slices.SortStableFunc(out, func(first, second github.Check) int {
		if group := int(first.Result.Group()) - int(second.Result.Group()); group != 0 {
			return group
		}
		return strings.Compare(strings.ToLower(first.Name), strings.ToLower(second.Name))
	})
	return out
}

// checkGlyph returns the one-cell mark of a check and its style. The colours
// are the pull request list's checks styles; skipped and neutral are muted.
func checkGlyph(activeTheme theme.Theme, result github.CheckResult) (string, lipgloss.Style) {
	styles := activeTheme.PullRequestList
	switch result.Group() {
	case github.CheckGroupFailing:
		return "✗", styles.ChecksFailing
	case github.CheckGroupPending:
		if result == github.CheckQueued {
			return "○", styles.ChecksRunning
		}
		return "●", styles.ChecksRunning
	case github.CheckGroupPassing:
		return "✓", styles.ChecksPassing
	case github.CheckGroupInactive:
	}
	return "-", activeTheme.Muted
}

// checkNameWidth is the width every name in rows is padded or cut to: the
// longest name, but no more than leaves room for the glyph, the longest state
// word and the gaps within width.
func checkNameWidth(checks []github.Check, width int) int {
	longest, longestWord := 0, 0
	for _, check := range checks {
		longest = max(longest, ansi.StringWidth(terminaltext.Clean(check.Name)))
		longestWord = max(longestWord, ansi.StringWidth(check.Result.Word()))
	}
	return max(1, min(longest, width-2-2-longestWord))
}

// checkName cleans name and pads or cuts it to exactly width cells.
func checkName(name string, width int) string {
	name = terminaltext.Clean(name)
	if ansi.StringWidth(name) > width {
		name = ansi.Truncate(name, width, "…")
	}
	return padRight(name, width)
}

// checkLabel is the plain text of a check row: glyph, name and state in
// words, with names padded to nameWidth so the words line up. The picker lists
// it as is.
func checkLabel(activeTheme theme.Theme, check github.Check, nameWidth int) string {
	glyph, _ := checkGlyph(activeTheme, check.Result)
	return glyph + " " + checkName(check.Name, nameWidth) + "  " + check.Result.Word()
}

// checkRow is a styled Overview row; see checkLabel.
func checkRow(activeTheme theme.Theme, check github.Check, nameWidth int) string {
	glyph, style := checkGlyph(activeTheme, check.Result)
	return style.Render(glyph) + " " + activeTheme.Normal.Render(checkName(check.Name, nameWidth)) + "  " + style.Render(check.Result.Word())
}

// checkSectionLines are the Overview's Checks section: heading and rows.
func checkSectionLines(activeTheme theme.Theme, detail *github.PullRequestDetail, width int) []string {
	total := max(detail.CheckTotal, len(detail.CheckList))
	lines := []string{activeTheme.SectionHeading.Render(fmt.Sprintf("Checks (%d)", total))}
	if total == 0 {
		return append(lines, activeTheme.Muted.Render("No checks."))
	}
	nameWidth := checkNameWidth(detail.CheckList, width)
	for _, check := range sortedChecks(detail.CheckList) {
		lines = append(lines, checkRow(activeTheme, check, nameWidth))
	}
	if more := total - len(detail.CheckList); more > 0 {
		lines = append(lines, activeTheme.Muted.Render(fmt.Sprintf("and %d more", more)))
	}
	return lines
}

// checksCommand handles :checks: it opens a picker of the same rows.
func (view *View) checksCommand() tea.Cmd {
	switch {
	case view.detail == nil:
		return userinterface.Info("pull request not loaded yet")
	case len(view.detail.CheckList) == 0:
		return userinterface.Info("No checks.")
	case view.picker != nil || view.review.chooser != nil:
		return nil
	}
	checks := sortedChecks(view.detail.CheckList)
	items := make([]string, len(checks))
	nameWidth := checkNameWidth(checks, pickerLabelWidth(view.width))
	for i, check := range checks {
		items[i] = checkLabel(view.theme, check, nameWidth)
	}
	view.pickerChecks = checks
	view.picker = picker.New(view.theme, "Checks", items)
	if more := max(view.detail.CheckTotal, len(checks)) - len(checks); more > 0 {
		view.picker.SetNote(fmt.Sprintf("and %d more", more))
	}
	return nil
}

// openCheck opens a check's details page in the browser, the way `o` opens
// the pull request. Only http and https links are opened: a commit status's
// target URL is set by whoever posts the status.
func (view *View) openCheck(check github.Check) tea.Cmd {
	address, err := url.Parse(check.URL)
	if check.URL == "" || err != nil || (address.Scheme != "https" && address.Scheme != "http") {
		return userinterface.Info("this check has no link to open")
	}
	return view.openAddress(check.URL)
}
