package workspace

import (
	"net/url"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/diff"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface"
	"github.com/MikePors/strigidae/internal/userinterface/components/diffview"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
)

// excerptMargin is how many lines of the hunk the excerpt adds on each side of
// the selected lines.
const excerptMargin = 3

// entries is every action of the workspace: the built-in registry, then one
// entry per configured plugin. A built-in command name is found first, so a
// plugin can never shadow one (cmd/strig refuses such a config at startup).
func (view *View) entries() []actionEntry {
	return append(view.bind.entries(), view.pluginEntries()...)
}

// pluginEntries makes the registry entries of the plugin commands. They have
// the shape of any other entry (a command, a level, a guard, a run), and no
// key: a plugin gets commands only.
func (view *View) pluginEntries() []actionEntry {
	entries := make([]actionEntry, 0, len(view.pluginCommands))
	for _, command := range view.pluginCommands {
		entry := actionEntry{
			Action: keys.Action{Name: "plugin-" + command.Name, Command: command.Name},
			level:  levelPullRequest,
			guard:  usePluginPullRequest,
			run: func(view *View, call invocation) tea.Cmd {
				return view.runPlugin(command, call)
			},
		}
		if command.Target == config.TargetLines {
			entry.level, entry.guard = levelLines, usePluginLines
			entry.words, entry.diffVisible = "run :"+command.Name, true
		}
		entries = append(entries, entry)
	}
	return entries
}

// runPlugin asks the root to run a plugin. The root owns the run, so closing
// this view does not cancel it.
func (view *View) runPlugin(command userinterface.PluginCommand, call invocation) tea.Cmd {
	request := plugins.Request{
		Command:   command.Name,
		Arguments: call.arguments,
		Repository: plugins.Repository{
			Host:  view.host(),
			Owner: view.repository.Owner,
			Name:  view.repository.Name,
		},
		Number: view.number,
		Head:   view.detail.HeadSHA,
	}
	if command.Target == config.TargetLines {
		target, refusal := view.linesTarget(call.target.lines)
		if refusal != nil {
			return refusal
		}
		request.Target, request.Head = target, view.diffHead
		view.filePane.VisualEnd() // the selection has been used, as when a comment is started
	}
	identity, _ := view.PullRequestIdentity()
	return func() tea.Msg {
		return userinterface.RunPluginMessage{Name: command.Name, Request: request, Identity: identity}
	}
}

// linesTarget describes the selected lines for a plugin.
func (view *View) linesTarget(lines diffview.Range) (*plugins.Target, tea.Cmd) {
	var file *diff.File
	if view.diff.Parsed != nil {
		file = view.diff.Parsed.File(lines.Path)
	}
	excerpt, ok := excerptOf(file, lines)
	if !ok {
		return nil, userinterface.Info("those lines are not in the diff; refresh and try again")
	}
	return &plugins.Target{
		Path:      lines.Path,
		Side:      string(lines.Side),
		Line:      lines.End,
		StartLine: lines.Start,
		Excerpt:   excerpt,
	}, nil
}

// excerptOf is the selected lines and up to excerptMargin lines of their hunk
// on each side, one text line each, starting with "+", "-" or a space.
func excerptOf(file *diff.File, lines diffview.Range) (string, bool) {
	if file == nil {
		return "", false
	}
	for _, hunk := range file.Hunks {
		shown := make([]diff.Line, 0, len(hunk.Lines))
		for _, line := range hunk.Lines {
			if line.Kind != diff.NoNewline {
				shown = append(shown, line)
			}
		}
		first, last := indexOfLine(shown, lines.Side, lines.Start, 0), -1
		if first >= 0 {
			last = indexOfLine(shown, lines.Side, lines.End, first)
		}
		if last < 0 {
			continue
		}
		from, to := max(0, first-excerptMargin), min(len(shown), last+excerptMargin+1)
		text := make([]string, 0, to-from)
		for _, line := range shown[from:to] {
			text = append(text, linePrefix(line.Kind)+line.Text)
		}
		return strings.Join(text, "\n"), true
	}
	return "", false
}

// indexOfLine is the index, from start on, of the line numbered number on
// side, or -1. A deleted line has no number on the new side and an added line
// none on the old side.
func indexOfLine(lines []diff.Line, side github.DiffSide, number, start int) int {
	for i := start; i < len(lines); i++ {
		if side == github.SideLeft && lines[i].Kind != diff.Add && lines[i].Old == number {
			return i
		}
		if side != github.SideLeft && lines[i].Kind != diff.Delete && lines[i].New == number {
			return i
		}
	}
	return -1
}

func linePrefix(kind diff.LineKind) string {
	switch kind {
	case diff.Add:
		return "+"
	case diff.Delete:
		return "-"
	}
	return " "
}

// host is the GitHub host of the pull request, from the address GitHub gave
// for it; empty if it has none.
func (view *View) host() string {
	if view.detail == nil {
		return ""
	}
	address, err := url.Parse(view.detail.URL)
	if err != nil {
		return ""
	}
	return github.NormalizeHost(address.Hostname())
}

// PullRequestIdentity implements userinterface.PluginHost.
func (view *View) PullRequestIdentity() (userinterface.PullRequestIdentity, bool) {
	if view.detail == nil {
		return userinterface.PullRequestIdentity{}, false
	}
	return userinterface.PullRequestIdentity{Repository: view.repository, Number: view.number, Head: view.detail.HeadSHA}, true
}

// Reload implements userinterface.PluginHost: the same reload as refresh.
func (view *View) Reload() tea.Cmd { return view.load() }

// KeepsCommandLine implements userinterface.CommandLineKeeper: ":" opens the
// command line during a line selection, so a `lines` plugin can use it. With
// no such plugin configured the selection ignores ":" as it always did.
func (view *View) KeepsCommandLine() bool {
	return view.visualActive() && view.picker == nil && view.review.chooser == nil && view.hasLinesPlugin()
}

func (view *View) hasLinesPlugin() bool {
	for _, command := range view.pluginCommands {
		if command.Target == config.TargetLines {
			return true
		}
	}
	return false
}

// duringSelection refuses the commands of the workspace other than a `lines`
// plugin's while a line selection is open: they could open a chooser or picker
// under it. The shell's own commands never get here.
// It returns nil when the command may run.
func (view *View) duringSelection(chosen actionEntry) tea.Cmd {
	if !view.visualActive() || (chosen.guard == usePluginLines && chosen.level == levelLines) {
		return nil
	}
	return userinterface.Info("end the selection to run :" + chosen.Command)
}
