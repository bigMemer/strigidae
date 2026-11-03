package dashboard

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/userinterface/components/pullrequestrows"
	"github.com/MikePors/strigidae/internal/userinterface/components/tabbar"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Render implements userinterface.View.
func (view *View) Render(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	view.height = height
	if len(view.tabs) == 0 {
		return clip(view.theme.Muted.Render("no tabs configured"), width, height)
	}
	current := &view.tabs[view.active]
	table := pullrequestrows.New(view.theme, view.now, pullrequestrows.Options{Repository: true})
	layout := table.Layout(current.pullRequests(), width-pullrequestrows.MarkerColumns)

	selectedIndex := current.selectedIndex()
	lines, selectedLine := current.lines(selectedIndex)
	current.follow(lines, selectedIndex, selectedLine, view.listHeight())

	out := []string{view.tabBar(width), table.Header(layout, width)}
	end := min(len(lines), current.window.Top()+view.listHeight())
	for _, entry := range lines[current.window.Top():end] {
		out = append(out, view.drawLine(table, layout, current, entry, selectedIndex, width))
	}
	return clip(strings.Join(out, "\n"), width, height)
}

// tabBar draws the tab names.
func (view *View) tabBar(width int) string {
	names := make([]string, len(view.tabs))
	for i, current := range view.tabs {
		names[i] = current.name
	}
	return tabbar.Render(view.theme, names, view.active, width)
}

// drawLine draws one line of the scrolling area.
func (view *View) drawLine(table pullrequestrows.Table, layout pullrequestrows.Layout, current *tab, entry line, selectedIndex, width int) string {
	target := current.sections[entry.section]
	switch entry.kind {
	case headingLine:
		return view.heading(target, width)
	case noteLine:
		return view.note(target, width)
	default:
		return table.Row(layout, target.pullRequests[entry.row], entry.index == selectedIndex, view.options.HighlightSearch() && current.search.Marked(entry.index), width)
	}
}

func (view *View) heading(target section, width int) string {
	title := view.theme.SectionHeading.Render(terminaltext.Clean(target.name))
	if target.err != nil && len(target.pullRequests) > 0 {
		title += " " + view.theme.ErrorText.Render("(refresh failed: "+terminaltext.SafeError(target.err).Error()+")")
	}
	query := "  " + view.theme.Muted.Render(terminaltext.Clean(target.query))
	return ansi.Truncate(title+query, width, "…")
}

func (view *View) note(target section, width int) string {
	switch {
	case target.loading:
		return view.theme.Muted.Render("  Loading...")
	case target.err != nil:
		message := terminaltext.SafeError(target.err).Error()
		return view.theme.ErrorText.Render(ansi.Truncate("  Could not load: "+message, width, "…"))
	default:
		return view.theme.Muted.Render("  No pull requests")
	}
}

// clip cuts text to at most height lines of width columns.
func clip(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	lines = lines[:min(len(lines), height)]
	for i, current := range lines {
		lines[i] = ansi.Truncate(current, width, "")
	}
	return strings.Join(lines, "\n")
}
