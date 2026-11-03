package pullrequestlist

import (
	"fmt"
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
	view.width, view.height = width, height
	current := view.current()
	current.window.SetRows(len(current.pullRequests))
	current.window.SetSize(view.listHeight())

	var out string
	switch {
	case len(current.pullRequests) == 0 && current.loading:
		out = view.theme.Muted.Render(fmt.Sprintf("Loading pull requests in %s...", view.repositoryName()))
	case len(current.pullRequests) == 0 && current.err != nil:
		out = view.errorState(width)
	case len(current.pullRequests) == 0:
		out = view.theme.Muted.Render(view.emptyText())
	default:
		out = view.table(width, height)
	}
	return view.overlayPicker(fit(view.tabBar(width)+"\n"+out, width, height), width, height)
}

// tabBar draws the tab names.
func (view *View) tabBar(width int) string {
	names := make([]string, len(view.tabs))
	for i, current := range view.tabs {
		names[i] = current.name
	}
	return tabbar.Render(view.theme, names, view.active, width)
}

// emptyText says why the shown tab has no rows.
func (view *View) emptyText() string {
	current := view.current()
	if current.author == "" {
		return fmt.Sprintf("no open pull requests in %s", view.repositoryName())
	}
	return fmt.Sprintf("no open pull requests by %s in %s", current.author, view.repositoryName())
}

// fit clips s to at most height lines of width columns, however narrow the
// terminal: layouts have minimum widths that can exceed what we are given.
func fit(text string, width, height int) string {
	lines := strings.Split(text, "\n")
	lines = lines[:min(len(lines), height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

func (view *View) errorState(width int) string {
	head := view.theme.PullRequestList.ChangesRequested.Render("Could not load pull requests")
	body := ansi.Wrap(terminaltext.SafeError(view.current().err).Error(), width, "")
	return head + "\n" + body + "\n\n" + view.theme.Muted.Render("Press R to retry.")
}

func (view *View) table(width, height int) string {
	current := view.current()
	table := pullrequestrows.New(view.theme, view.now, pullrequestrows.Options{Branches: true})
	layout := table.Layout(current.pullRequests, width-pullrequestrows.MarkerColumns)
	lines := make([]string, 0, height)
	lines = append(lines, table.Header(layout, width))

	end := min(len(current.pullRequests), current.window.Top()+view.listHeight())
	for i := current.window.Top(); i < end; i++ {
		lines = append(lines, table.IndentedRow(layout, current.pullRequests[i], current.depths[i], i == current.window.Cursor(), view.options.HighlightSearch() && current.search.Marked(i), width))
	}
	return strings.Join(lines, "\n")
}
