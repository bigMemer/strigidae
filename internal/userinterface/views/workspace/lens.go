package workspace

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/analysis"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// lensPane is the lens open in the left pane, in place of the Files list:
// the lens's segments in reading order, each with its spans. Moving between
// spans moves the diff pane to them.
type lensPane struct {
	identifier string // the lens's id, so a reloaded analysis finds it again
	stops      []lensStop
	cursor     int // index into stops
	top        int // first drawn row
}

// lensStop is one span of one segment: a span shared by two segments is a
// stop in each.
type lensStop struct {
	segment int // index into the lens's Segments
	span    string
}

// lensOpen reports whether the left pane shows a lens.
func (view *View) lensOpen() bool { return view.lens != nil }

// currentLens returns the open lens, or false when none is open or the
// analysis no longer has it.
func (view *View) currentLens() (analysis.Lens, bool) {
	loaded := view.analysis()
	if view.lens == nil || loaded == nil {
		return analysis.Lens{}, false
	}
	index := slices.IndexFunc(loaded.content.Lenses, func(lens analysis.Lens) bool { return lens.Identifier == view.lens.identifier })
	if index < 0 {
		return analysis.Lens{}, false
	}
	return loaded.content.Lenses[index], true
}

// readingOrder lists a lens's stops: segments in the lens's order then the
// rest, and in each segment its spans in the segment's order then the rest.
func readingOrder(lens analysis.Lens) []lensStop {
	var stops []lensStop
	for _, segment := range ordered(len(lens.Segments), lens.Order, func(i int) string { return lens.Segments[i].Identifier }) {
		spans := lens.Segments[segment].Spans
		for _, span := range ordered(len(spans), lens.Segments[segment].Order, func(i int) string { return spans[i] }) {
			stops = append(stops, lensStop{segment: segment, span: spans[span]})
		}
	}
	return stops
}

// ordered returns the indexes 0..count-1, those named in order first (in
// that order), then the others in their own order.
func ordered(count int, order []string, identifier func(int) string) []int {
	var indexes []int
	for _, name := range order {
		for i := range count {
			if identifier(i) == name && !slices.Contains(indexes, i) {
				indexes = append(indexes, i)
			}
		}
	}
	for i := range count {
		if !slices.Contains(indexes, i) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// lensCommand is :lens [id]: with an id it opens that lens; without one it
// closes the open lens, or opens the first.
func (view *View) lensCommand(arguments string) tea.Cmd {
	loaded := view.analysis()
	if loaded == nil {
		return userinterface.Info("no analysis loaded: :analysis <path> loads one")
	}
	identifier := strings.TrimSpace(arguments)
	if identifier == "" {
		if view.lensOpen() {
			view.closeLens()
			return nil
		}
		identifier = loaded.content.Lenses[0].Identifier
	}
	index := slices.IndexFunc(loaded.content.Lenses, func(lens analysis.Lens) bool { return lens.Identifier == identifier })
	if index < 0 {
		var names []string
		for _, lens := range loaded.content.Lenses {
			names = append(names, lens.Identifier)
		}
		return userinterface.Info(terminaltext.Clean(fmt.Sprintf("no lens %q; lenses: %s", identifier, strings.Join(names, ", "))))
	}
	view.openLens(index)
	view.focus = paneFiles
	view.layout()
	return nil
}

// openLens shows lens index of the analysis, at its first span.
func (view *View) openLens(index int) {
	lens := view.analysis().content.Lenses[index]
	view.lens = &lensPane{identifier: lens.Identifier, stops: readingOrder(lens)}
	view.showStop()
}

// stepLens is gt and gT: the next or previous lens, wrapping around.
func (view *View) stepLens(delta int) tea.Cmd {
	loaded := view.analysis()
	if loaded == nil || !view.lensOpen() {
		return nil
	}
	lenses := loaded.content.Lenses
	current := slices.IndexFunc(lenses, func(lens analysis.Lens) bool { return lens.Identifier == view.lens.identifier })
	view.openLens(((current+delta)%len(lenses) + len(lenses)) % len(lenses))
	view.layout()
	return nil
}

func (view *View) closeLens() {
	view.lens = nil
	view.filePane.SetSpan(diffview.Range{})
	view.layout()
}

// stepSpan is :next-span and :previous-span.
func (view *View) stepSpan(delta int) tea.Cmd {
	if !view.lensOpen() {
		return userinterface.Info("no lens open: :lens opens one")
	}
	view.moveLensCursor(view.lens.cursor + delta)
	return nil
}

func (view *View) moveLensCursor(cursor int) {
	cursor = max(0, min(cursor, len(view.lens.stops)-1))
	if cursor != view.lens.cursor {
		view.lens.cursor = cursor
		view.showStop()
	}
}

// lensKey moves through the lens with the Files pane's movement keys.
func (view *View) lensKey(message keys.Message) {
	switch {
	case key.Matches(message, view.globalKeys.Down):
		view.moveLensCursor(view.lens.cursor + message.Repeat())
	case key.Matches(message, view.globalKeys.Up):
		view.moveLensCursor(view.lens.cursor - message.Repeat())
	case key.Matches(message, view.globalKeys.Top):
		view.moveLensCursor(0)
	case key.Matches(message, view.globalKeys.Bottom):
		view.moveLensCursor(len(view.lens.stops) - 1)
	case key.Matches(message, view.globalKeys.Select):
		view.focus = paneRight
	}
}

// showStop opens the stop's file in the diff pane at the span's first line.
// A span in a file the pull request does not change leaves the diff pane as
// it is; its row says so.
func (view *View) showStop() {
	span, ok := view.stopSpan(view.lens.cursor)
	if !ok {
		return
	}
	view.filePane.SetSpan(diffview.Range{Path: span.Path, Side: span.Side, Start: span.Start, End: span.End})
	entry := view.fileEntry(span.Path)
	if entry == 0 {
		return
	}
	view.selectEntry(entry)
	view.filePane.GotoLine(span.Side, span.Start)
}

// stopSpan returns the span of stop i.
func (view *View) stopSpan(i int) (analysis.Span, bool) {
	loaded := view.analysis()
	if loaded == nil || view.lens == nil || i < 0 || i >= len(view.lens.stops) {
		return analysis.Span{}, false
	}
	span, ok := loaded.content.Spans[view.lens.stops[i].span]
	return span, ok
}

// fileEntry is the Files entry of path (1 for the first file), or 0 when the
// pull request does not change it.
func (view *View) fileEntry(path string) int {
	return slices.IndexFunc(view.files(), func(file github.ChangedFile) bool { return file.Path == path }) + 1
}

// analysisChanged keeps an open lens in step with a reloaded analysis: it
// closes when the lens is gone, else its stops are rebuilt.
func (view *View) analysisChanged() {
	if view.lens == nil {
		return
	}
	lens, ok := view.currentLens()
	if !ok {
		view.closeLens()
		return
	}
	view.lens.stops = readingOrder(lens)
	view.lens.cursor = min(view.lens.cursor, len(view.lens.stops)-1)
}

// spanPlace says where a span is relative to the diff on screen.
type spanPlace int

const (
	spanInDiff      spanPlace = iota
	spanOutsideDiff           // the file changed, but no hunk shows these lines
	spanUnchanged             // the pull request does not change the file
)

func (view *View) spanPlace(span analysis.Span) spanPlace {
	if view.fileEntry(span.Path) == 0 {
		return spanUnchanged
	}
	if view.diff.Parsed == nil {
		return spanInDiff // not loaded yet: claim nothing
	}
	file := view.diff.Parsed.File(span.Path)
	if file == nil {
		return spanOutsideDiff
	}
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			number := line.New
			if span.Side == github.SideLeft {
				number = line.Old
			}
			if number >= span.Start && number <= span.End {
				return spanInDiff
			}
		}
	}
	return spanOutsideDiff
}

// lensTitle is the left pane's title while a lens is open.
func (view *View) lensTitle() string {
	loaded := view.analysis()
	lens, ok := view.currentLens()
	if !ok {
		return "Lens"
	}
	index := slices.IndexFunc(loaded.content.Lenses, func(candidate analysis.Lens) bool { return candidate.Identifier == lens.Identifier })
	title := fmt.Sprintf("Lens %d/%d: %s", index+1, len(loaded.content.Lenses), terminaltext.Clean(lens.Title))
	if view.analysisStale() {
		title += " (stale)"
	}
	return title
}

// lensRows draws the open lens in width columns and height rows, scrolled so
// the cursor's span is shown.
func (view *View) lensRows(width, height int) []string {
	lens, ok := view.currentLens()
	if !ok {
		return []string{view.theme.Muted.Render("The lens is gone from the analysis.")}
	}
	var rows []string
	// cursorRow is the cursor's span; anchorRow is where its segment starts
	// (the top, for the first segment: the lens's purpose belongs to it), kept
	// in view with it when they fit.
	cursorRow, anchorRow, segmentRow := 0, 0, 0
	wrap := func(text string, indent int) []string {
		var lines []string
		for _, line := range strings.Split(ansi.Wrap(terminaltext.Clean(text), max(1, width-indent), ""), "\n") {
			lines = append(lines, view.theme.Muted.Render(strings.Repeat(" ", indent)+line))
		}
		return lines
	}
	if lens.Purpose != "" {
		rows = append(rows, wrap(lens.Purpose, 0)...)
	}
	previousSegment := -1
	for i, stop := range view.lens.stops {
		if stop.segment != previousSegment {
			previousSegment = stop.segment
			segment := lens.Segments[stop.segment]
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			if i > 0 {
				segmentRow = len(rows)
			}
			rows = append(rows, view.segmentHeader(segment, width))
			if segment.Annotation != "" {
				rows = append(rows, wrap(segment.Annotation, 2)...)
			}
		}
		if i == view.lens.cursor {
			cursorRow, anchorRow = len(rows), segmentRow
		}
		rows = append(rows, view.stopRow(i, width))
	}
	view.lens.top = scrolledTop(view.lens.top, cursorRow, len(rows), height)
	if anchorRow < view.lens.top && cursorRow-anchorRow < height {
		view.lens.top = anchorRow
	}
	return rows[view.lens.top:min(len(rows), view.lens.top+height)]
}

// scrolledTop keeps row cursor within height rows starting at top.
func scrolledTop(top, cursor, count, height int) int {
	if height <= 0 {
		return 0
	}
	if cursor < top {
		top = cursor
	}
	if cursor >= top+height {
		top = cursor - height + 1
	}
	return max(0, min(top, count-height))
}

func (view *View) segmentHeader(segment analysis.Segment, width int) string {
	header := view.theme.StrongText.Render(terminaltext.Clean(segment.Title))
	switch segment.Risk {
	case analysis.RiskHigh:
		header += " " + view.theme.FileDeleted.Render("high risk")
	case analysis.RiskMedium:
		header += " " + view.theme.FileModified.Render("medium risk")
	case analysis.RiskLow:
		header += " " + view.theme.Muted.Render("low risk")
	}
	return ansi.Truncate(header, width, "…")
}

// stopRow is one span: file name and lines, its label, and where it is.
func (view *View) stopRow(i, width int) string {
	span, ok := view.stopSpan(i)
	if !ok {
		return ""
	}
	lines := strconv.Itoa(span.Start)
	if span.End != span.Start {
		lines += "–" + strconv.Itoa(span.End)
	}
	text := terminaltext.Clean(path.Base(span.Path)) + ":" + lines
	if span.Side == github.SideLeft {
		text += " (old)"
	}
	if span.Label != "" {
		text += " " + terminaltext.Clean(span.Label)
	}
	switch view.spanPlace(span) {
	case spanOutsideDiff:
		text += " · not in the diff"
	case spanUnchanged:
		text += " · file not changed"
	case spanInDiff:
	}
	if i == view.lens.cursor {
		return view.theme.Selected.Render(ansi.Truncate("> "+text, width, "…"))
	}
	return view.theme.Normal.Render(ansi.Truncate("  "+text, width, "…"))
}
