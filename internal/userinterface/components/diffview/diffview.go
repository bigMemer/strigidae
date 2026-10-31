// Package diffview renders the unified diff of ONE file: old and new line
// number gutters, add/delete colouring, syntax highlighting, a cursor line,
// vertical and horizontal scrolling.
//
// It is driven by its host through methods, never through key messages. A
// workspace maps keys to methods like this:
//
//	j / k          CursorDown(1) / CursorUp(1)
//	g g / G        GotoTop() / GotoBottom()
//	ctrl+d / ctrl+u HalfPageDown() / HalfPageUp()
//	ctrl+f / ctrl+b PageDown() / PageUp()
//	] / [          NextHunk() / PreviousHunk()
//	z h / z l      ScrollLeft(4) / ScrollRight(4)
//	z H / z L      HalfScrollLeft() / HalfScrollRight()
//	z t / z z / z b  ScrollCursorTop() / ScrollCursorMiddle() / ScrollCursorBottom()
//	z a / z o / z c  ToggleThread() / OpenThread() / CloseThread()
//	) / (          NextThread() / PreviousThread()
//
// Usage:
//
//	diffView := diffview.New(theme.Default())
//	diffView.SetFile(parsed.File("main.go"))
//	out := diffView.Render(width, height)
//	if cursor, ok := diffView.Cursor(); ok { /* comment on cursor.Path line cursor.New or cursor.Old */ }
//
// Review threads (SetThreads) are placed under the line they are anchored to
// and take extra rows: one header row, plus the comments while expanded.
// Threads that no longer map onto a line of the diff are listed after the last
// row. Only the visible rows are ever styled or highlighted, and highlighted
// rows are cached, so scrolling a 5k-line file never re-lexes it.
package diffview

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/components/markdown"
	"github.com/MikePors/strigidae/internal/userinterface/components/scroll"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// CursorLine describes the diff line under the cursor.
type CursorLine struct {
	Path     string // the file's current path (old path for deleted files)
	Hunk     int    // index into File.Hunks
	Kind     diff.LineKind
	Old, New int             // line numbers; 0 on the side where the line does not exist
	Side     github.DiffSide // github.SideLeft for deleted lines, else github.SideRight
	Text     string
}

// Number returns the line number on the cursor's side.
func (cursorLine CursorLine) Number() int {
	if cursorLine.Side == github.SideLeft {
		return cursorLine.Old
	}
	return cursorLine.New
}

// Option configures New.
type Option func(*Model)

// WithHighlighter replaces the default Chroma highlighter (used by tests to
// count calls or to keep output free of lexer details).
func WithHighlighter(highlighter Highlighter) Option {
	return func(model *Model) { model.highlight = highlighter }
}

// WithClock sets the clock used for the relative times in review threads.
func WithClock(now func() time.Time) Option { return func(model *Model) { model.now = now } }

// row is one display row: a hunk header (line == nil, tr == nil), a diff line,
// or one line of a review thread (tr != nil).
type row struct {
	hunk      int
	line      *diff.Line
	text      string // display text: tabs expanded, controls replaced
	baseIndex int    // index in base of this row, or of the row a thread row follows
	threadRow *threadRow
	message   bool // one line of the file's notice (binary, renamed, ...), shown above its threads
	// placeholder marks the header row of a hidden hunk, the one row left of
	// it; see hidden.go.
	placeholder bool
}

// isLine reports whether r is a diff line a comment can be written on: not a
// hunk header, notice or thread row, and not a "no newline" marker.
func (row row) isLine() bool {
	return row.threadRow == nil && row.line != nil && row.line.Kind != diff.NoNewline
}

// isCode reports whether row draws a diff line's text (a line or a "no
// newline" marker), the rows that wrap.
func (row row) isCode() bool { return row.threadRow == nil && row.line != nil }

// threadRow places a row inside a thread's block of lines.
type threadRow struct{ threadIndex, rowInBlock int }

// Model is the diff pane. Create it with New. It is not a tea.Model.
type Model struct {
	theme     theme.Theme
	highlight Highlighter
	styles    styleSet

	file    *diff.File
	message string // when set, shown instead of the diff
	base    []row  // the diff's own rows; rows is base with thread rows added
	rows    []row
	hunkRow []int // row index of each hunk header

	now              func() time.Time
	markdownRenderer *markdown.Renderer
	threads          []github.ReviewThread // all of the pull request's threads
	fileThreads      []github.ReviewThread // the ones on the shown file
	attach           map[int][]int         // base row -> indexes in fileThreads
	orphans          []int                 // fileThreads that map to no diff line
	open             map[string]bool       // thread ID -> expanded, once the user chose
	hideResolved     bool                  // see SetHideResolved
	skipResolved     bool                  // see SetSkipResolved
	blocks           [][]string            // rendered lines of each fileThreads entry
	owners           [][]int               // for each line of each block, the comment it belongs to or -1
	blockCache       map[blockKey]block    // rendered threads; dropped by SetThreads and when width or theme change
	blockSignature   blockSignature
	renders          int // thread blocks rendered, for tests
	wrappedRows      int // screen rows of wrapped lines drawn, for tests
	numberWidth      int // width of one line-number column
	maximumWidth     int // widest code line, in cells

	hunkReviewed func(file *diff.File, hunk int) bool // see SetHunkReviewed; nil: none are
	hunkHidden   func(file *diff.File, hunk int) bool // see SetHunkHidden; nil: none are
	hunkFade     func(file *diff.File, hunk int) int  // see SetHunkFade; nil: none fade

	threadFade func(identifier string) (step int, fading bool) // see SetThreadFade; nil: none fade

	cache map[int][]Span // highlighted rows, by base row index; reset per file
	saved *position      // position kept while a message replaces the file

	width, height int

	visual          bool // a line selection is in progress
	visualBaseIndex int  // base row where it started

	window scroll.Window // cursor, top row and horizontal offset

	search          search.State // over rows; see search.go
	highlightSearch bool         // matching rows are marked

	wrap           bool // long lines continue on the next screen rows; see wrap.go
	relativeNumber bool // the gutter numbers rows by distance from the cursor; see relative.go
	// wrapStarts caches, per base row, the column each of its screen rows
	// starts at; it holds starts for wrapWidth only.
	wrapStarts map[int][]int
	wrapWidth  int
}

// New returns an empty diff pane.
func New(activeTheme theme.Theme, options ...Option) *Model {
	model := &Model{theme: activeTheme, highlight: ChromaHighlighter(), cache: map[int][]Span{}, now: time.Now, open: map[string]bool{}}
	for _, option := range options {
		option(model)
	}
	model.styles = newStyleSet(activeTheme)
	model.markdownRenderer = markdown.New(activeTheme)
	return model
}

// SetTheme swaps the theme. The highlight cache holds token types, not
// colours, so it stays valid.
func (model *Model) SetTheme(activeTheme theme.Theme) {
	model.theme = activeTheme
	model.styles = newStyleSet(activeTheme)
	model.markdownRenderer = markdown.New(activeTheme)
	model.layout()
}

// SetFile shows f (nil clears the pane). Passing the file already shown is a
// no-op. Passing a different *diff.File with the same path (a re-parsed
// refresh) rebuilds the rows and re-anchors the cursor on the same diff line
// (matching kind and line numbers, else the nearest line), keeping its place in
// the window and the horizontal offset. A different path resets them.
func (model *Model) SetFile(file *diff.File) {
	if file != nil && file == model.file && model.message == "" {
		return
	}
	// A refresh re-parses the diff, giving a new pointer for the same file
	// (possibly with a message shown in between): put the cursor back on the
	// same diff line so the reviewer does not lose their place.
	var previous *position
	switch {
	case file == nil:
	case model.file != nil && file.Path() == model.file.Path():
		position := model.capture()
		previous = &position
	case model.file == nil && model.saved != nil && model.saved.path == file.Path():
		previous = model.saved
	}
	model.saved = nil
	model.file, model.message = file, ""
	model.reset()
	if previous == nil {
		model.search.Clear() // another file: the query belonged to the last one
	}
	if file == nil {
		return
	}
	if file.Binary || len(file.Hunks) == 0 {
		// No diff rows: the file's notice and its threads are all there is.
		model.placeThreads()
		model.layout()
	} else {
		model.build(file)
	}
	if previous != nil {
		model.restore(*previous)
	}
}

// SetMessage shows a message instead of a diff (for example while loading).
// The position in the file being replaced is remembered, so a following
// SetFile for the same path (the refresh finishing) restores it.
func (model *Model) SetMessage(message string) {
	if model.file != nil {
		position := model.capture()
		model.saved = &position
	}
	model.file, model.message = nil, message
	model.reset()
}

// SetDiffError shows the message for a failed diff load; it recognises
// github.ErrDiffTooLarge.
func (model *Model) SetDiffError(err error) {
	if errors.Is(err, github.ErrDiffTooLarge) {
		model.SetMessage(TooLargeMessage())
		return
	}
	model.SetMessage("Could not load the diff: " + err.Error())
}

// TooLargeMessage is the text shown when GitHub refuses to serve the diff.
func TooLargeMessage() string {
	return "This diff is too large to load through the GitHub API.\nOpen the pull request in the browser to view it."
}

func (model *Model) reset() {
	model.blockCache = nil
	model.visual = false // base rows are about to change
	model.base, model.rows, model.hunkRow = nil, nil, nil
	model.fileThreads, model.attach, model.orphans, model.blocks, model.owners = nil, nil, nil, nil, nil
	model.cache = map[int][]Span{}
	model.wrapStarts = nil
	model.window.Reset()
	model.maximumWidth, model.numberWidth = 0, 0
	model.sync()
	model.search.Refresh(model.searchList())
}

func (model *Model) build(file *diff.File) {
	total := 0
	for _, hunk := range file.Hunks {
		total += len(hunk.Lines) + 1
	}
	model.base = make([]row, 0, total)
	maximumNumber := 0
	for high := range file.Hunks {
		hunk := &file.Hunks[high]
		model.base = append(model.base, row{hunk: high, baseIndex: len(model.base)})
		maximumNumber = max(maximumNumber, hunk.OldStart+hunk.OldLines, hunk.NewStart+hunk.NewLines)
		for lineIndex := range hunk.Lines {
			line := &hunk.Lines[lineIndex]
			text, width := displayText(line.Text)
			model.maximumWidth = max(model.maximumWidth, width)
			model.base = append(model.base, row{hunk: high, line: line, text: text, baseIndex: len(model.base)})
		}
	}
	model.numberWidth = max(len(strconv.Itoa(maximumNumber)), 3)
	model.placeThreads()
	model.layout()
}

// SetSize sets the drawing area. Scroll positions are re-clamped.
func (model *Model) SetSize(width, height int) {
	relayout := width != model.width
	model.width, model.height = max(width, 0), max(height, 0)
	if relayout {
		model.layout() // thread text wraps to the width
		return
	}
	model.sync()
}

// Size returns the current drawing area.
func (model *Model) Size() (width, height int) { return model.width, model.height }

// gutterWidth is the fixed width of the non-scrolling left part of a row:
// cursor mark, relative number (when shown), then lineNumbersWidth.
func (model *Model) gutterWidth() int {
	return 1 + model.relativeColumnWidth() + model.lineNumbersWidth()
}

// lineNumbersWidth is the width of the gutter after the relative number: old
// number, new number, thread marker, change marker.
func (model *Model) lineNumbersWidth() int { return 2*model.numberWidth + 4 }

func (model *Model) codeWidth() int { return max(model.width-model.gutterWidth(), 0) }

// sync pushes the diff's row count and widths into the scroll window.
func (model *Model) sync() {
	model.window.SetRows(len(model.rows))
	model.window.SetSize(model.height)
	if model.wrap {
		model.window.SetRowHeight(model.rowHeight)
		model.window.SetWidths(0, model.codeWidth()) // nothing to scroll sideways
		return
	}
	model.window.SetRowHeight(nil)
	model.window.SetWidths(model.maximumWidth, model.codeWidth())
}

// cursorKey identifies the cursor row independently of how many thread rows
// precede it. A thread row is identified by the thread's ID, so a reload that
// adds or removes threads keeps the cursor on the same one.
type cursorKey struct {
	baseIndex   int
	thread      string // ID of the thread the cursor is in, or ""
	blockOffset int    // row within the thread's block
}

func (model *Model) cursorKey() (cursorKey, bool) {
	cursor := model.window.Cursor()
	if cursor >= len(model.rows) {
		return cursorKey{}, false
	}
	row := model.rows[cursor]
	key := cursorKey{baseIndex: row.baseIndex}
	if row.threadRow != nil {
		key.thread, key.blockOffset = model.fileThreads[row.threadRow.threadIndex].Identifier, row.threadRow.rowInBlock
	}
	return key, true
}

// keyRow returns the row for key; a thread row that is gone (a collapsed block
// is shorter) falls back to the nearest one in its block, then to its line.
func (model *Model) keyRow(key cursorKey) int {
	if key.thread != "" {
		if row := model.threadRowFor(key.thread, key.blockOffset); row >= 0 {
			return row
		}
	}
	return model.baseRow(key.baseIndex)
}

// baseRow returns the row showing base row baseIndex, or the placeholder of
// its hunk when that is hidden.
func (model *Model) baseRow(baseIndex int) int {
	for i, row := range model.rows {
		if row.threadRow == nil && row.baseIndex == baseIndex {
			return i
		}
	}
	if baseIndex >= 0 && baseIndex < len(model.base) {
		return max(model.placeholderRow(model.base[baseIndex].hunk), 0)
	}
	return 0
}

// threadRowFor returns the row at offset blockOffset in the block of the thread with
// the given ID (clamped to the block), or -1 if the thread is not shown.
func (model *Model) threadRowFor(identifier string, blockOffset int) int {
	best := -1
	for i, row := range model.rows {
		if row.threadRow != nil && model.fileThreads[row.threadRow.threadIndex].Identifier == identifier && row.threadRow.rowInBlock <= blockOffset {
			best = i
		}
	}
	return best
}

// layout rebuilds rows from the diff's rows and the threads, keeping the
// cursor on the same row and at the same place in the view.
func (model *Model) layout() {
	key, found := model.cursorKey()
	model.relayout(key, found)
}

// relayout is layout with the cursor key taken by the caller, for changes to
// the thread list, which would make the current rows' thread indexes stale.
func (model *Model) relayout(key cursorKey, found bool) {
	cursorOffset := model.window.Cursor() - model.window.Top()
	model.rows = model.rows[:0]
	model.hunkRow = model.hunkRow[:0]
	model.blocks = make([][]string, len(model.fileThreads))
	model.owners = make([][]int, len(model.fileThreads))
	for i := range model.fileThreads {
		built := model.renderBlock(i)
		model.blocks[i], model.owners[i] = built.lines, built.owners
	}
	base := model.base
	// Counted after hiding: a notice whose threads are all hidden is drawn
	// as for a file with none, with no cursor.
	if model.file != nil && len(base) == 0 && model.ThreadCount() > 0 {
		base = model.noticeRows()
	}
	hiddenHunk := -1 // the hunk whose lines are being left out
	var pending []int
	flush := func(baseIndex int) {
		for _, threadIndex := range model.shownThreads(pending) {
			for lineIndex := range model.blocks[threadIndex] {
				model.rows = append(model.rows, row{baseIndex: baseIndex, threadRow: &threadRow{threadIndex: threadIndex, rowInBlock: lineIndex}})
			}
		}
		pending = pending[:0]
	}
	for i, row := range base {
		switch {
		case row.line == nil && !row.message:
			hiddenHunk = -1
			if model.isHidden(row.hunk) {
				hiddenHunk, row.placeholder = row.hunk, true
			} else {
				model.hunkRow = append(model.hunkRow, len(model.rows))
			}
		case row.line != nil && row.hunk == hiddenHunk:
			continue // with the threads on it
		}
		model.rows = append(model.rows, row)
		pending = append(pending, model.attach[i]...)
		if i+1 < len(base) && base[i+1].line != nil && base[i+1].line.Kind == diff.NoNewline {
			continue // the marker belongs to this line; threads go after it
		}
		flush(i)
	}
	pending = append(pending, model.orphans...)
	flush(len(base) - 1)
	if model.visual && model.visualBaseIndex < len(model.base) && model.isHidden(model.base[model.visualBaseIndex].hunk) {
		model.visual = false // its first line is gone
	}
	model.sync()
	model.search.Refresh(model.searchList()) // the rows moved; the query stays
	if found && len(model.rows) > 0 {
		cursorRow := model.keyRow(key)
		model.window.Restore(cursorRow, cursorRow-cursorOffset, model.window.HorizontalOffset())
	}
}

// --- vertical movement: cursor follows, view follows the cursor ---

// CursorDown moves the cursor down n rows, scrolling to keep it visible.
func (model *Model) CursorDown(count int) { model.window.Down(count) }

// CursorUp moves the cursor up n rows, scrolling to keep it visible.
func (model *Model) CursorUp(count int) { model.window.Up(count) }

// GotoTop moves the cursor to the first row.
func (model *Model) GotoTop() { model.window.GotoTop() }

// GotoBottom moves the cursor to the last row.
func (model *Model) GotoBottom() { model.window.GotoBottom() }

// CursorRow returns the index of the row under the cursor.
func (model *Model) CursorRow() int { return model.window.Cursor() }

// GotoLine moves the cursor to line number line of the file's new side. When
// no hunk shows that line it goes to the shown line with the closest number,
// preferring the later one on a tie. A deleted file has no new side, so its
// old-side numbers are used. A line of a hidden hunk lands on the hunk's
// placeholder. Without any numbered line it does nothing.
func (model *Model) GotoLine(line int) {
	if model.file == nil {
		return
	}
	side := github.SideRight
	if model.file.Status == diff.Deleted {
		side = github.SideLeft
	}
	model.GotoSideLine(side, line)
}

// GotoSideLine moves the cursor to line number line of side (LEFT: the old
// side), or the closest shown line of that side as GotoLine does. A file
// with no line on that side (an added file's old side) keeps the cursor.
func (model *Model) GotoSideLine(side github.DiffSide, line int) {
	if model.file == nil {
		return
	}
	best, bestNumber := -1, 0
	for i, row := range model.base {
		if !row.isLine() {
			continue
		}
		number := row.line.New
		if side == github.SideLeft {
			number = row.line.Old
		}
		if number <= 0 {
			continue
		}
		distance, bestDistance := absolute(number-line), absolute(bestNumber-line)
		if best < 0 || distance < bestDistance || (distance == bestDistance && number > bestNumber) {
			best, bestNumber = i, number
		}
	}
	if best >= 0 {
		model.window.GotoRow(model.baseRow(best))
	}
}

// --- viewport movement: view and cursor move together, as in vim ---

// ScrollDown scrolls the view down n rows without moving the cursor, unless
// the cursor would leave the view, in which case it is pulled along.
func (model *Model) ScrollDown(count int) { model.window.ScrollDown(count) }

// ScrollUp is the opposite of ScrollDown.
func (model *Model) ScrollUp(count int) { model.window.ScrollUp(count) }

// ScrollCursorTop scrolls so the cursor row is the first visible row.
func (model *Model) ScrollCursorTop() { model.window.ScrollCursorTop() }

// ScrollCursorMiddle scrolls so the cursor row is in the middle of the view.
func (model *Model) ScrollCursorMiddle() { model.window.ScrollCursorMiddle() }

// ScrollCursorBottom scrolls so the cursor row is the last visible row.
func (model *Model) ScrollCursorBottom() { model.window.ScrollCursorBottom() }

// HalfPageDown moves the view and the cursor down half a screen.
func (model *Model) HalfPageDown() { model.window.HalfPageDown() }

// HalfPageUp moves the view and the cursor up half a screen.
func (model *Model) HalfPageUp() { model.window.HalfPageUp() }

// PageDown moves the view and the cursor down a full screen.
func (model *Model) PageDown() { model.window.PageDown() }

// PageUp moves the view and the cursor up a full screen.
func (model *Model) PageUp() { model.window.PageUp() }

// NextHunk moves the cursor to the next hunk header (no-op on the last hunk),
// putting the header at the top of the view.
func (model *Model) NextHunk() {
	for _, row := range model.hunkRow {
		if row > model.window.Cursor() {
			model.window.JumpTo(row)
			return
		}
	}
}

// PreviousHunk moves the cursor to the closest hunk header above it, which is the
// current hunk's own header when the cursor is inside the hunk.
func (model *Model) PreviousHunk() {
	for i := len(model.hunkRow) - 1; i >= 0; i-- {
		if model.hunkRow[i] < model.window.Cursor() {
			model.window.JumpTo(model.hunkRow[i])
			return
		}
	}
}

// --- horizontal scrolling ---

// ScrollRight shifts the code area n cells to the right (reveals later text).
func (model *Model) ScrollRight(count int) { model.window.Right(count) }

// ScrollLeft shifts the code area n cells to the left.
func (model *Model) ScrollLeft(count int) { model.window.Left(count) }

// HalfScrollRight scrolls right by half the code area.
func (model *Model) HalfScrollRight() { model.window.HalfRight() }

// HalfScrollLeft scrolls left by half the code area.
func (model *Model) HalfScrollLeft() { model.window.HalfLeft() }

// HorizontalOffset returns the horizontal scroll in cells.
func (model *Model) HorizontalOffset() int { return model.window.HorizontalOffset() }

// Top returns the index of the first visible row (hunk headers count as rows).
func (model *Model) Top() int { return model.window.Top() }

// Cursor describes the diff line under the cursor. ok is false when there is
// no file, or the cursor is on a hunk header or a "no newline" marker.
func (model *Model) Cursor() (cursorLine CursorLine, ok bool) {
	if model.file == nil || model.window.Cursor() >= len(model.rows) {
		return CursorLine{}, false
	}
	row := model.rows[model.window.Cursor()]
	if !row.isLine() {
		return CursorLine{}, false
	}
	cursorLine = CursorLine{
		Path: model.file.Path(), Hunk: row.hunk, Kind: row.line.Kind,
		Old: row.line.Old, New: row.line.New, Side: github.SideRight, Text: row.line.Text,
	}
	if row.line.Kind == diff.Delete {
		cursorLine.Side = github.SideLeft
	}
	return cursorLine, true
}

// Render draws the pane in a w x h area. It is SetSize followed by the render,
// so hosts can call it from their own Render(width, height).
func (model *Model) Render(width, height int) string {
	model.SetSize(width, height)
	if model.width == 0 || model.height == 0 {
		return ""
	}
	if len(model.rows) == 0 {
		return model.renderMessage()
	}
	lines := make([]string, 0, model.height)
	for i := model.window.Top(); i < len(model.rows) && len(lines) < model.height; i++ {
		var drawn []string
		if model.wrap && model.rows[i].isCode() {
			drawn = model.renderWrapped(i, model.height-len(lines))
		} else {
			drawn = []string{model.renderRow(i)}
		}
		if fade := model.fadeOf(i); fade > 0 {
			drawn = model.faded(drawn, fade)
		}
		lines = append(lines, drawn...)
	}
	return strings.Join(lines[:min(len(lines), model.height)], "\n")
}

func (model *Model) renderMessage() string {
	lines := model.messageLines()
	var out []string
	for _, line := range lines {
		line, _ = displayText(line)
		out = append(out, strings.Split(ansi.Wrap(line, model.width, ""), "\n")...)
	}
	if len(out) > model.height {
		out = out[:model.height]
	}
	for i, line := range out {
		out[i] = model.theme.Muted.Render(line)
	}
	// Anything built from diff content (paths, modes, errors) is untrusted.
	return strings.Join(out, "\n")
}

func (model *Model) messageLines() []string {
	switch {
	case model.message != "":
		return strings.Split(model.message, "\n")
	case model.file == nil:
		return []string{"No file selected."}
	}
	file := model.file
	var out []string
	if file.PatchMissing {
		out = append(out, strings.Split(TooLargeMessage(), "\n")...)
	}
	if file.Binary {
		out = append(out, "Binary file: contents not shown.")
	}
	switch file.Status {
	case diff.Renamed:
		out = append(out, "Renamed: "+file.OldPath+" -> "+file.NewPath+similarity(file))
	case diff.Copied:
		out = append(out, "Copied: "+file.OldPath+" -> "+file.NewPath+similarity(file))
	}
	if file.ModeChanged() {
		out = append(out, "Mode changed: "+file.OldMode+" -> "+file.NewMode)
	}
	if len(out) == 0 {
		switch file.Status {
		case diff.Added:
			out = append(out, "Empty file added.")
		case diff.Deleted:
			out = append(out, "Empty file deleted.")
		default:
			out = append(out, "No changes to show.")
		}
	}
	return out
}

func similarity(file *diff.File) string {
	if file.Similarity <= 0 {
		return ""
	}
	return " (" + strconv.Itoa(file.Similarity) + "% similar)"
}
