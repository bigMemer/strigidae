package workspace

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// Layout constants.
const (
	narrowBelow  = 80 // below this width only one pane is shown
	headerRows   = 2
	filesMinimum = 26
	filesMaximum = 44
)

// panesHeight is the height available to the panes below the header.
func (view *View) panesHeight() int { return max(0, view.height-headerRows) }

// paneWidths returns the outer widths of the Files and right panes. In a
// narrow terminal the unfocused pane gets zero width.
func (view *View) paneWidths() (files, right int) {
	if view.width < narrowBelow {
		if view.focus == paneFiles {
			return view.width, 0
		}
		return 0, view.width
	}
	files = min(max(view.width*30/100, filesMinimum), filesMaximum)
	return files, view.width - files
}

// layout sizes the overview viewport and keeps the Files selection in view.
// It runs on resize, selection change and render.
func (view *View) layout() {
	filesWidth, rightWidth := view.paneWidths()
	height := max(0, view.panesHeight()-2)
	view.sizeFilePane(filesWidth, rightWidth, height)
	if rightWidth > 0 {
		view.overview.ensure(view.theme, view.markdownRenderer, view.detail, threadsView{view.threads, view.threadLoaded, view.threadPending, view.threadErr}, view.now(), max(1, rightWidth-4), height)
	}
	view.window.SetRows(len(view.rows))
	view.window.SetSize(height)
}

// sizeFilePane tells the file pane its inner size, also while it is hidden in
// a narrow terminal, whenever that size changes.
func (view *View) sizeFilePane(filesWidth, rightWidth, height int) {
	if rightWidth == 0 {
		rightWidth = view.width // the pane would get the whole width once shown
		if filesWidth > 0 && view.width >= narrowBelow {
			rightWidth = view.width - filesWidth
		}
	}
	width := max(0, rightWidth-2)
	if width != view.paneWidth || height != view.paneHeight {
		view.paneWidth, view.paneHeight = width, height
		view.filePane.SetSize(width, height)
	}
}

// Render implements userinterface.View.
func (view *View) Render(width, height int) string {
	view.width, view.height = width, height
	if width < 4 || height < headerRows+3 {
		return ""
	}
	view.layout()
	switch {
	case view.detail == nil && view.err != nil:
		return view.renderFailure()
	case view.detail == nil:
		return view.theme.Title.Render(view.Title()) + "\n" +
			view.theme.Muted.Render(terminaltext.Clean(fmt.Sprintf("Loading %s#%d…", view.repository, view.number)))
	}

	filesWidth, rightWidth := view.paneWidths()
	lineHeight := view.panesHeight()
	var left, right []string
	switch {
	case filesWidth > 0 && view.lensOpen():
		left = drawPane(view, view.lensTitle(), view.lensRows(filesWidth-2, lineHeight-2), filesWidth, lineHeight, view.focus == paneFiles)
	case filesWidth > 0:
		left = drawPane(view, view.filesTitle(), view.fileRows(filesWidth-2, lineHeight-2), filesWidth, lineHeight, view.focus == paneFiles)
	}
	if rightWidth > 0 {
		right = drawPane(view, view.rightTitle(rightWidth-5), view.rightRows(rightWidth-2, lineHeight-2), rightWidth, lineHeight, view.focus == paneRight)
	}
	rows := make([]string, 0, height)
	rows = append(rows, view.renderHeader(width)...)
	for i := range lineHeight {
		var leftLine, rightLine string
		if left != nil {
			leftLine = left[i]
		}
		if right != nil {
			rightLine = right[i]
		}
		rows = append(rows, leftLine+rightLine)
	}
	if view.picker != nil {
		overlay(rows, view.picker.Render(width, height-headerRows), width, headerRows)
	}
	if view.review.chooser != nil {
		overlay(rows, view.review.chooser.Render(width, height-headerRows), width, headerRows)
	}
	return strings.Join(rows, "\n")
}

func (view *View) renderFailure() string {
	head := view.theme.Title.Render(view.Title())
	if view.notFound {
		return head + "\n" +
			view.theme.ErrorText.Render(terminaltext.Clean(fmt.Sprintf("Pull request %s#%d was not found.", view.repository, view.number))) + "\n" +
			view.theme.Muted.Render("It may not exist, or you may lack access. Press R to retry.")
	}
	return head + "\n" +
		view.theme.ErrorText.Render("Could not load the pull request.") + "\n" +
		view.theme.Normal.Render(ansi.Wrap(terminaltext.SafeError(view.err).Error(), max(1, view.width), "")) + "\n" +
		view.theme.Muted.Render("Press R to retry.")
}

func (view *View) renderHeader(width int) []string {
	detail := view.detail
	badge := view.stateBadge()
	line1 := badge + " " + view.theme.Title.Render(fmt.Sprintf("#%d", detail.Number)) + " " + view.theme.StrongText.Render(terminaltext.Clean(detail.Title))

	separator := view.theme.Muted.Render(" · ")
	parts := []string{
		view.theme.Author.Render(terminaltext.Clean(detail.Author)),
	}
	switch detail.Checks {
	case github.ChecksPassing:
		parts = append(parts, view.theme.PullRequestList.ChecksPassing.Render("✓ checks passing"))
	case github.ChecksFailing:
		parts = append(parts, view.theme.PullRequestList.ChecksFailing.Render("✗ checks failing"))
	case github.ChecksRunning:
		parts = append(parts, view.theme.PullRequestList.ChecksRunning.Render("● checks running"))
	case github.ChecksNone:
	}
	switch detail.ReviewDecision {
	case github.ReviewDecisionApproved:
		parts = append(parts, view.theme.FileAdded.Render("approved"))
	case github.ReviewDecisionChangesRequested:
		parts = append(parts, view.theme.FileDeleted.Render("changes requested"))
	case github.ReviewDecisionReviewRequired:
		parts = append(parts, view.theme.FileModified.Render("review required"))
	case github.ReviewDecisionNone:
	}
	// Only open pull requests show what stands in the way of merging.
	if detail.State == github.PullRequestStateOpen {
		switch detail.MergeState {
		case github.MergeStateConflicts:
			parts = append(parts, view.theme.FileDeleted.Render("conflicts"))
		case github.MergeStateBehind:
			parts = append(parts, view.theme.FileModified.Render("behind base"))
		case github.MergeStateBlocked:
			parts = append(parts, view.theme.FileModified.Render("blocked"))
		case github.MergeStateOther:
		}
	}
	// Branches are the least glanceable part, so they come after the states
	// and are what truncation eats first.
	parts = append(parts, view.theme.Muted.Render(terminaltext.Clean(detail.BaseBranch)+" ← "+terminaltext.Clean(detail.HeadBranch)))
	if len(detail.Labels) > 0 {
		names := make([]string, len(detail.Labels))
		for i, label := range detail.Labels {
			names[i] = "[" + terminaltext.Clean(label.Name) + "]"
		}
		parts = append(parts, view.theme.Normal.Render(strings.Join(names, " ")))
	}
	if entries := reviewerEntries(detail); len(entries) > 0 {
		parts = append(parts, reviewersText(view.theme, entries))
	}
	line2 := strings.Join(parts, separator)
	return []string{ansi.Truncate(line1, width, "…"), ansi.Truncate(line2, width, "…")}
}

func (view *View) stateBadge() string {
	detail := view.detail
	switch {
	case detail.State == github.PullRequestStateMerged:
		return view.theme.StateMerged.Render("merged")
	case detail.State == github.PullRequestStateClosed:
		return view.theme.StateClosed.Render("closed")
	case detail.Draft:
		return view.theme.StateDraft.Render("draft")
	default:
		return view.theme.StateOpen.Render("open")
	}
}

// rightTitle is the right pane's title in at most width cells (drawPane
// truncates anything longer). A long path is shortened from the left, so the
// hunk progress after it stays visible.
func (view *View) rightTitle(width int) string {
	if path := view.selectedPath(); path != "" {
		path, progress := terminaltext.Clean(path), view.hunkProgress()
		if over := ansi.StringWidth(path) + ansi.StringWidth(progress) - width; over > 0 && progress != "" {
			path = ansi.TruncateLeft(path, over+1, "…") // +1: the ellipsis takes a cell
		}
		return path + progress
	}
	return "Overview"
}

// fileRows renders the Files list window of height rows.
func (view *View) fileRows(width, height int) []string {
	files := view.files()
	var rows []string
	view.window.SetSize(height)
	for row := view.window.Top(); row < len(view.rows) && len(rows) < height; row++ {
		selected := row == view.window.Cursor()
		entry := view.rows[row]
		if entry == 0 {
			rows = append(rows, view.overviewRow(selected))
			continue
		}
		file := files[entry-1]
		faded := view.fades.step(fadeKey(file.Path))
		rows = append(rows, view.fileRow(file, width, selected, view.options.HighlightSearch() && view.search.Marked(row), faded))
	}
	return rows
}

func (view *View) overviewRow(selected bool) string {
	if selected {
		return view.theme.Selected.Render("> Overview")
	}
	return view.theme.Normal.Render("  Overview")
}

// fileRow draws one file. faded is how far it has faded (theme.FadeSteps at
// most); the selected row never fades.
func (view *View) fileRow(file github.ChangedFile, width int, selected, matched bool, faded int) string {
	counts := ""
	if file.Additions > 0 {
		counts += fmt.Sprintf("+%d", file.Additions)
	}
	if file.Deletions > 0 {
		counts += fmt.Sprintf(" -%d", file.Deletions)
	}
	counts = strings.TrimSpace(counts)
	pathWidth := max(4, width-2-2-2-1-ansi.StringWidth(counts))
	path := terminaltext.Clean(file.Path)
	if over := ansi.StringWidth(path) - pathWidth; over > 0 {
		path = ansi.TruncateLeft(path, over+1, "…") // +1: the ellipsis takes a cell
	}
	path = padRight(path, pathWidth)
	marker := changeMarker(file.Change)

	if faded > 0 && !selected {
		lead := "  "
		if matched {
			lead = search.Glyph + " "
		}
		return view.theme.Fade[faded-1].Render(lead + viewedGlyph(file.Viewed) + " " + marker + " " + path + " " + counts)
	}
	if selected {
		cursor := "> "
		if matched {
			cursor = search.SelectedGlyph
		}
		return view.theme.Selected.Render(cursor + viewedGlyph(file.Viewed) + " " + marker + " " + path + " " + counts)
	}
	modifiedStyle := view.theme.FileModified
	switch file.Change {
	case github.FileAdded:
		modifiedStyle = view.theme.FileAdded
	case github.FileDeleted:
		modifiedStyle = view.theme.FileDeleted
	case github.FileModified, github.FileRenamed, github.FileCopied, github.FileChanged:
	}
	var names []string
	if file.Additions > 0 {
		names = append(names, view.theme.FileAdded.Render(fmt.Sprintf("+%d", file.Additions)))
	}
	if file.Deletions > 0 {
		names = append(names, view.theme.FileDeleted.Render(fmt.Sprintf("-%d", file.Deletions)))
	}
	pathStyle, glyph := view.theme.Normal, " "
	switch file.Viewed {
	case github.FileViewed:
		// Viewed files recede; the check is the only bright part of the row.
		pathStyle, glyph = view.theme.Muted, view.theme.FileAdded.Render(viewedGlyph(file.Viewed))
	case github.FileDismissed:
		// Changed since viewed: needs another look, so the row stays normal.
		glyph = view.theme.FileModified.Render(viewedGlyph(file.Viewed))
	case github.FileUnviewed:
	}
	lead := "  "
	if matched {
		lead = view.theme.SearchMarker.Render(search.Glyph) + " "
	}
	return lead + glyph + " " + modifiedStyle.Render(marker) + " " + pathStyle.Render(path) + " " + strings.Join(names, " ")
}

// viewedGlyph is the one-cell mark of a file's viewed state.
func viewedGlyph(state github.ViewedState) string {
	switch state {
	case github.FileViewed:
		return "✓"
	case github.FileDismissed:
		return "↻"
	case github.FileUnviewed:
	}
	return " "
}

func changeMarker(change github.FileChangeType) string {
	switch change {
	case github.FileAdded:
		return "A"
	case github.FileDeleted:
		return "D"
	case github.FileRenamed:
		return "R"
	case github.FileCopied:
		return "C"
	case github.FileModified:
		return "M"
	case github.FileChanged:
		return "T"
	default:
		return "?"
	}
}

// rightRows renders the right pane's content.
func (view *View) rightRows(width, height int) []string {
	if view.selection() > 0 {
		return strings.Split(view.filePane.Render(width, height), "\n")
	}
	lines := strings.Split(view.overview.viewport.View(), "\n")
	for i, line := range lines {
		lines[i] = " " + line
	}
	return lines
}

func padRight(text string, width int) string {
	if count := width - ansi.StringWidth(text); count > 0 {
		return text + strings.Repeat(" ", count)
	}
	return text
}

// drawPane frames lines in a titled rounded box of exactly w x h cells.
func drawPane(view *View, title string, lines []string, width, height int, focused bool) []string {
	border, titleStyle := view.theme.PaneBorder, view.theme.PaneTitle
	if focused {
		border, titleStyle = view.theme.PaneBorderFocus, view.theme.PaneTitleFocus
	}
	inner := width - 2
	title = ansi.Truncate(title, max(0, inner-3), "…")
	text := " " + title + " "
	top := border.Render("╭─") + titleStyle.Render(text) +
		border.Render(strings.Repeat("─", max(0, inner-1-ansi.StringWidth(text)))+"╮")
	out := make([]string, 0, height)
	out = append(out, top)
	side := border.Render("│")
	for i := range height - 2 {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		line = padRight(ansi.Truncate(line, inner, ""), inner)
		out = append(out, side+line+side)
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

// overlay draws box centred horizontally over rows, starting at row top.
func overlay(rows []string, box string, width, top int) {
	if box == "" {
		return
	}
	boxLines := strings.Split(box, "\n")
	boxWidth := ansi.StringWidth(boxLines[0])
	column := max(0, (width-boxWidth)/2)
	for i, line := range boxLines {
		row := top + 1 + i
		if row >= len(rows) {
			return
		}
		base := padRight(rows[row], width)
		rows[row] = ansi.Cut(base, 0, column) + line + ansi.Cut(base, column+boxWidth, width)
	}
}
