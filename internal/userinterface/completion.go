package userinterface

import (
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/options"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
	"github.com/MikePors/strigidae/internal/userinterface/theme/scheme"
)

const (
	matchSeparator = "  "
	matchCut       = "…"
	// matchCutWidth is a cut marker and the space between it and the matches.
	matchCutWidth = 2
)

// completion is the list of matches Tab is cycling through on the command
// line. It exists only while there are two or more matches and nothing but
// Tab and shift+tab has been pressed since.
type completion struct {
	before  string // the line up to the word being completed
	matches []string
	index   int // the match the line shows
}

// commandNames lists the commands that work now: the shell's own, in table
// order, then the top view's, in the order it declares them.
func commandNames(top View) []string {
	names := CommandNames()
	if lister, ok := top.(CommandLister); ok {
		names = append(names, lister.CommandNames()...)
	}
	return names
}

// optionWords lists what `:set` accepts as a word to turn an option on or off.
func optionWords() []string {
	var words []string
	for _, option := range options.All() {
		words = append(words, option.Name)
	}
	for _, option := range options.All() {
		words = append(words, "no"+option.Name)
	}
	return words
}

// completionCandidates finds the word at the end of line that Tab completes,
// and what it may become. before is the line up to that word. ok is false
// where Tab completes nothing.
func (model *Model) completionCandidates(line string) (before string, candidates []string, ok bool) {
	leading, name, gap, arguments, hasArguments := splitCommandLine(line)
	if !hasArguments {
		return leading, commandNames(model.top()), true
	}
	switch name {
	case "set":
		start := strings.LastIndexByte(arguments, ' ') + 1
		return leading + name + gap + arguments[:start], optionWords(), true
	case "theme":
		if strings.Contains(arguments, " ") {
			return "", nil, false
		}
		return leading + name + gap, scheme.Names(), true
	}
	return "", nil, false
}

// complete is Tab (step 1) and shift+tab (step -1) on the command line. The
// keys never reach the text input, so no tab character is typed.
func (model *Model) complete(step int) {
	commandLine := &model.commandLine
	if current := commandLine.completion; current != nil {
		current.index = (current.index + step + len(current.matches)) % len(current.matches)
		commandLine.fill(current.before + current.matches[current.index])
		return
	}
	line := commandLine.input.Value()
	if commandLine.input.Position() != utf8.RuneCountInString(line) {
		return
	}
	before, candidates, ok := model.completionCandidates(line)
	if !ok {
		return
	}
	word := strings.TrimPrefix(line, before)
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, word) && !slices.Contains(matches, candidate) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return
	}
	index := 0
	if step < 0 {
		index = len(matches) - 1
	}
	commandLine.fill(before + matches[index])
	if len(matches) > 1 {
		commandLine.completion = &completion{before: before, matches: matches, index: index}
	}
}

// fill replaces the line with text and puts the cursor at its end.
func (commandLine *commandLine) fill(text string) {
	commandLine.input.SetValue(text)
	commandLine.input.CursorEnd()
}

// overlayCompletion draws the match list over the last row of the content
// while there is one.
func (model *Model) overlayCompletion(content string) string {
	current := model.commandLine.completion
	if current == nil || content == "" {
		return content
	}
	rows := strings.Split(content, "\n")
	rows[len(rows)-1] = renderMatches(model.theme, current.matches, current.index, model.width)
	return strings.Join(rows, "\n")
}

// renderMatches draws the matches on one line of exactly width columns, the
// current one highlighted. When they do not fit, the window of matches around
// the current one that does is drawn, with "…" at each cut end; a current match
// wider than the line is itself cut.
func renderMatches(activeTheme theme.Theme, matches []string, index, width int) string {
	widths := make([]int, len(matches))
	for i, match := range matches {
		widths[i] = lipgloss.Width(match)
	}
	low, high := matchWindow(widths, index, width)
	line := activeTheme.StatusBar.Width(width).MaxWidth(width)
	if matchCost(widths, low, high) > width {
		return line.Render(activeTheme.StatusContext.Render(ansi.Truncate(matches[index], width, matchCut)))
	}
	var text strings.Builder
	if low > 0 {
		text.WriteString(activeTheme.CommandHint.Render(matchCut + " "))
	}
	for i := low; i <= high; i++ {
		if i > low {
			text.WriteString(activeTheme.StatusBar.Render(matchSeparator))
		}
		style := activeTheme.CommandHint
		if i == index {
			style = activeTheme.StatusContext
		}
		text.WriteString(style.Render(matches[i]))
	}
	if high < len(matches)-1 {
		text.WriteString(activeTheme.CommandHint.Render(" " + matchCut))
	}
	return line.Render(text.String())
}

// matchCost is the width of matches low to high with their separators and cut
// markers.
func matchCost(widths []int, low, high int) int {
	cost := lipgloss.Width(matchSeparator) * (high - low)
	for i := low; i <= high; i++ {
		cost += widths[i]
	}
	if low > 0 {
		cost += matchCutWidth
	}
	if high < len(widths)-1 {
		cost += matchCutWidth
	}
	return cost
}

// matchWindow grows a window around index, to the right first, for as long as
// it fits in width. The window is just index when even that does not fit.
func matchWindow(widths []int, index, width int) (low, high int) {
	low, high = index, index
	for {
		switch {
		case high+1 < len(widths) && matchCost(widths, low, high+1) <= width:
			high++
		case low > 0 && matchCost(widths, low-1, high) <= width:
			low--
		default:
			return low, high
		}
	}
}
