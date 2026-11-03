package pullrequestlist

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/search"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// searchRows has "fix" in rows 1, 3 and 5.
func searchRows() []github.PullRequest {
	titles := []string{"Add cache", "Fix parser", "Tidy docs", "Fix lexer", "Rename flag", "Fix prompt"}
	rows := makePullRequests(len(titles))
	for i := range rows {
		rows[i].Title = titles[i]
	}
	return rows
}

func searchView(test *testing.T) *View {
	test.Helper()
	return loadedView(test, fakeWith(searchRows()), nil)
}

func typeSearch(view *View, text string) {
	view.Update(userinterface.PromptResultMessage{Prefix: "/", Text: text, State: userinterface.PromptChanged})
}

func finishSearch(view *View, text string, state userinterface.PromptState) []tea.Msg {
	_, command := view.Update(userinterface.PromptResultMessage{Prefix: "/", Text: text, State: state})
	if command == nil {
		return nil
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, inner := range batch {
			if inner != nil {
				out = append(out, inner())
			}
		}
		return out
	}
	return []tea.Msg{message}
}

func TestSlashAsksForThePrompt(test *testing.T) {
	view := searchView(test)
	if _, ok := view.PromptRequest(); ok {
		test.Fatal("a prompt is requested before /")
	}
	press(view, "/")
	if prefix, ok := view.PromptRequest(); !ok || prefix != "/" {
		test.Errorf("after / the request is %q, %v", prefix, ok)
	}
	if _, ok := view.PromptRequest(); ok {
		test.Error("the request was not cleared")
	}
}

func TestTypingMovesTheCursorLive(test *testing.T) {
	view := searchView(test)
	press(view, "j") // row 1
	press(view, "j") // row 2
	press(view, "/")
	typeSearch(view, "fix")
	if got := view.current().window.Cursor(); got != 3 {
		test.Errorf("cursor = %d, want the first match after row 2 (3)", got)
	}
	typeSearch(view, "fix p") // narrows to row 1 and 5; from row 2 the next is 5
	if got := view.current().window.Cursor(); got != 5 {
		test.Errorf("cursor = %d, want 5", got)
	}
	typeSearch(view, "zzz")
	if got := view.current().window.Cursor(); got != 2 {
		test.Errorf("cursor = %d with no match, want it back on 2", got)
	}
}

func TestEscWhileTypingRestoresTheCursor(test *testing.T) {
	view := searchView(test)
	press(view, "j")
	press(view, "/")
	typeSearch(view, "fix l")
	finishSearch(view, "fix l", userinterface.PromptCancelled)
	if got := view.current().window.Cursor(); got != 1 {
		test.Errorf("cursor = %d, want 1", got)
	}
	if view.current().search.Active() {
		test.Error("a cancelled search is still active")
	}
}

func TestEnterKeepsTheSearchAndEscClearsIt(test *testing.T) {
	view := searchView(test)
	press(view, "/")
	typeSearch(view, "fix")
	finishSearch(view, "fix", userinterface.PromptSubmitted)
	if !view.current().search.Marked(1) || !view.current().search.Marked(3) || !view.current().search.Marked(5) || view.current().search.Marked(0) {
		test.Error("enter did not keep the marks")
	}
	press(view, "esc")
	if view.current().search.Active() || view.current().search.Marked(1) {
		test.Error("esc in the list did not clear the search")
	}
}

func TestNextAndPreviousWrapAndTakeACount(test *testing.T) {
	view := searchView(test)
	press(view, "/")
	typeSearch(view, "fix")
	finishSearch(view, "fix", userinterface.PromptSubmitted) // cursor on 1
	press(view, "n")
	if got := view.current().window.Cursor(); got != 3 {
		test.Errorf("n: cursor = %d, want 3", got)
	}
	press(view, "n")
	press(view, "n") // wraps
	if got := view.current().window.Cursor(); got != 1 {
		test.Errorf("n wrap: cursor = %d, want 1", got)
	}
	press(view, "N") // wraps back
	if got := view.current().window.Cursor(); got != 5 {
		test.Errorf("N wrap: cursor = %d, want 5", got)
	}
	view.Update(keys.Message{Sequence: "n", Count: 3})
	if got := view.current().window.Cursor(); got != 5 {
		test.Errorf("3n: cursor = %d, want 5 (three matches round the ring)", got)
	}
	view.Update(keys.Message{Sequence: "n", Count: 2})
	if got := view.current().window.Cursor(); got != 3 {
		test.Errorf("2n: cursor = %d, want 3", got)
	}
}

func TestNoMatchSaysSoAndKeepsTheCursor(test *testing.T) {
	view := searchView(test)
	press(view, "j")
	press(view, "/")
	typeSearch(view, "zzz")
	messages := finishSearch(view, "zzz", userinterface.PromptSubmitted)
	var info []string
	for _, message := range messages {
		if text, ok := message.(userinterface.InfoMessage); ok {
			info = append(info, text.Text)
		}
	}
	if len(info) != 1 || info[0] != "no match: zzz" {
		test.Errorf("info = %q, want [no match: zzz]", info)
	}
	if got := view.current().window.Cursor(); got != 1 {
		test.Errorf("cursor = %d, want 1", got)
	}
}

func TestRefreshKeepsTheSearchAndRecomputesTheMarks(test *testing.T) {
	client := fakeWith(searchRows())
	view := loadedView(test, client, nil)
	press(view, "/")
	typeSearch(view, "fix")
	finishSearch(view, "fix", userinterface.PromptSubmitted)

	fresh := searchRows()
	fresh[0].Title = "Fix cache" // now matches
	fresh[3].Title = "Lexer"     // no longer matches
	client.SetPullRequests(testRepository, fresh)
	deliver(test, view, press(view, "R"))
	if !view.current().search.Marked(0) || view.current().search.Marked(3) || !view.current().search.Marked(1) {
		test.Error("marks were not recomputed after the refresh")
	}
	if view.current().search.Query() != "fix" {
		test.Errorf("query = %q", view.current().search.Query())
	}
}

func TestMatchingRowsCarryTheMarker(test *testing.T) {
	view := searchView(test)
	press(view, "/")
	typeSearch(view, "fix")
	finishSearch(view, "fix", userinterface.PromptSubmitted) // cursor on row 1
	marker := theme.Default().SearchMarker.Render(search.Glyph)
	lines := strings.Split(view.Render(80, 13), "\n")[1:] // under the tab bar
	// Header, then rows 0.. : row 3 is a match that is not selected.
	if !strings.HasPrefix(lines[4], marker) {
		test.Errorf("row 3 = %q, want it to start with the styled marker %q", lines[4], marker)
	}
	if strings.Contains(lines[1], search.Glyph) || strings.Contains(lines[3], search.Glyph) {
		test.Error("rows that do not match carry the marker")
	}
	if !strings.HasPrefix(ansi.Strip(lines[2]), search.SelectedGlyph) {
		test.Errorf("selected matching row = %q, want the cursor and the match glyph", ansi.Strip(lines[2]))
	}
	if strings.Contains(lines[2], marker) {
		test.Error("the selected row draws the marker in the accent colour, which vanishes on its background")
	}
	press(view, "j") // row 2 does not match
	if line := ansi.Strip(strings.Split(view.Render(80, 13), "\n")[4]); !strings.HasPrefix(line, "> ") {
		test.Errorf("selected row without a match = %q", line)
	}
	press(view, "k")
	press(view, "esc")
	if strings.Contains(ansi.Strip(view.Render(80, 13)), search.Glyph) {
		test.Error("marks survive esc")
	}
}

func TestHelpListsTheSearchKeys(test *testing.T) {
	var got []string
	for _, group := range searchView(test).FullHelp() {
		for _, binding := range group {
			got = append(got, binding.Help().Key)
		}
	}
	if strings.Join(got, " ") != "gt gT / n N" {
		test.Errorf("help keys = %q", got)
	}
}

func TestRestoreFollowsThePullRequestWhenRowsChange(test *testing.T) {
	client := fakeWith(searchRows())
	view := loadedView(test, client, nil)
	press(view, "j")
	press(view, "j") // row 2, "Tidy docs" (number 4)
	press(view, "/")
	typeSearch(view, "fix")
	// A new pull request arrives on top while the prompt is open.
	fresh := append([]github.PullRequest{{Repository: testRepository, Number: 99, Title: "New", Author: "al"}}, searchRows()...)
	client.SetPullRequests(testRepository, fresh)
	deliver(test, view, press(view, "R"))
	finishSearch(view, "fix", userinterface.PromptCancelled)
	if pullRequest, _ := view.current().selected(); pullRequest.Title != "Tidy docs" {
		test.Errorf("esc restored %q, want Tidy docs", pullRequest.Title)
	}
}

func TestSubmitJumpsToTheFirstMatchAtOrAfterTheOrigin(test *testing.T) {
	view := searchView(test)
	press(view, "j")
	press(view, "j") // row 2
	press(view, "/")
	// A paste is reported as one change; nothing moved the cursor yet.
	finishSearch(view, "fix", userinterface.PromptSubmitted)
	if got := view.current().window.Cursor(); got != 3 {
		test.Errorf("cursor = %d, want 3", got)
	}
}

func TestWhitespaceQueryIsNoSearch(test *testing.T) {
	view := searchView(test)
	press(view, "/")
	typeSearch(view, "  ")
	messages := finishSearch(view, "  ", userinterface.PromptSubmitted)
	if view.current().search.Active() {
		test.Error("a blank query is an active search")
	}
	for _, message := range messages {
		if _, ok := message.(userinterface.InfoMessage); ok {
			test.Errorf("blank query produced %v", message)
		}
	}
}

func TestEnterOnAnEmptyPromptRepeatsTheLastSearch(test *testing.T) {
	view := searchView(test)
	finishSearch(view, "", userinterface.PromptSubmitted)
	if view.current().search.Active() {
		test.Fatal("an empty prompt with no previous search started one")
	}
	press(view, "/")
	typeSearch(view, "fix")
	finishSearch(view, "fix", userinterface.PromptSubmitted) // row 1
	press(view, "esc")
	press(view, "j") // row 2
	press(view, "/")
	finishSearch(view, "", userinterface.PromptSubmitted)
	if view.current().search.Query() != "fix" || !view.current().search.Marked(3) {
		test.Errorf("query = %q, marks not restored", view.current().search.Query())
	}
	if got := view.current().window.Cursor(); got != 3 {
		test.Errorf("cursor = %d, want the first match at or after row 2", got)
	}
}

func TestSearchFindsABranchName(test *testing.T) {
	rows := searchRows()
	rows[2].HeadBranch = "hotfix/login"
	rows[4].BaseBranch = "release/2.0"
	view := loadedView(test, fakeWith(rows), nil)
	press(view, "/")
	for _, testCase := range []struct {
		text string
		want int
	}{{"hotfix", 2}, {"release/2", 4}} {
		typeSearch(view, testCase.text)
		if got := view.current().window.Cursor(); got != testCase.want {
			test.Errorf("search %q: cursor = %d, want %d", testCase.text, got, testCase.want)
		}
	}
}
