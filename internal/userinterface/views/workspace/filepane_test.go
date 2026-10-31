package workspace

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// stubPane records what the workspace tells a FilePane.
type stubPane struct {
	files []string
	sizes [][2]int
	keys  []string
}

func (pane *stubPane) SetTheme(theme.Theme) {}

func (pane *stubPane) Context() diffview.Context     { return diffview.Context{} }
func (pane *stubPane) GotoLine(github.DiffSide, int) {}
func (pane *stubPane) SetSpan(diffview.Range)        {}
func (pane *stubPane) VisualStart() bool             { return false }
func (pane *stubPane) VisualActive() bool            { return false }
func (pane *stubPane) VisualMove(int) error          { return nil }
func (pane *stubPane) VisualEnd()                    {}

func (pane *stubPane) SetDiff(DiffState) {}

func (pane *stubPane) StartSearch() tea.Cmd                                   { return nil }
func (pane *stubPane) TakeSearchRequest() bool                                { return false }
func (pane *stubPane) SearchPrompt(userinterface.PromptResultMessage) tea.Cmd { return nil }
func (pane *stubPane) SearchStep(keys.Message, bool, bool) tea.Cmd            { return nil }
func (pane *stubPane) ClearSearch()                                           {}
func (pane *stubPane) SetHighlightSearch(bool)                                {}

func (pane *stubPane) SetWrap(bool)                               {}
func (pane *stubPane) SetHideResolved(bool)                       {}
func (pane *stubPane) SetSkipResolved(bool)                       {}
func (pane *stubPane) SetRelativeNumber(bool)                     {}
func (pane *stubPane) SetHunkReviewed(func(*diff.File, int) bool) {}

func (pane *stubPane) SetHunkHiding(func(*diff.File, int) bool, func(*diff.File, int) int) {}
func (pane *stubPane) HiddenHunksChanged()                                                 {}
func (pane *stubPane) SetThreadFade(func(string) (int, bool))                              {}
func (pane *stubPane) ThreadFadesChanged()                                                 {}

func (pane *stubPane) SetThreadExpanded(string, bool) {}

func (pane *stubPane) SetThreads([]github.ReviewThread) {}

func (pane *stubPane) SetFile(file github.ChangedFile) { pane.files = append(pane.files, file.Path) }

func (pane *stubPane) SetSize(width, height int) {
	pane.sizes = append(pane.sizes, [2]int{width, height})
}

func (pane *stubPane) Update(message keys.Message) tea.Cmd {
	pane.keys = append(pane.keys, message.Sequence)
	return nil
}

func (pane *stubPane) Render(int, int) string { return "stub" }

func (pane *stubPane) FullHelp() [][]key.Binding {
	return [][]key.Binding{{
		key.NewBinding(key.WithKeys("]")), key.NewBinding(key.WithKeys("z h")),
	}}
}

func TestSetFileOnChangedMetadata(test *testing.T) {
	harness := loaded(test, 100, 30)
	pane := &recordPane{}
	harness.view.filePane = pane
	harness.press("j")
	if len(pane.files) != 1 {
		test.Fatalf("SetFile calls = %v", pane.files)
	}
	detail := testDetail()
	detail.Files[0].Additions = 99
	harness.send(pullRequestLoadedMessage{owner: harness.view, repository: testRepository, number: 7, sequence: harness.view.sequence, detail: detail})
	if len(pane.files) != 2 || pane.files[1].Additions != 99 || pane.files[1].Path != pane.files[0].Path {
		test.Errorf("pane did not get the new metadata: %+v", pane.files)
	}
	harness.send(pullRequestLoadedMessage{owner: harness.view, repository: testRepository, number: 7, sequence: harness.view.sequence, detail: detail})
	if len(pane.files) != 2 {
		test.Errorf("unchanged file repeated: %+v", pane.files)
	}
}

type recordPane struct {
	stubPane
	files []github.ChangedFile
}

func (pane *recordPane) SetFile(file github.ChangedFile) { pane.files = append(pane.files, file) }

func TestFilePaneContractThroughRoot(test *testing.T) {
	client := fake.New()
	client.SetPullRequest(testDetail())
	client.SetDiff(testRepository, 7, readDiff(test))
	pane := &stubPane{}
	view := New(client, testRepository, 7, WithTheme(theme.New(true)), WithFilePane(pane))
	noTick := func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	model := userinterface.New(view, theme.New(true), userinterface.Options{Tick: noTick, ViewCommands: Commands()})
	testHarness := &harness{test: test, view: view, client: client}

	step := func(message tea.Msg) { _, _ = model.Update(message) }
	keyPress := func(key string) { step(tea.KeyPressMsg{Code: rune(key[0]), Text: key}) }
	step(tea.WindowSizeMsg{Width: 100, Height: 25})
	testHarness.run(view.Init())

	if len(pane.sizes) == 0 {
		test.Fatal("SetSize was never called")
	}
	last := pane.sizes[len(pane.sizes)-1]
	step(tea.WindowSizeMsg{Width: 120, Height: 30})
	if got := pane.sizes[len(pane.sizes)-1]; got == last {
		test.Errorf("resize did not change SetSize: %v", got)
	}

	keyPress("}") // selects the first file
	keyPress("l") // focus the right pane
	keyPress("]")
	keyPress("z")
	keyPress("h")
	keyPress("j")
	if diff := cmp.Diff([]string{"]", "z h", "j"}, pane.keys); diff != "" {
		test.Errorf("keys reaching the pane (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"internal/sync/client.go"}, pane.files); diff != "" {
		test.Fatalf("SetFile calls (-want +got):\n%s", diff)
	}

	// Refresh with the same file selected must not call SetFile again.
	testHarness.press("R")
	if len(pane.files) != 1 {
		test.Errorf("SetFile called on refresh: %v", pane.files)
	}
	if view.selectedPath() != "internal/sync/client.go" {
		test.Errorf("selection lost: %q", view.selectedPath())
	}
}
