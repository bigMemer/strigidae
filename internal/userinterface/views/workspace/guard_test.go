package workspace

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/github/fake"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
)

// writeKinds is every kind of write, so a kind added without a row in the
// tables below fails TestGuardTableCoversEveryKind.
var writeKinds = []writeKind{writeComment, writeOverviewComment, writeThread, writeReaction, writeReview, writeMerge, writeDiscard, writePullRequest}

// pluginKinds are the guard kinds of the plugin commands: they write nothing,
// so busy states do not block them.
var pluginKinds = []writeKind{usePluginLines, usePluginPullRequest}

// busyCase is one busy state: how to start it, what it is called and the
// notice it gives; saving says whether it counts as a comment write in flight.
type busyCase struct {
	name   string
	apply  func(inliner *inliner)
	notice string
	saving bool
}

func busyCases() []busyCase {
	return []busyCase{
		{"review submitting", func(inliner *inliner) { inliner.view.review.submitting = true }, "a review is being submitted", false},
		{"merge in progress", func(inliner *inliner) { inliner.view.merge.merging = true }, "a merge is in progress", false},
		{"comment saving", func(inliner *inliner) { inliner.view.writes = 1 }, commentSavingText, true},
	}
}

// mutations counts the writes the fake has seen.
func (inliner *inliner) mutations() int {
	writing := []string{
		fake.MethodStartReview, fake.MethodAddReviewThread, fake.MethodUpdateReviewComment, fake.MethodDeleteReviewComment,
		fake.MethodDeleteReview, fake.MethodReplyToThread, fake.MethodAddComment, fake.MethodSubmitReview,
		fake.MethodMergePullRequest, fake.MethodAddReaction, fake.MethodRemoveReaction,
		fake.MethodResolveReviewThread, fake.MethodUnresolveReviewThread,
		fake.MethodUpdatePullRequest,
	}
	count := 0
	for _, call := range inliner.client.Calls() {
		if slices.Contains(writing, call.Method) {
			count++
		}
	}
	return count
}

// guardedInliner is an inliner with a published comment of the viewer's seeded.
func guardedInliner(test *testing.T) *inliner {
	inliner := newInliner(test)
	inliner.seed(inliner.mine(true, true))
	inliner.body = "text"
	return inliner
}

func (inliner *inliner) onOwnComment() { inliner.toCommentOf(mineIdentifier, 0) }

func (inliner *inliner) onDiffLine() { inliner.moveTo(github.SideRight, 14) }

// infoText is the text of an info notice, or "" for any other message.
func infoText(message tea.Msg) string {
	if info, ok := message.(userinterface.InfoMessage); ok {
		return info.Text
	}
	return ""
}

// guardAction is one action that writes, started at the key or command.
type guardAction struct {
	name  string
	kind  writeKind
	place func(inliner *inliner) // puts the cursor where the action has its target
	start func(inliner *inliner) // runs the action
}

func command(name, arguments string) func(inliner *inliner) {
	return func(inliner *inliner) {
		inliner.test.Helper()
		started, ok := inliner.view.Command(name, arguments)
		if !ok {
			inliner.test.Fatalf("command %q not handled", name)
		}
		inliner.run(started)
	}
}

func press(sequence string) func(inliner *inliner) {
	return func(inliner *inliner) { inliner.test.Helper(); inliner.press(sequence) }
}

func guardActions() []guardAction {
	return []guardAction{
		{"c on a diff line", writeComment, (*inliner).onDiffLine, press("c")},
		{"C on the Overview", writeOverviewComment, func(inliner *inliner) { inliner.press("h", "g g") }, press("C")},
		{"V", writeComment, (*inliner).onDiffLine, press("V")},
		{"r", writeComment, (*inliner).onOwnComment, press("r")},
		{"e", writeComment, (*inliner).onOwnComment, press("e")},
		{"D", writeComment, (*inliner).onOwnComment, press("D")},
		{":edit-comment", writeComment, (*inliner).onOwnComment, command("edit-comment", "")},
		{":delete-comment", writeComment, (*inliner).onOwnComment, command("delete-comment", "")},
		{"x", writeThread, (*inliner).onOwnComment, press("x")},
		{":resolve", writeThread, (*inliner).onOwnComment, command("resolve", "")},
		{":unresolve", writeThread, (*inliner).onOwnComment, command("unresolve", "")},
		{":react", writeReaction, (*inliner).onOwnComment, command("react", "")},
		{":react heart", writeReaction, (*inliner).onOwnComment, command("react", "heart")},
		{":approve", writeReview, func(*inliner) {}, command("approve", "")},
		{":request-changes", writeReview, func(*inliner) {}, command("request-changes", "")},
		{":review", writeReview, func(*inliner) {}, command("review", "")},
		{":merge", writeMerge, func(*inliner) {}, command("merge", "")},
		{":discard-review", writeDiscard, func(*inliner) {}, command("discard-review", "")},
		{":edit-pull-request", writePullRequest, func(*inliner) {}, command("edit-pull-request", "")},
	}
}

func TestGuardTableCoversEveryKind(test *testing.T) {
	for _, kind := range writeKinds {
		if _, ok := guardTable[kind]; !ok {
			test.Errorf("write kind %d has no row in the guard table", kind)
		}
		if !slices.ContainsFunc(guardActions(), func(action guardAction) bool { return action.kind == kind }) {
			test.Errorf("write kind %d has no action in the tests", kind)
		}
	}
	for _, kind := range pluginKinds {
		if _, ok := guardTable[kind]; !ok {
			test.Errorf("plugin kind %d has no row in the guard table", kind)
		}
	}
	if len(guardTable) != len(writeKinds)+len(pluginKinds) {
		test.Errorf("guard table has %d rows for %d kinds", len(guardTable), len(writeKinds)+len(pluginKinds))
	}
}

// A plugin command needs the pull request to have loaded (and, for lines, the
// diff), and nothing else.
func TestGuardLetsPluginsThroughBusyStates(test *testing.T) {
	busy := guardState{loaded: true, diffReady: true, submittingReview: true, mergeInProgress: true, commentSaving: true}
	for _, kind := range pluginKinds {
		if notice := busy.admit(kind); notice != nil {
			test.Errorf("plugin kind %d blocked while busy: %q", kind, infoText(notice()))
		}
		if notice := (guardState{}).admit(kind); infoText(notice()) != "pull request not loaded yet" {
			test.Errorf("plugin kind %d before the pull request loaded: %q", kind, infoText(notice()))
		}
	}
	if notice := (guardState{loaded: true}).admit(usePluginLines); infoText(notice()) != "diff not loaded" {
		test.Errorf("lines plugin without a diff: %q", infoText(notice()))
	}
	if notice := (guardState{loaded: true}).admit(usePluginPullRequest); notice != nil {
		test.Errorf("pull request plugin without a diff: %q", infoText(notice()))
	}
}

// The table itself: every kind, every state.
func TestGuardStateAnswersPerKind(test *testing.T) {
	ready := guardState{loaded: true, diffReady: true}
	for _, kind := range writeKinds {
		if notice := ready.admit(kind); notice != nil {
			test.Errorf("kind %d refused in the ready, idle state", kind)
		}
		if notice := (guardState{diffReady: true}).admit(kind); notice == nil {
			test.Errorf("kind %d admitted before the pull request loaded", kind)
		}
	}
	tests := []struct {
		name  string
		state guardState
		want  string
	}{
		{"not loaded", guardState{diffReady: true}, "pull request not loaded yet"},
		{"not loaded wins over diff", guardState{}, "pull request not loaded yet"},
		{"review submitting", guardState{loaded: true, diffReady: true, submittingReview: true}, "a review is being submitted"},
		{"merge in progress", guardState{loaded: true, diffReady: true, mergeInProgress: true}, "a merge is in progress"},
		{"comment saving", guardState{loaded: true, diffReady: true, commentSaving: true}, commentSavingText},
		{"review before merge before comment", guardState{loaded: true, diffReady: true, submittingReview: true, mergeInProgress: true, commentSaving: true}, "a review is being submitted"},
	}
	for _, kind := range writeKinds {
		notice := (guardState{loaded: true}).admit(kind)
		if guardTable[kind].needsDiff != (notice != nil) {
			test.Errorf("kind %d: needsDiff = %v but the diff-less state gave %v", kind, guardTable[kind].needsDiff, notice != nil)
		} else if notice != nil && infoText(notice()) != "diff not loaded" {
			test.Errorf("kind %d: notice %q, want diff not loaded", kind, infoText(notice()))
		}
	}
	for _, testCase := range tests {
		for _, kind := range writeKinds {
			notice := testCase.state.admit(kind)
			if notice == nil {
				test.Errorf("%s, kind %d: admitted", testCase.name, kind)
				continue
			}
			if diff := cmp.Diff(infoText(notice()), testCase.want); diff != "" {
				test.Errorf("%s, kind %d (-got +want):\n%s", testCase.name, kind, diff)
			}
		}
	}
}

// At the key: ready, then busy, for each action in each state.
func TestGuardAtTheKey(test *testing.T) {
	notLoaded := busyCase{"pull request not loaded", func(inliner *inliner) { inliner.view.detail = nil }, "pull request not loaded yet", false}
	diffLoading := busyCase{"diff loading", func(inliner *inliner) { inliner.view.diffPending = true }, "diff not loaded", false}
	states := append([]busyCase{notLoaded, diffLoading}, busyCases()...)
	for _, action := range guardActions() {
		for _, state := range states {
			if state.name == diffLoading.name && !guardTable[action.kind].needsDiff {
				continue // asserted in TestGuardWithoutADiff
			}
			test.Run(action.name+" while "+state.name, func(test *testing.T) {
				inliner := guardedInliner(test)
				action.place(inliner)
				state.apply(inliner)
				before, writes := inliner.mutations(), inliner.view.writes
				inliner.openedOn, inliner.messages = nil, nil
				action.start(inliner)
				if diff := cmp.Diff([]string{state.notice}, inliner.infoMessages()); diff != "" {
					test.Errorf("notices (-want +got):\n%s", diff)
				}
				if _, asked := inliner.lastConfirm(); asked {
					test.Error("a question was asked")
				}
				if len(inliner.openedOn) != 0 || inliner.view.review.chooser != nil || inliner.view.picker != nil || inliner.view.visualActive() {
					test.Error("an editor, chooser, picker or selection opened")
				}
				if inliner.mutations() != before || inliner.view.writes != writes {
					test.Errorf("something was written: %d -> %d mutations, writes %d -> %d", before, inliner.mutations(), writes, inliner.view.writes)
				}
			})
		}
	}
}

// The order: not loaded, then the target, then busy.
func TestGuardOrder(test *testing.T) {
	test.Run("r while the diff loads says the diff is not loaded", func(test *testing.T) {
		inliner := guardedInliner(test)
		inliner.view.diffPending = true
		inliner.messages = nil
		inliner.press("r")
		if diff := cmp.Diff([]string{"diff not loaded"}, inliner.infoMessages()); diff != "" {
			test.Errorf("(-want +got):\n%s", diff)
		}
	})
	test.Run("the focus notice comes before the saving one", func(test *testing.T) {
		inliner := guardedInliner(test)
		inliner.onOwnComment()
		inliner.press("h") // a narrow terminal shows one pane; fake it by sizing
		inliner.send(tea.WindowSizeMsg{Width: 60, Height: 30})
		inliner.view.writes = 1
		inliner.messages = nil
		inliner.press("r")
		if diff := cmp.Diff([]string{"focus the diff to reply"}, inliner.infoMessages()); diff != "" {
			test.Errorf("(-want +got):\n%s", diff)
		}
	})
	test.Run("no thread here comes before busy", func(test *testing.T) {
		inliner := guardedInliner(test)
		inliner.moveTo(github.SideRight, 15) // no thread on this line
		inliner.view.writes = 1
		inliner.messages = nil
		inliner.press("r")
		if diff := cmp.Diff([]string{"no comment thread here"}, inliner.infoMessages()); diff != "" {
			test.Errorf("(-want +got):\n%s", diff)
		}
	})
}

// readmission is a point where an action is let in again: what it leaves
// unrun after its first step, to run once a busy state has started.
type readmission struct {
	name  string
	kind  writeKind
	setup func(inliner *inliner) tea.Cmd
}

func askedYes(inliner *inliner) tea.Cmd {
	inliner.test.Helper()
	confirmation, asked := inliner.lastConfirm()
	if !asked {
		inliner.test.Fatalf("no question asked; messages %v", inliner.messages)
	}
	return confirmation.OnYes
}

func readmissions() []readmission {
	return []readmission{
		{"yes to a comment delete", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.addPending("draft")
			inliner.messages = nil
			inliner.press("D")
			return askedYes(inliner)
		}},
		{"yes to a published comment edit", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.onOwnComment()
			inliner.messages = nil
			inliner.press("e")
			return askedYes(inliner)
		}},
		{"yes to a top-level comment", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.press("h", "g g")
			inliner.messages = nil
			inliner.press("C")
			return askedYes(inliner)
		}},
		{"yes to a reply", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.onOwnComment()
			inliner.messages = nil
			inliner.press("r")
			return askedYes(inliner)
		}},
		{"re-read before the editor opens", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.addPending("draft")
			_, read := inliner.view.Update(keys.Message{Sequence: "e"})
			return read
		}},
		{"editor closed on an inline comment", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.onDiffLine()
			_, compose := inliner.view.Update(keys.Message{Sequence: "c"})
			return func() tea.Msg { return compose() }
		}},
		{"editor closed on a comment edit", writeComment, func(inliner *inliner) tea.Cmd {
			inliner.addPending("draft")
			inliner.body = "changed"
			_, read := inliner.view.Update(keys.Message{Sequence: "e"})
			_, compose := inliner.view.Update(read())
			return compose
		}},
		{"yes to a resolve", writeThread, func(inliner *inliner) tea.Cmd {
			inliner.onOwnComment()
			inliner.messages = nil
			inliner.press("x")
			return askedYes(inliner)
		}},
		{"yes to a discard", writeDiscard, func(inliner *inliner) tea.Cmd {
			inliner.addPending("draft")
			return inliner.askDiscard().OnYes
		}},
		{"yes to a review", writeReview, func(inliner *inliner) tea.Cmd {
			inliner.messages = nil
			inliner.command("approve")
			return askedYes(inliner)
		}},
		{"re-read before the pull request editor opens", writePullRequest, func(inliner *inliner) tea.Cmd {
			read, _ := inliner.view.Command("edit-pull-request", "")
			return read
		}},
		{"editor closed on a pull request edit", writePullRequest, func(inliner *inliner) tea.Cmd {
			inliner.body = "Changed\n\nbody"
			read, _ := inliner.view.Command("edit-pull-request", "")
			_, opened := inliner.view.Update(read())
			return opened
		}},
		{"yes to a pull request edit", writePullRequest, func(inliner *inliner) tea.Cmd {
			inliner.body = "Changed\n\nbody"
			inliner.messages = nil
			inliner.command("edit-pull-request")
			return askedYes(inliner)
		}},
		{"yes to a merge", writeMerge, func(inliner *inliner) tea.Cmd {
			inliner.messages = nil
			inliner.command("merge")
			return askedYes(inliner)
		}},
	}
}

// At the re-admission point: a busy state that started since is refused with
// its notice and nothing is written. (:react's re-admission is choosing in the
// picker: TestGuardAtReactionPickerChoice.)
func TestGuardAtReadmission(test *testing.T) {
	for _, point := range readmissions() {
		for _, state := range busyCases() {
			test.Run(point.name+" while "+state.name, func(test *testing.T) {
				inliner := guardedInliner(test)
				next := point.setup(inliner)
				if next == nil {
					test.Fatal("nothing left to run")
				}
				state.apply(inliner)
				before, writes := inliner.mutations(), inliner.view.writes
				inliner.openedOn, inliner.messages = nil, nil
				inliner.run(next)
				if diff := cmp.Diff([]string{state.notice}, inliner.infoMessages()); diff != "" {
					test.Errorf("notices (-want +got):\n%s", diff)
				}
				if _, asked := inliner.lastConfirm(); asked {
					test.Error("a question was asked")
				}
				if len(inliner.openedOn) != 0 {
					test.Errorf("an editor opened on %v", inliner.openedOn)
				}
				if inliner.mutations() != before || inliner.view.writes != writes {
					test.Errorf("something was written: %d -> %d mutations, writes %d -> %d", before, inliner.mutations(), writes, inliner.view.writes)
				}
				if inliner.view.review.submitting != (state.name == "review submitting") || inliner.view.merge.merging != (state.name == "merge in progress") {
					test.Error("the refusal changed a busy state")
				}
			})
		}
	}
}

// Choosing a reaction in the picker is a re-admission point: a review or a
// merge that started while the picker was open refuses it.
func TestGuardAtReactionPickerChoice(test *testing.T) {
	for _, state := range busyCases() {
		test.Run(state.name, func(test *testing.T) {
			inliner := guardedInliner(test)
			inliner.onOwnComment()
			command("react", "")(inliner)
			if inliner.view.picker == nil {
				test.Fatal("the picker did not open")
			}
			state.apply(inliner)
			before, writes := inliner.mutations(), inliner.view.writes
			inliner.messages = nil
			inliner.keyPress(tea.KeyEnter)
			if diff := cmp.Diff([]string{state.notice}, inliner.infoMessages()); diff != "" {
				test.Errorf("notices (-want +got):\n%s", diff)
			}
			if inliner.mutations() != before || inliner.view.writes != writes {
				test.Errorf("something was written: %d -> %d mutations, writes %d -> %d", before, inliner.mutations(), writes, inliner.view.writes)
			}
		})
	}
}

// noDiff is a diff state as load.go leaves it: a load in flight (nothing parsed
// yet), or one that failed (too large, nothing parsed).
type noDiff struct {
	name  string
	apply func(inliner *inliner)
}

func noDiffStates() []noDiff {
	return []noDiff{
		{"diff loading", func(inliner *inliner) {
			inliner.view.diff = DiffState{Loading: true}
			inliner.view.diffPending = true
		}},
		{"diff failed", func(inliner *inliner) {
			inliner.view.diff = DiffState{Err: github.ErrDiffTooLarge}
			inliner.view.diffPending = false
		}},
	}
}

// removeDiff leaves the pull request loaded and the diff absent.
func (state noDiff) removeDiff(inliner *inliner) {
	state.apply(inliner)
	inliner.view.filePane.SetDiff(inliner.view.diff)
	inliner.view.layout()
	if inliner.view.detail == nil || inliner.view.diff.Parsed != nil || inliner.view.guardState().diffReady {
		inliner.test.Fatal("want the pull request loaded and no diff")
	}
	inliner.messages, inliner.openedOn = nil, nil
}

// The top-level comment and :discard-review need only the pull request: with
// no diff (still loading, or failed) they run through their yes, while
// everything on the diff, the review verdicts and :merge say the diff is not
// loaded.
func TestGuardWithoutADiff(test *testing.T) {
	for _, state := range noDiffStates() {
		test.Run(state.name+" top-level comment is posted", func(test *testing.T) {
			inliner := guardedInliner(test)
			inliner.press("h", "g g")
			state.removeDiff(inliner)
			inliner.press("C")
			if len(inliner.openedOn) != 1 {
				test.Fatalf("editor opened %d times, notices %v", len(inliner.openedOn), inliner.infoMessages())
			}
			inliner.run(askedYes(inliner))
			if got := inliner.methods(fake.MethodAddComment); len(got) != 1 {
				test.Errorf("AddComment calls = %v, notices %v", got, inliner.infoMessages())
			}
		})
		test.Run(state.name+" :discard-review deletes", func(test *testing.T) {
			inliner := guardedInliner(test)
			inliner.addPending("draft")
			state.removeDiff(inliner)
			inliner.run(inliner.askDiscard().OnYes)
			if got := inliner.discardCalls(); len(got) != 1 {
				test.Errorf("DeleteReview calls = %v, notices %v", got, inliner.infoMessages())
			}
		})
		for _, action := range guardActions() {
			if !guardTable[action.kind].needsDiff {
				continue
			}
			test.Run(state.name+" "+action.name+" says the diff is not loaded", func(test *testing.T) {
				inliner := guardedInliner(test)
				action.place(inliner)
				state.removeDiff(inliner)
				action.start(inliner)
				if diff := cmp.Diff([]string{"diff not loaded"}, inliner.infoMessages()); diff != "" {
					test.Errorf("(-want +got):\n%s", diff)
				}
			})
		}
	}
}

// postKind picks the table row of a post's yes: a reply is a diff comment, a
// top-level comment needs no diff.
func TestPostKind(test *testing.T) {
	tests := []struct {
		name   string
		target target
		want   writeKind
	}{
		{"reply", target{kind: kindReply}, writeComment},
		{"top-level comment", target{}, writeOverviewComment},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			if got := postKind(testCase.target); got != testCase.want {
				test.Errorf("postKind = %d, want %d", got, testCase.want)
			}
		})
	}
	if guardTable[writeComment].needsDiff == guardTable[writeOverviewComment].needsDiff {
		test.Error("the two comment kinds no longer differ in what they need loaded")
	}
}
