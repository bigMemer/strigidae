package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/analysis"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/golden"
)

// lensed is an inliner with the test analysis loaded from a.json.
type lensed struct {
	*inliner
	files *files
}

func newLensed(test *testing.T, width, height int) *lensed {
	inliner := newInliner(test)
	inliner.send(tea.WindowSizeMsg{Width: width, Height: height})
	files := inliner.withFiles(map[string]string{
		"a.json":     analysisJSON("acme/widgets", 7, headSHA),
		"stale.json": analysisJSON("acme/widgets", 7, "old"),
	})
	lensed := &lensed{inliner: inliner, files: files}
	lensed.command("analysis", "a.json")
	return lensed
}

// command runs :name arguments.
func (lensed *lensed) command(name, arguments string) {
	lensed.test.Helper()
	command, handled := lensed.view.Command(name, arguments)
	if !handled {
		lensed.test.Fatalf(":%s not handled", name)
	}
	lensed.run(command)
}

// at reports the file and line the diff cursor is on.
func (lensed *lensed) at() (string, github.DiffSide, int) {
	line, ok := lensed.diffView().Cursor()
	if !ok {
		return lensed.view.selectedPath(), "", 0
	}
	return lensed.view.selectedPath(), line.Side, line.Number()
}

func TestReadingOrder(test *testing.T) {
	lens := analysis.Lens{
		Order: []string{"b"},
		Segments: []analysis.Segment{
			{Identifier: "a", Spans: []string{"x", "y", "z"}, Order: []string{"z"}},
			{Identifier: "b", Spans: []string{"y"}},
		},
	}
	want := []lensStop{{1, "y"}, {0, "z"}, {0, "x"}, {0, "y"}}
	got := readingOrder(lens)
	if len(got) != len(want) {
		test.Fatalf("stops = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			test.Errorf("stop %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLensNeedsAnAnalysis(test *testing.T) {
	inliner := newInliner(test)
	command, _ := inliner.view.Command("lens", "")
	inliner.run(command)
	if got := inliner.lastInfo(); got != "no analysis loaded: :analysis <path> loads one" {
		test.Errorf("notice = %q", got)
	}
	command, _ = inliner.view.Command("next-span", "")
	inliner.run(command)
	if got := inliner.lastInfo(); got != "no lens open: :lens opens one" {
		test.Errorf("next-span notice = %q", got)
	}
}

func TestLensWalksTheSpansInReadingOrder(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.command("lens", "")
	if !lensed.view.lensOpen() || lensed.view.focus != paneFiles {
		test.Fatal("lens not open in the focused left pane")
	}
	type place struct {
		path string
		side github.DiffSide
		line int
	}
	check := func(step string, want place) {
		test.Helper()
		path, side, line := lensed.at()
		if got := (place{path, side, line}); got != want {
			test.Errorf("%s: at %v, want %v", step, got, want)
		}
	}
	check("open", place{"internal/sync/client.go", github.SideRight, 14})
	lensed.command("next-span", "")
	// The far span is outside every hunk: the diff stays on its file, at the
	// closest line.
	if path, _, _ := lensed.at(); path != "internal/sync/client.go" {
		test.Errorf("far span: on %s", path)
	}
	lensed.press("j") // the next segment, in the lens's order: its first span is the old side
	check("j", place{"internal/sync/legacy.go", github.SideLeft, 1})
	lensed.command("next-span", "")
	check("next-span", place{"internal/sync/retry.go", github.SideRight, 1})
	lensed.command("next-span", "") // past the end stays
	check("past the end", place{"internal/sync/retry.go", github.SideRight, 1})
	lensed.press("g g")
	check("gg", place{"internal/sync/client.go", github.SideRight, 14})
	lensed.command("previous-span", "") // before the start stays
	check("before the start", place{"internal/sync/client.go", github.SideRight, 14})
	lensed.press("enter")
	if lensed.view.focus != paneRight {
		test.Error("enter did not focus the diff")
	}
}

func TestLensSwitchAndClose(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.command("lens", "claims")
	if screen := lensed.screen(); !strings.Contains(screen, "Lens 2/2: Claims") {
		test.Errorf("claims lens not shown:\n%s", screen)
	}
	lensed.command("lens", "nope")
	if got := lensed.lastInfo(); got != `no lens "nope"; lenses: flow, claims` {
		test.Errorf("notice = %q", got)
	}
	lensed.command("lens", "")
	if lensed.view.lensOpen() || !strings.Contains(lensed.screen(), "Files 0/5 viewed") {
		test.Errorf("lens not closed:\n%s", lensed.screen())
	}
}

func TestLensFollowsAReloadedAnalysis(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.command("lens", "claims")
	lensed.files.content["fewer.json"] = strings.Replace(analysisJSON("acme/widgets", 7, headSHA), `"id": "claims"`, `"id": "other"`, 1)
	lensed.command("analysis", "fewer.json")
	if lensed.view.lensOpen() {
		test.Error("a lens gone from the reloaded analysis is still open")
	}
	lensed.command("lens", "flow")
	lensed.press("G")
	lensed.command("analysis", "a.json")
	if !lensed.view.lensOpen() || lensed.view.lens.cursor != len(lensed.view.lens.stops)-1 {
		test.Error("the open lens did not survive a reload of the same analysis")
	}
}

func TestLensKeysLeaveTheFilesSearchAlone(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.command("lens", "")
	lensed.press("/")
	if lensed.view.search.TakeRequest() {
		test.Error("/ started a Files search under the lens")
	}
}

func TestLensHostileText(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	evil := strings.NewReplacer(`"General Flow"`, `"Flow \u001b]0;pwned\u0007"`, `"Follow the call path"`, `"purpose \u001b[2J"`, `"the new call"`, `"label \u202e"`).
		Replace(analysisJSON("acme/widgets", 7, headSHA))
	lensed.files.content["evil.json"] = evil
	lensed.command("analysis", "evil.json")
	lensed.command("lens", "")
	rendered := lensed.render()
	for _, bad := range []string{"\x1b]0;", "\x1b[2J", "\u202e"} {
		if strings.Contains(rendered, bad) {
			test.Errorf("render contains %q", bad)
		}
	}
}

func TestLensScreens(test *testing.T) {
	test.Run("open", func(test *testing.T) {
		lensed := newLensed(test, 120, 24)
		lensed.command("lens", "")
		golden.Assert(test, "lens_open", lensed.render(), false)
	})
	test.Run("old side", func(test *testing.T) {
		lensed := newLensed(test, 120, 24)
		lensed.command("lens", "")
		lensed.press("j", "j")
		golden.Assert(test, "lens_old_side", lensed.render(), false)
	})
	test.Run("stale", func(test *testing.T) {
		lensed := newLensed(test, 120, 24)
		lensed.command("analysis", "stale.json")
		lensed.command("lens", "")
		golden.Assert(test, "lens_stale", lensed.render(), false)
	})
	test.Run("scrolled", func(test *testing.T) {
		lensed := newLensed(test, 120, 12)
		lensed.command("lens", "")
		lensed.press("G")
		golden.Assert(test, "lens_scrolled", lensed.render(), false)
	})
	test.Run("narrow", func(test *testing.T) {
		lensed := newLensed(test, 60, 20)
		lensed.command("lens", "")
		golden.Assert(test, "lens_narrow", lensed.render(), false)
	})
}

func TestLensScrollsBackToTheTop(test *testing.T) {
	lensed := newLensed(test, 120, 12) // 6 rows for the lens: less than it has
	lensed.command("lens", "")
	lensed.press("G")
	if screen := lensed.screen(); strings.Contains(screen, "Follow the call path") || !strings.Contains(screen, "The helper") {
		test.Fatalf("at the bottom the lens should show the last segment, not the purpose:\n%s", screen)
	}
	lensed.press("g g")
	if screen := lensed.screen(); !strings.Contains(screen, "Follow the call path") {
		test.Errorf("back at the first span, the purpose above it is not shown:\n%s", screen)
	}
	lensed.press("G", "k")
	if screen := lensed.screen(); !strings.Contains(screen, "The helper high risk") || !strings.Contains(screen, "Check the backoff") {
		test.Errorf("the segment's title and annotation are not shown above its first span:\n%s", screen)
	}
}

func TestLensKeysSwitchLens(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.press("g t")
	if lensed.view.lensOpen() {
		test.Fatal("gt opened a lens although none was open")
	}
	lensed.command("lens", "")
	for _, step := range []struct{ press, want string }{
		{"g t", "Lens 2/2: Claims"},
		{"g t", "Lens 1/2: General Flow"},
		{"g T", "Lens 2/2: Claims"},
	} {
		lensed.press(step.press)
		if screen := lensed.screen(); !strings.Contains(screen, step.want) {
			test.Errorf("after %s: want %q:\n%s", step.press, step.want, screen)
		}
	}
	if path, _, line := lensed.at(); path != "internal/sync/client.go" || line != 14 {
		test.Errorf("switching lens did not go to its first span: %s:%d", path, line)
	}
	lensed.press("l", "g t") // from the diff pane too
	if !strings.Contains(lensed.screen(), "Lens 1/2: General Flow") {
		test.Error("gt from the diff pane did not switch lens")
	}
}

func TestLensMarksTheSpanInTheDiff(test *testing.T) {
	lensed := newLensed(test, 120, 30)
	lensed.command("lens", "")
	marked := func() []string {
		var lines []string
		for _, line := range strings.Split(lensed.screen(), "\n") {
			if strings.Contains(line, "┃") {
				lines = append(lines, line)
			}
		}
		return lines
	}
	if got := marked(); len(got) == 0 {
		test.Fatalf("no line of the span is marked:\n%s", lensed.screen())
	}
	lensed.command("lens", "")
	if got := marked(); len(got) != 0 {
		test.Errorf("closing the lens left marks:\n%s", strings.Join(got, "\n"))
	}
}

func TestLensPutsTheSpanAtTheTopOfTheDiff(test *testing.T) {
	lensed := newLensed(test, 120, 16)
	lensed.command("lens", "")
	diffView := lensed.diffView()
	if diffView.CursorRow() != diffView.Top() {
		test.Errorf("cursor row %d, top row %d: the span's first line is not at the top", diffView.CursorRow(), diffView.Top())
	}
	if line, ok := diffView.Cursor(); !ok || line.Number() != 14 {
		test.Errorf("cursor on %+v, want line 14", line)
	}
}
