package workspace

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
)

// freshCheck says what an action re-reads before it asks or writes, so it
// never acts on a stale screen. The check itself runs in the command the
// action returns; the policy is declared here so the table shows it.
type freshCheck int

const (
	freshNone   freshCheck = iota // nothing to re-read
	freshTarget                   // re-read the target (the comment, the thread, the pending review, the pull request)
	freshHead                     // check that the head commit is still the one on screen
)

// invocation is how an entry was reached: by a key, which carries its count,
// or by a command, which carries its arguments. target is filled in by the
// dispatcher for the levels the resolver handles.
type invocation struct {
	message   keys.Message
	arguments string
	target    resolvedTarget
}

// actionEntry is one workspace action: the generic part (name, keys, command) in
// keys.Action, and what only the workspace knows around it. Key dispatch and
// command dispatch both read the same entry, so a key and its command always
// do the same thing.
//
// The dispatcher (invoke) runs the steps in a fixed order: check the
// arguments, ready (the guard table), the diff pane on screen, the target, then
// busy (the guard table), then run.
type actionEntry struct {
	keys.Action
	// level is what the action works on. The resolver finds lines, threads and
	// comments; the others are for the table, and run finds its own.
	level level
	// words finish the notices (see request.words); rule narrows a comment.
	words string
	rule  *commentRule
	// diffVisible: the diff pane must be on screen, focused or not. It is
	// per action, not per level.
	diffVisible bool
	// guard is the kind of write, checked by the one guard table, or writeNone.
	guard writeKind
	// fresh is the re-read policy; see freshCheck.
	fresh freshCheck
	// applies, if set, says whether a key reaches the entry in the view's
	// current state; otherwise the key falls through to what is behind it.
	applies func(*View) bool
	// check refuses bad arguments before anything else; nil accepts all.
	check func(arguments string) tea.Cmd
	// run does the action. It is nil for the keys that only mean something
	// while a line selection is open (see visualKey).
	run func(view *View, call invocation) tea.Cmd
}

// onOverview and inFile say where the section and selection keys apply.
func onOverview(view *View) bool { return view.selection() == 0 }

func inFile(view *View) bool { return view.selection() > 0 }

// laidOut wraps an action that changes what is on screen, so the panes are
// laid out again after it.
func laidOut(run func(view *View, call invocation)) func(*View, invocation) tea.Cmd {
	return func(view *View, call invocation) tea.Cmd {
		run(view, call)
		view.layout()
		return nil
	}
}

// entries is the registry: every workspace action, once. Movement keys come
// from keys.Global and the diff pane's from diffBindings. The order is the
// help order of the bindings, and the order keys are tried in.
func (workspaceBindings *bindings) entries() []actionEntry {
	editRule, deleteRule := commentRuleFor(changeEdit), commentRuleFor(changeDelete)
	return []actionEntry{
		{
			Action: keys.Action{Name: "focus-files-pane", Binding: &workspaceBindings.PaneLeft},
			run:    laidOut(func(view *View, _ invocation) { view.focus = paneFiles }),
		},
		{
			Action: keys.Action{Name: "focus-right-pane", Binding: &workspaceBindings.PaneRight},
			run:    laidOut(func(view *View, _ invocation) { view.focus = paneRight }),
		},
		{
			Action: keys.Action{Name: "next-file", Binding: &workspaceBindings.NextFile},
			run:    laidOut(func(view *View, call invocation) { view.stepFile(call.message.Repeat()) }),
		},
		{
			Action: keys.Action{Name: "previous-file", Binding: &workspaceBindings.PreviousFile},
			run:    laidOut(func(view *View, call invocation) { view.stepFile(-call.message.Repeat()) }),
		},
		{
			Action: keys.Action{Name: "file-picker", Binding: &workspaceBindings.FilePicker},
			run:    laidOut(func(view *View, _ invocation) { view.openPicker() }),
		},
		{
			Action: keys.Action{Name: "next-section", Binding: &workspaceBindings.NextSection}, applies: onOverview,
			run: laidOut(func(view *View, call invocation) {
				call.message.Each(view.overview.viewport.YOffset, func() { view.jumpSection(1) })
			}),
		},
		{
			Action: keys.Action{Name: "previous-section", Binding: &workspaceBindings.PreviousSection}, applies: onOverview,
			run: laidOut(func(view *View, call invocation) {
				call.message.Each(view.overview.viewport.YOffset, func() { view.jumpSection(-1) })
			}),
		},
		{
			Action: keys.Action{Name: "new-comment", Binding: &workspaceBindings.Comment},
			level:  levelLines, words: "comment on it", diffVisible: true, guard: writeComment, fresh: freshHead,
			run: func(view *View, call invocation) tea.Cmd { return view.inlineComment(call.target.lines) },
		},
		{
			Action: keys.Action{Name: "new-overview-comment", Binding: &workspaceBindings.OverviewComment},
			level:  levelPullRequest, guard: writeOverviewComment,
			run: func(view *View, _ invocation) tea.Cmd { return view.overviewComment() },
		},
		{
			Action: keys.Action{Name: "reply", Binding: &workspaceBindings.Reply},
			level:  levelThread, words: "reply", diffVisible: true, guard: writeComment,
			run: func(view *View, call invocation) tea.Cmd { return view.reply(call.target.thread) },
		},
		{
			Action: keys.Action{Name: "toggle-viewed", Binding: &workspaceBindings.Viewed},
			level:  levelFile,
			run:    func(view *View, _ invocation) tea.Cmd { return view.toggleViewed() },
		},
		{
			Action: keys.Action{Name: "toggle-hunk-reviewed", Binding: &workspaceBindings.ReviewHunk},
			level:  levelHunk, words: "mark the hunk", diffVisible: true,
			run: func(view *View, _ invocation) tea.Cmd { return view.toggleHunkReviewed() },
		},
		{
			Action: keys.Action{Name: "resolve-thread", Binding: &workspaceBindings.ResolveThread},
			level:  levelThread, words: "resolve it", diffVisible: true, guard: writeThread, fresh: freshTarget,
			run: func(view *View, call invocation) tea.Cmd {
				return view.resolveThread(resolveToggle, call.target.thread)
			},
		},
		{
			Action: keys.Action{Name: "start-visual-mode", Binding: &workspaceBindings.Visual}, applies: inFile,
			level: levelLines, words: "select lines", diffVisible: true, guard: writeComment,
			run: func(view *View, _ invocation) tea.Cmd { return view.startVisual() },
		},
		{
			Action: keys.Action{Name: "edit-comment", Binding: &workspaceBindings.EditComment, Command: "edit-comment"},
			level:  levelComment, words: "edit it", rule: editRule, diffVisible: true, guard: writeComment, fresh: freshTarget,
			run: func(view *View, call invocation) tea.Cmd { return view.editComment(call.target.comment) },
		},
		{
			Action: keys.Action{Name: "delete-comment", Binding: &workspaceBindings.DeleteComment, Command: "delete-comment"},
			level:  levelComment, words: "delete it", rule: deleteRule, diffVisible: true, guard: writeComment, fresh: freshTarget,
			run: func(view *View, call invocation) tea.Cmd { return view.deleteComment(call.target.comment) },
		},
		{Action: keys.Action{Name: "extend-selection-down", Binding: &workspaceBindings.VisualDown}},
		{Action: keys.Action{Name: "extend-selection-up", Binding: &workspaceBindings.VisualUp}},
		{Action: keys.Action{Name: "leave-visual-mode", Binding: &workspaceBindings.VisualExit}},

		// Commands without keys.
		{
			Action: keys.Action{Name: "resolve", Command: "resolve"},
			level:  levelThread, words: "resolve it", diffVisible: true, guard: writeThread, fresh: freshTarget,
			run: func(view *View, call invocation) tea.Cmd { return view.resolveThread(resolveOnly, call.target.thread) },
		},
		{
			Action: keys.Action{Name: "unresolve", Command: "unresolve"},
			level:  levelThread, words: "resolve it", diffVisible: true, guard: writeThread, fresh: freshTarget,
			run: func(view *View, call invocation) tea.Cmd {
				return view.resolveThread(unresolveOnly, call.target.thread)
			},
		},
		{
			Action: keys.Action{Name: "react", Command: "react"},
			level:  levelComment, words: "react to it", diffVisible: true, guard: writeReaction,
			check: checkReaction,
			run: func(view *View, call invocation) tea.Cmd {
				return view.reactCommand(call.arguments, call.target.comment)
			},
		},
		{
			Action: keys.Action{Name: "approve", Command: "approve"},
			level:  levelPullRequest, guard: writeReview, fresh: freshHead,
			run: func(view *View, _ invocation) tea.Cmd {
				return view.review.start(view.reviewEnvironment(), github.ReviewEventApprove)
			},
		},
		{
			Action: keys.Action{Name: "request-changes", Command: "request-changes"},
			level:  levelPullRequest, guard: writeReview, fresh: freshHead,
			run: func(view *View, _ invocation) tea.Cmd {
				return view.review.start(view.reviewEnvironment(), github.ReviewEventRequestChanges)
			},
		},
		{
			Action: keys.Action{Name: "review", Command: "review"},
			level:  levelPullRequest, guard: writeReview, fresh: freshHead,
			run: func(view *View, _ invocation) tea.Cmd { return view.review.openChooser(view.reviewEnvironment()) },
		},
		{
			Action: keys.Action{Name: "merge", Command: "merge"},
			level:  levelPullRequest, guard: writeMerge, fresh: freshHead,
			check: checkMergeArguments,
			run: func(view *View, call invocation) tea.Cmd {
				return view.merge.command(call.arguments, view.reviewEnvironment())
			},
		},
		{
			Action: keys.Action{Name: "discard-review", Command: "discard-review"},
			level:  levelPullRequest, guard: writeDiscard, fresh: freshTarget,
			run: func(view *View, _ invocation) tea.Cmd { return view.discardReview() },
		},
		{
			Action: keys.Action{Name: "edit-pull-request", Command: "edit-pull-request"},
			level:  levelPullRequest, guard: writePullRequest, fresh: freshTarget,
			run: func(view *View, _ invocation) tea.Cmd { return view.editPullRequest() },
		},
		{
			Action: keys.Action{Name: "ask", Command: "ask"},
			level:  levelLines, words: "ask about it", diffVisible: true,
			check: checkQuestion,
			run:   func(view *View, call invocation) tea.Cmd { return view.ask(call.arguments, call.target.lines) },
		},
		{
			Action: keys.Action{Name: "analysis", Command: "analysis"},
			level:  levelPullRequest,
			run:    func(view *View, call invocation) tea.Cmd { return view.analysisCommand(call.arguments) },
		},
		{
			Action: keys.Action{Name: "checks", Command: "checks"},
			level:  levelPullRequest,
			run:    func(view *View, _ invocation) tea.Cmd { return view.checksCommand() },
		},
		{
			Action: keys.Action{Name: "copy-filename", Command: "copy-filename"},
			level:  levelFile,
			run:    func(view *View, _ invocation) tea.Cmd { return view.copyFilename() },
		},
		{
			Action: keys.Action{Name: "copy-url", Command: "copy-url"},
			level:  levelPullRequest,
			run:    func(view *View, _ invocation) tea.Cmd { return view.copyURL() },
		},
	}
}

// actions lists the keys.Action of every entry, in the namespace of the
// global ones (keys.Global.Actions).
func (workspaceBindings *bindings) actions() []keys.Action {
	var actions []keys.Action
	for _, candidate := range workspaceBindings.entries() {
		actions = append(actions, candidate.Action)
	}
	return actions
}

// invoke runs an entry for a key or a command. The steps and their order are
// the ones every write used to repeat by hand; see actionEntry.
func (view *View) invoke(chosen actionEntry, call invocation) tea.Cmd {
	if chosen.check != nil {
		if refusal := chosen.check(call.arguments); refusal != nil {
			return refusal
		}
	}
	if chosen.guard != writeNone {
		if notice := view.guardState().ready(chosen.guard); notice != nil {
			return notice
		}
	}
	if chosen.diffVisible {
		if hidden := view.diffHidden(chosen.words); hidden != nil {
			return hidden
		}
	}
	if chosen.level.resolved() {
		resolved, refusal := view.resolveTarget(request{level: chosen.level, words: chosen.words, rule: chosen.rule})
		if refusal != nil {
			return refusal
		}
		call.target = resolved
	}
	if chosen.guard != writeNone {
		if busy := view.guardState().busy(chosen.guard); busy != nil {
			return busy
		}
	}
	return chosen.run(view, call)
}

// entryNamed returns the entry with the given action name.
func (view *View) entryNamed(name string) actionEntry {
	for _, candidate := range view.bind.entries() {
		if candidate.Name == name {
			return candidate
		}
	}
	panic("workspace: no action named " + name)
}

// keyEntry returns the entry a key press reaches, if any.
func (view *View) keyEntry(message keys.Message) (actionEntry, bool) {
	for _, candidate := range view.bind.entries() {
		if candidate.run == nil || candidate.Binding == nil || !key.Matches(message, *candidate.Binding) {
			continue
		}
		if candidate.applies == nil || candidate.applies(view) {
			return candidate, true
		}
	}
	return actionEntry{}, false
}

// commandEntry returns the entry that has the command, if any.
func (view *View) commandEntry(name string) (actionEntry, bool) {
	if name == "" { // most entries have no command
		return actionEntry{}, false
	}
	for _, candidate := range view.bind.entries() {
		if candidate.Command == name {
			return candidate, true
		}
	}
	return actionEntry{}, false
}

// Commands lists the command names of the workspace, for the shell to tell a
// command another view owns from a word that is no command at all.
func Commands() []string {
	workspaceBindings := defaultBindings()
	var names []string
	for _, candidate := range workspaceBindings.entries() {
		if candidate.Command != "" {
			names = append(names, candidate.Command)
		}
	}
	return names
}
