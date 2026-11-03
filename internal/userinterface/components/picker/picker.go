// Package picker is a small filterable list shown as a popup. The owning view
// must capture keys while the picker is open (userinterface.KeyCapturer) and feed every
// key to Update; esc closes it.
package picker

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/userinterface/components/scroll"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// Outcome is what a key press did to the picker.
type Outcome int

// Outcomes of Update.
const (
	// Open means the picker stays open.
	Open Outcome = iota
	// Chosen means the user picked an item; Result.Index says which.
	Chosen
	// Cancelled means the user pressed esc.
	Cancelled
)

// Result is returned by Update.
type Result struct {
	Outcome Outcome
	// Index is the chosen item's index in the slice given to New.
	Index int
}

// Model is a filterable list. Matching is a case-insensitive subsequence test,
// so "wsgo" finds "views/workspace/workspace.go".
type Model struct {
	theme   theme.Theme
	title   string
	items   []string
	query   []rune
	matches []int         // indexes into items
	window  scroll.Window // cursor and top row over matches
	note    string        // muted explanation under the items, if any
}

// New returns a picker over items.
func New(activeTheme theme.Theme, title string, items []string) *Model {
	model := &Model{theme: activeTheme, title: title, items: items}
	model.refilter()
	return model
}

// SetNote sets a muted line of explanation drawn under the items, for example
// why some choices are missing.
func (model *Model) SetNote(note string) { model.note = note }

// Select puts the cursor on the item at index in the slice given to New, so a
// picker can open on a sensible choice. An index out of range changes nothing.
func (model *Model) Select(index int) {
	for position, match := range model.matches {
		if match == index {
			model.window.GotoRow(position)
			return
		}
	}
}

// Query returns the current filter text.
func (model *Model) Query() string { return string(model.query) }

// Matches returns the number of items passing the filter.
func (model *Model) Matches() int { return len(model.matches) }

// Update handles one key press. Every key is consumed: printable text edits
// the filter, so letters never trigger anything outside the picker.
func (model *Model) Update(message tea.KeyPressMsg) Result {
	switch message.String() {
	case "esc":
		return Result{Outcome: Cancelled}
	case "enter":
		if len(model.matches) == 0 {
			return Result{Outcome: Open}
		}
		return Result{Outcome: Chosen, Index: model.matches[model.window.Cursor()]}
	case "up", "ctrl+p":
		model.window.Up(1)
	case "down", "ctrl+n":
		model.window.Down(1)
	case "backspace":
		if count := len(model.query); count > 0 {
			model.query = model.query[:count-1]
			model.refilter()
		}
	case "ctrl+u":
		model.query = nil
		model.refilter()
	default:
		if text := message.Text; text != "" && !strings.ContainsFunc(text, unicode.IsControl) {
			model.query = append(model.query, []rune(text)...)
			model.refilter()
		}
	}
	return Result{Outcome: Open}
}

func (model *Model) refilter() {
	model.matches = model.matches[:0]
	query := strings.ToLower(string(model.query))
	for i, item := range model.items {
		if subsequence(strings.ToLower(item), query) {
			model.matches = append(model.matches, i)
		}
	}
	model.window.Reset()
	model.window.SetRows(len(model.matches))
}

func subsequence(text, query string) bool {
	runes := []rune(text)
	i := 0
	for _, character := range query {
		for i < len(runes) && runes[i] != character {
			i++
		}
		if i == len(runes) {
			return false
		}
		i++
	}
	return true
}

// Render draws the popup, sized to fit within width x height. It returns ""
// when there is no room.
func (model *Model) Render(width, height int) string {
	paneWidth := min(64, width)
	if paneWidth < 12 || height < 5 {
		return ""
	}
	inner := paneWidth - 2
	// The selected item always keeps a row; the note gets what is left.
	rows := min(max(len(model.matches), 1), min(height-4, 12))
	noteLines := model.noteLines(inner - 2)
	noteLines = noteLines[:min(len(noteLines), max(height-4-rows, 0))]

	lines := make([]string, 0, rows+2)
	query := string(model.query)
	if over := ansi.StringWidth(query) - (inner - 3); over > 0 {
		query = ansi.TruncateLeft(query, over+1, "…") // keep the tail of a long query visible
	}
	lines = append(lines, model.theme.Title.Render("> ")+model.theme.Normal.Render(query)+model.theme.Muted.Render("▏"))
	if len(model.matches) == 0 {
		lines = append(lines, model.theme.Muted.Render("no matches"))
	}
	model.window.SetSize(rows)
	for i := model.window.Top(); i < len(model.matches) && i < model.window.Top()+rows; i++ {
		label := model.items[model.matches[i]]
		if over := ansi.StringWidth(label) - (inner - 4); over > 0 {
			label = ansi.TruncateLeft(label, over+1, "…") // keep the file name visible
		}
		if i == model.window.Cursor() {
			lines = append(lines, model.theme.OverlayItemFocus.Render(pad("> "+label, inner-2)))
		} else {
			lines = append(lines, model.theme.OverlayItem.Render("  "+label))
		}
	}
	for _, line := range noteLines {
		lines = append(lines, model.theme.Muted.Render(line))
	}
	box := model.theme.OverlayBox.
		Width(paneWidth).
		Render(strings.Join(lines, "\n"))
	return model.titled(box)
}

func (model *Model) noteLines(width int) []string {
	if model.note == "" || width < 1 {
		return nil
	}
	return strings.Split(ansi.Wrap(model.note, width, ""), "\n")
}

// titled puts the title in the top edge of the box.
func (model *Model) titled(box string) string {
	if model.title == "" {
		return box
	}
	lines := strings.Split(box, "\n")
	text := " " + model.title + " "
	width := ansi.StringWidth(lines[0])
	if ansi.StringWidth(text)+4 > width {
		return box
	}
	lines[0] = ansi.Cut(lines[0], 0, 2) + model.theme.OverlayBorder.Render(text) + ansi.Cut(lines[0], 2+ansi.StringWidth(text), width)
	return strings.Join(lines, "\n")
}

func pad(text string, width int) string {
	if count := width - ansi.StringWidth(text); count > 0 {
		return text + strings.Repeat(" ", count)
	}
	return text
}
