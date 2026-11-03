package userinterface

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/plugins"
	"github.com/MikePors/strigidae/internal/userinterface/terminaltext"
)

// DefaultPluginQuitWait is the longest strig waits for plugin runs to end
// after asking to quit. The runs are cancelled at once; this only bounds
// what a plugin or the runner could still take.
const DefaultPluginQuitWait = 2 * time.Second

// PluginRunner runs the plugin called name. *plugins.Runner is the real one.
type PluginRunner interface {
	Run(ctx context.Context, name string, request plugins.Request) ([]plugins.Effect, error)
}

// PluginCommand is a configured plugin as the views see it: a command name
// and what the command acts on.
type PluginCommand struct {
	Name   string
	Target config.Target
	// Show is where the answer is drawn; empty is the status bar.
	Show config.Show
}

// PluginsMessage tells views which plugin commands exist. Like OptionsMessage,
// the root sends it to the root view when strig starts and to every view it
// pushes.
type PluginsMessage struct{ Commands []PluginCommand }

// PullRequestIdentity names a pull request at one head commit. It is carried
// by a plugin run and compared (repository and number) with the pull request
// on screen when the run ends, so a result is shown only where it belongs.
type PullRequestIdentity struct {
	Repository github.Repository
	Number     int
	Head       string
}

// SamePullRequest reports whether both name the same pull request, whatever
// their heads. Result routing and the spinner both use it, so they agree: a
// new commit during a run does not hide its result, and the plugin has the
// head it was run at in its request.
func (identity PullRequestIdentity) SamePullRequest(other PullRequestIdentity) bool {
	return identity.Repository == other.Repository && identity.Number == other.Number
}

// RunPluginMessage asks the root to run a plugin. Runs belong to the root,
// not to the view that asked: closing the view does not cancel one, and
// quitting does.
type RunPluginMessage struct {
	Name    string
	Request plugins.Request
	// Identity is the pull request the run was asked from.
	Identity PullRequestIdentity
}

// PluginHost is implemented by a view that shows a pull request and so can
// receive the effects of a plugin run.
type PluginHost interface {
	// PullRequestIdentity returns the pull request on screen; ok is false until
	// it has loaded.
	PullRequestIdentity() (identity PullRequestIdentity, ok bool)
	// Reload reloads what the view shows, as its refresh key does.
	Reload() tea.Cmd
}

// CommandLineKeeper is implemented by a KeyCapturer that lets ":" open the
// command line while it captures keys (a line selection that a command is to
// use). The line closes without touching the view's state, so cancelling it
// leaves the view as it was.
type CommandLineKeeper interface {
	KeepsCommandLine() bool
}

// pluginRun is a run the root has started and not yet heard back from.
type pluginRun struct {
	identity PullRequestIdentity
	name     string
	// thread is the local thread the run fills, or "" when it has none.
	thread string
}

type (
	pluginResultMessage struct {
		run     int
		effects []plugins.Effect
		err     error
	}
	// quitWaitedMessage ends the wait for plugin runs at quit.
	quitWaitedMessage struct{}
)

// startPlugin begins a run in a command the root owns. After quit began there
// are no new runs.
func (model *Model) startPlugin(message RunPluginMessage) tea.Cmd {
	if model.quitting || model.pluginRunner == nil {
		return nil
	}
	if model.pluginContext == nil {
		model.pluginContext, model.pluginCancel = context.WithCancel(context.Background())
	}
	model.pluginSequence++
	run := model.pluginSequence
	started := pluginRun{identity: message.Identity, name: message.Name}
	var shown tea.Cmd
	if model.showsThread(message.Name) && message.Request.Target != nil {
		started.thread = model.askLocalThread(message)
		shown = model.broadcast(LocalThreadsMessage{Threads: model.localThreads.snapshot()})
	}
	model.pluginRuns[run] = started
	runner, ctx := model.pluginRunner, model.pluginContext
	model.pluginRunning.Add(1)
	return tea.Batch(shown, func() tea.Msg {
		defer model.pluginRunning.Done()
		effects, err := runner.Run(ctx, message.Name, message.Request)
		return pluginResultMessage{run: run, effects: effects, err: err}
	})
}

// showsThread reports whether the plugin draws its answer as a local thread.
func (model *Model) showsThread(name string) bool {
	for _, command := range model.pluginCommands {
		if command.Name == name {
			return command.Show == config.ShowThread
		}
	}
	return false
}

// askLocalThread adds the thread a run will fill, at once, so it shows even
// while the run waits its turn. The question is the arguments, or the command
// alone when there are none.
func (model *Model) askLocalThread(message RunPluginMessage) string {
	target := message.Request.Target
	question := message.Request.Arguments
	if question == "" {
		question = ":" + message.Name
	}
	return model.localThreads.add(LocalThread{
		Repository: message.Identity.Repository,
		Number:     message.Identity.Number,
		Head:       message.Request.Head,
		Path:       target.Path,
		Side:       github.DiffSide(target.Side),
		Line:       target.Line,
		StartLine:  target.StartLine,
		Question:   question,
		State:      github.LocalWaiting,
	})
}

// finishLocalThread fills the thread of a finished run: the answers joined by
// a blank line, or why there is none. An error effect with no answer is the
// reason; a run that printed neither says so. The status bar is left alone: the thread is
// where the outcome shows.
func (model *Model) finishLocalThread(identifier string, message pluginResultMessage) tea.Cmd {
	state, text := github.LocalFailed, ""
	var answers []string
	var failure string
	for _, effect := range message.effects {
		switch effect.Name {
		case plugins.EffectAnswer:
			answers = append(answers, effect.Text)
		case plugins.EffectError:
			if failure == "" {
				failure = effect.Text
			}
		}
	}
	switch {
	case message.err != nil && len(answers) == 0:
		text = message.err.Error()
	case len(answers) > 0:
		state, text = github.LocalAnswered, strings.Join(answers, "\n\n")
	case failure != "":
		text = failure
	default:
		text = "the plugin gave no answer"
	}
	model.localThreads.finish(identifier, state, text)
	return model.broadcast(LocalThreadsMessage{Threads: model.localThreads.snapshot()})
}

// pluginActive reports whether a run of the pull request the top view shows
// is under way or queued.
func (model *Model) pluginActive() bool {
	host, ok := model.top().(PluginHost)
	if !ok || len(model.pluginRuns) == 0 {
		return false
	}
	shown, ok := host.PullRequestIdentity()
	if !ok {
		return false
	}
	for _, run := range model.pluginRuns {
		if run.identity.SamePullRequest(shown) {
			return true
		}
	}
	return false
}

// pluginResult shows the effects of a finished run on the pull request it was
// asked from. Anything else is dropped, with a log line that names no text.
func (model *Model) pluginResult(message pluginResultMessage) tea.Cmd {
	finished := model.pluginRuns[message.run]
	delete(model.pluginRuns, message.run)
	var filled tea.Cmd
	if finished.thread != "" && !model.quitting {
		// The thread is in the store whatever is on screen, so an answer that
		// comes while its pull request is closed is there when it reopens.
		filled = model.finishLocalThread(finished.thread, message)
	}
	if model.quitting {
		// Nothing arriving after quit began is shown. The last run to end
		// lets strig go before the wait is over.
		if len(model.pluginRuns) == 0 {
			return tea.Quit
		}
		return nil
	}
	host, ok := model.top().(PluginHost)
	if ok {
		var shown PullRequestIdentity
		if shown, ok = host.PullRequestIdentity(); ok {
			ok = shown.SamePullRequest(finished.identity)
		}
	}
	if !ok {
		model.log.Info("plugin result dropped: its pull request is not on screen", "plugin", finished.name, "number", finished.identity.Number)
		return filled
	}
	if message.err != nil {
		model.log.Warn("plugin failed", "plugin", finished.name, "err", message.err)
		if finished.thread != "" {
			return filled
		}
		return model.setStatus(terminaltext.Clean(message.err.Error()), statusError)
	}
	return tea.Batch(filled, model.pluginEffects(host, finished.name, message.effects, finished.thread != ""))
}

// pluginEffects applies the effects in the order the plugin printed them.
// Their text is the plugin's, so it is cleaned before the status bar.
func (model *Model) pluginEffects(host PluginHost, name string, effects []plugins.Effect, inThread bool) tea.Cmd {
	var commands []tea.Cmd
	answered := slices.ContainsFunc(effects, func(effect plugins.Effect) bool { return effect.Name == plugins.EffectAnswer })
	for _, effect := range effects {
		// The thread shows the answers, and the error when there is no answer;
		// an error beside an answer would otherwise be lost, so it also goes
		// to the status bar.
		if inThread && (effect.Name == plugins.EffectAnswer || (effect.Name == plugins.EffectError && !answered)) {
			continue
		}
		switch effect.Name {
		case plugins.EffectNotice, plugins.EffectAnswer: // answers show as notices until they have a place of their own
			commands = append(commands, model.setStatus(terminaltext.Clean(effect.Text), statusInfo))
		case plugins.EffectError:
			commands = append(commands, model.setStatus(terminaltext.Clean(effect.Text), statusError))
		case plugins.EffectReload:
			commands = append(commands, host.Reload())
		default:
			model.log.Warn("plugin effect not handled", slog.String("plugin", name), slog.String("effect", effect.Name))
		}
	}
	return tea.Batch(commands...)
}

// quit ends the program. With plugin runs under way they are cancelled and
// strig waits for them, but never longer than the quit wait; with none it
// quits at once. A second request does not wait again.
func (model *Model) quit() tea.Cmd {
	if model.quitting || len(model.pluginRuns) == 0 {
		return tea.Quit
	}
	model.quitting = true
	model.pluginCancel()
	return model.tick(model.pluginQuitWait, func(time.Time) tea.Msg { return quitWaitedMessage{} })
}

// Close cancels every plugin run and waits for them to end, for at most the
// quit wait. cmd/strig calls it after the program returns, on every path: an
// exit that never reaches Update (SIGTERM ends Bubble Tea with a QuitMsg that
// the model does not see; SIGINT and a failed Run are others) would otherwise
// leave plugins running, and a plugin has a session of its own. The wait is
// for the runs to end, not just for the cancel: the kill happens in the
// runner's goroutine, which strig must not outrun by exiting. The program has
// returned, so nothing else uses the model; the caller must not use it
// afterwards.
func (model *Model) Close() {
	if model.pluginCancel == nil {
		return
	}
	model.pluginCancel()
	ended := make(chan struct{})
	go func() {
		model.pluginRunning.Wait()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(model.pluginQuitWait):
	}
}
