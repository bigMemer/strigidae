package userinterface

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/MikePors/strigidae/internal/options"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

// Defaults for Options.
const (
	DefaultSequenceTimeout = 500 * time.Millisecond
	DefaultMessageLifetime = 4 * time.Second
)

// TickFunction schedules function after duration, like tea.Tick. Tests inject their own.
type TickFunction func(duration time.Duration, function func(time.Time) tea.Msg) tea.Cmd

// Options configures the root Model. Zero values select defaults.
type Options struct {
	Logger          *slog.Logger
	SequenceTimeout time.Duration // how long a pending key sequence waits
	MessageLifetime time.Duration // how long info/error messages stay
	Tick            TickFunction
	// Notice, if set, is shown in the status bar when strig starts, for
	// example why the configured theme could not be used.
	Notice string
	// LoadTheme loads the theme `:theme` names; LoadThemeFromWorkingDirectory
	// by default.
	LoadTheme ThemeLoader
	// StartingOptions are the starting values of the `:set` options, normally
	// from the config file. The zero value is every option at its default.
	StartingOptions options.Values
	// ViewCommands are the names of the commands the views handle through
	// Commander, collected by cmd/strig from each view package. They let the
	// shell tell a command the top view lacks ("not available here") from a
	// word that is no command at all.
	ViewCommands []string
	// PluginRunner runs the configured plugins; nil when there are none.
	PluginRunner PluginRunner
	// PluginCommands are the configured plugins' commands. Views are told
	// through PluginsMessage; the names must also be in ViewCommands.
	PluginCommands []PluginCommand
	// PluginQuitWait is how long quitting waits for plugin runs it has
	// cancelled; DefaultPluginQuitWait by default.
	PluginQuitWait time.Duration
	// StateDirectory is where local threads are kept between runs of strig
	// (see logging.StateDirectory). Empty keeps them for the session only.
	StateDirectory string
	// Clock gives the time a local thread is asked at; time.Now by default.
	Clock func() time.Time
	// LocalThreadLimit is the most local threads kept per pull request;
	// DefaultLocalThreadLimit by default.
	LocalThreadLimit int
	// LocalThreadBytes is the largest a pull request's file of local threads
	// may be; localthreads.MaxFileSize by default, which is what a file is
	// refused above when read.
	LocalThreadBytes int
}

// Internal timer messages. The id/gen fields discard timers that were
// superseded before they fired.
type (
	sequenceTimeoutMessage struct{ generation int }
	clearStatusMessage     struct{ identifier int }
)

// Model is the root Bubble Tea model. It owns the view stack, key sequence
// resolution, the status bar, and the help overlay.
type Model struct {
	stack           []View
	theme           theme.Theme
	keys            keys.Global
	log             *slog.Logger
	tick            TickFunction
	sequenceWait    time.Duration
	messageLifetime time.Duration
	notice          string
	themeLoader     ThemeLoader
	themeSequence   int // the latest `:theme` load; older results are ignored
	options         options.Values
	viewCommands    []string

	// Plugin runs belong to the root: closing a view does not cancel one.
	pluginRunner   PluginRunner
	pluginCommands []PluginCommand
	pluginQuitWait time.Duration
	pluginContext  context.Context // made with the first run; cancelled at quit
	pluginCancel   context.CancelFunc
	pluginSequence int
	pluginRuns     map[int]pluginRun
	pluginRunning  sync.WaitGroup // runs whose command has not returned; see Close
	quitting       bool           // quit began and is waiting for plugin runs
	localThreads   localThreadStore
	localFiles     localThreadFiles
	clock          func() time.Time

	width, height      int
	matcher            keys.Matcher
	sequenceGeneration int

	showHelp    bool
	helpOffset  int            // top body line of the help; always within the body
	confirm     *confirmPrompt // open yes/no question, or nil
	commandLine commandLine
	status      struct {
		text       string
		kind       statusKind
		identifier int
	}
	spinning bool // the root spinner's tick chain is running
	spin     spinner.Model
}

// New returns a root model whose stack holds just root.
func New(root View, activeTheme theme.Theme, startup Options) *Model {
	if startup.SequenceTimeout == 0 {
		startup.SequenceTimeout = DefaultSequenceTimeout
	}
	if startup.MessageLifetime == 0 {
		startup.MessageLifetime = DefaultMessageLifetime
	}
	if startup.Tick == nil {
		startup.Tick = tea.Tick
	}
	if startup.LoadTheme == nil {
		startup.LoadTheme = LoadThemeFromWorkingDirectory
	}
	if startup.Logger == nil {
		startup.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	// Views start with the defaults, so they are told only when options were
	// set. The root view hears them here and a pushed view in push; a view
	// returns no command for them.
	if startup.StartingOptions != nil {
		root, _ = root.Update(OptionsMessage{Values: startup.StartingOptions})
	}
	if len(startup.PluginCommands) > 0 {
		root, _ = root.Update(PluginsMessage{Commands: startup.PluginCommands})
	}
	if startup.PluginQuitWait <= 0 {
		startup.PluginQuitWait = DefaultPluginQuitWait
	}
	if startup.Clock == nil {
		startup.Clock = time.Now
	}
	return &Model{
		clock:           startup.Clock,
		localThreads:    localThreadStore{limit: startup.LocalThreadLimit, bytes: startup.LocalThreadBytes},
		localFiles:      localThreadFiles{directory: startup.StateDirectory},
		stack:           []View{root},
		theme:           activeTheme,
		keys:            keys.DefaultGlobal(),
		log:             startup.Logger,
		tick:            startup.Tick,
		sequenceWait:    startup.SequenceTimeout,
		messageLifetime: startup.MessageLifetime,
		notice:          startup.Notice,
		themeLoader:     startup.LoadTheme,
		options:         startup.StartingOptions,
		viewCommands:    startup.ViewCommands,
		pluginRunner:    startup.PluginRunner,
		pluginCommands:  startup.PluginCommands,
		pluginQuitWait:  startup.PluginQuitWait,
		pluginRuns:      map[int]pluginRun{},
		commandLine:     newCommandLine(activeTheme),
		spin:            spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(activeTheme.Spinner)),
	}
}

// Init starts the root view, shows the startup notice if any, and asks the
// terminal for its background colour so the theme can match light and dark
// terminals.
func (model *Model) Init() tea.Cmd {
	commands := []tea.Cmd{model.top().Init(), tea.RequestBackgroundColor}
	if model.notice != "" {
		commands = append(commands, Info(model.notice))
	}
	return tea.Batch(commands...)
}

func (model *Model) top() View { return model.stack[len(model.stack)-1] }

func (model *Model) contentSize() (width, height int) { return model.width, max(0, model.height-1) }

// Update implements tea.Model.
func (model *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	command := model.update(message)
	return model, tea.Batch(command, model.syncSpinner())
}

func (model *Model) update(message tea.Msg) tea.Cmd {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = message.Width, message.Height
		model.commandLine.input.SetWidth(max(1, message.Width-2)) // room for the prompt and cursor
		model.clampHelp()
		return model.broadcast(model.sizeMessage())
	case tea.BackgroundColorMsg:
		// The reply picks a built-in variant only. A theme the user chose is
		// never replaced, however late the terminal answers.
		if model.theme.Chosen {
			return nil
		}
		return model.setTheme(theme.New(message.IsDark()))
	case setRequestedMessage:
		return model.set(message.arguments)
	case themeRequestedMessage:
		return model.loadTheme(message.value)
	case themeLoadedMessage:
		return model.themeLoaded(message)
	case tea.KeyPressMsg:
		return model.handleKey(message)
	case PushMessage:
		return model.push(message.View)
	case PopMessage:
		return model.pop()
	case QuitMessage:
		return model.quit()
	case RunPluginMessage:
		return model.startPlugin(message)
	case pluginResultMessage:
		return model.pluginResult(message)
	case PullRequestOpenedMessage:
		return model.pullRequestOpened(message)
	case localThreadsLoadedMessage:
		return model.localThreadsLoaded(message)
	case localThreadsSavedMessage:
		return model.localThreadsSaved(message)
	case quitWaitedMessage:
		return tea.Quit
	case ConfirmMessage:
		model.resetKeys()
		model.confirm = newConfirmPrompt(message)
		return nil
	case InfoMessage:
		return model.setStatus(message.Text, statusInfo)
	case ErrorMessage:
		model.log.Warn("error shown to user", "err", message.Err)
		text := "error"
		if message.Err != nil {
			text = message.Err.Error()
		}
		return model.setStatus(text, statusError)
	case clearStatusMessage:
		if message.identifier == model.status.identifier {
			model.status.text, model.status.kind = "", statusNone
		}
		return nil
	case sequenceTimeoutMessage:
		return model.expireSequence(message.generation)
	case spinner.TickMsg:
		if message.ID != model.spin.ID() {
			return model.broadcastOther(message) // a view's own spinner
		}
		if !model.spinning {
			return nil // let the tick chain die while idle
		}
		var command tea.Cmd
		model.spin, command = model.spin.Update(message)
		return command
	default:
		return model.broadcastOther(message)
	}
}

func (model *Model) sizeMessage() tea.WindowSizeMsg {
	width, height := model.contentSize()
	return tea.WindowSizeMsg{Width: width, Height: height}
}

// broadcast sends message to every view in the stack.
func (model *Model) broadcast(message tea.Msg) tea.Cmd {
	commands := make([]tea.Cmd, len(model.stack))
	for i, view := range model.stack {
		model.stack[i], commands[i] = view.Update(message)
	}
	return tea.Batch(commands...)
}

// broadcastOther sends a message the root does not consume to every view (and
// to the command line, for cursor blink).
func (model *Model) broadcastOther(message tea.Msg) tea.Cmd {
	if model.commandLine.active && model.commandLine.prompt != "" {
		return tea.Batch(model.promptInput(message), model.broadcast(message))
	}
	if model.commandLine.active {
		return tea.Batch(model.commandLine.update(message), model.broadcast(message))
	}
	return model.broadcast(message)
}

func (model *Model) forward(message tea.Msg) tea.Cmd {
	view, command := model.top().Update(message)
	model.stack[len(model.stack)-1] = view
	return command
}

func (model *Model) push(view View) tea.Cmd {
	if view == nil {
		return nil
	}
	model.resetKeys()
	model.stack = append(model.stack, view)
	model.log.Debug("push view", "title", view.Title(), "depth", len(model.stack))
	if model.options != nil {
		view, _ = view.Update(OptionsMessage{Values: model.options})
		model.stack[len(model.stack)-1] = view
	}
	if len(model.pluginCommands) > 0 {
		view, _ = view.Update(PluginsMessage{Commands: model.pluginCommands})
		model.stack[len(model.stack)-1] = view
	}
	if threads := model.localThreads.snapshot(); len(threads) > 0 {
		view, _ = view.Update(LocalThreadsMessage{Threads: threads})
		model.stack[len(model.stack)-1] = view
	}
	return tea.Batch(view.Init(), model.forward(model.sizeMessage()))
}

func (model *Model) pop() tea.Cmd {
	model.resetKeys()
	if len(model.stack) == 1 {
		return model.quit()
	}
	model.stack = model.stack[:len(model.stack)-1]
	model.log.Debug("pop view", "depth", len(model.stack))
	if resumer, ok := model.top().(Resumer); ok {
		return resumer.Resumed()
	}
	return nil
}

func (model *Model) resetKeys() {
	model.showHelp = false
	model.confirm = nil
	model.commandLine.close()
	model.matcher.Reset()
	model.sequenceGeneration++
}

func (model *Model) setStatus(text string, kind statusKind) tea.Cmd {
	model.status.identifier++
	model.status.text, model.status.kind = text, kind
	identifier := model.status.identifier
	return model.tick(model.messageLifetime, func(time.Time) tea.Msg { return clearStatusMessage{identifier: identifier} })
}

// syncSpinner starts the root spinner's tick chain when the top view is
// loading and lets it die when it is not. It runs after every Update, so
// push, pop and view state changes are all covered.
func (model *Model) syncSpinner() tea.Cmd {
	loader, ok := model.top().(Loader)
	want := ok && loader.Loading() || model.pluginActive()
	switch {
	case want && !model.spinning:
		model.spinning = true
		return model.spin.Tick
	case !want:
		model.spinning = false
	}
	return nil
}

// bindings returns every binding active now, for sequence matching.
func (model *Model) bindings() []key.Binding {
	out := model.keys.Bindings()
	for _, bindings := range model.top().FullHelp() {
		out = append(out, bindings...)
	}
	return out
}

func (model *Model) handleKey(message tea.KeyPressMsg) tea.Cmd {
	pressed := message.String()
	if confirmPrompt := model.confirm; confirmPrompt.CapturesKeys() {
		model.confirm = nil
		return confirmPrompt.answer(message)
	}
	if model.commandLine.active && model.commandLine.prompt != "" {
		return model.promptKey(message)
	}
	if model.commandLine.active {
		switch pressed {
		case "tab":
			model.complete(1)
			return nil
		case "shift+tab":
			model.complete(-1)
			return nil
		}
		line, ok, command := model.commandLine.handleKey(message)
		if ok {
			return parseCommand(line, model.top(), model.viewCommands)
		}
		return command
	}
	if model.showHelp {
		return model.handleHelpKey(message)
	}
	if capturer, ok := model.top().(KeyCapturer); ok && capturer.CapturesKeys() {
		if keeper, ok := model.top().(CommandLineKeeper); ok && keeper.KeepsCommandLine() && key.Matches(message, model.keys.Command) {
			model.resetKeys()
			return model.commandLine.open()
		}
		model.resetKeys()
		return model.forward(message)
	}

	result := model.matcher.Feed(pressed, model.bindings())
	model.sequenceGeneration++
	switch result.Kind {
	case keys.Pending:
		generation := model.sequenceGeneration
		return model.tick(model.sequenceWait, func(time.Time) tea.Msg { return sequenceTimeoutMessage{generation: generation} })
	case keys.Matched:
		return model.dispatch(result.Sequence, result.Count)
	default:
		return model.forward(message)
	}
}

func (model *Model) expireSequence(generation int) tea.Cmd {
	if generation != model.sequenceGeneration || !model.matcher.Pending() {
		return nil
	}
	model.sequenceGeneration++
	if model.showHelp {
		// The help owns the keys: nothing may reach the view from here.
		model.matcher.Reset()
		return nil
	}
	if result := model.matcher.Expire(model.bindings()); result.Kind == keys.Matched {
		return model.dispatch(result.Sequence, result.Count)
	}
	return nil
}

// dispatch acts on a resolved key or sequence: the root consumes Help and
// Command, everything else goes to the view.
func (model *Model) dispatch(sequence string, count int) tea.Cmd {
	keyMessage := keys.Message{Sequence: sequence, Count: count}
	switch {
	case key.Matches(keyMessage, model.keys.Help):
		model.showHelp = true
		model.helpOffset = 0
		return nil
	case key.Matches(keyMessage, model.keys.Command):
		return model.commandLine.open()
	}
	return model.withRequestedPrompt(model.forward(keyMessage))
}

// helpLines is the help's scrolling body at the current size and bindings.
func (model *Model) helpLines() []string {
	width, _ := model.contentSize()
	return helpBody(model.theme, model.top().Title(), model.keys.FullHelp(), model.top().FullHelp(), width)
}

// clampHelp pulls the offset back inside the body, which changes length with
// the width and with the view's enabled bindings.
func (model *Model) clampHelp() {
	if !model.showHelp {
		return
	}
	_, height := model.contentSize()
	model.helpOffset = clampHelpOffset(model.helpOffset, len(model.helpLines()), helpBodyRows(height))
}

// handleHelpKey runs a key press while the help is open. Only the global
// bindings are matched, and nothing reaches the view.
func (model *Model) handleHelpKey(message tea.KeyPressMsg) tea.Cmd {
	if key.Matches(message, model.keys.Help, model.keys.Cancel) {
		model.resetKeys()
		return nil
	}
	result := model.matcher.Feed(message.String(), model.keys.Bindings())
	model.sequenceGeneration++
	switch result.Kind {
	case keys.Pending:
		generation := model.sequenceGeneration
		return model.tick(model.sequenceWait, func(time.Time) tea.Msg { return sequenceTimeoutMessage{generation: generation} })
	case keys.Matched:
		model.scrollHelp(result.Sequence, result.Count)
	}
	return nil
}

// scrollHelp moves the help by a resolved global key; other keys do nothing.
func (model *Model) scrollHelp(sequence string, count int) {
	keyMessage := keys.Message{Sequence: sequence, Count: count}
	_, height := model.contentSize()
	rows := helpBodyRows(height)
	model.clampHelp() // the body may have changed since the last key
	offset := model.helpOffset
	repeat := keyMessage.Repeat()
	switch {
	case key.Matches(keyMessage, model.keys.Down):
		offset += repeat
	case key.Matches(keyMessage, model.keys.Up):
		offset -= repeat
	case key.Matches(keyMessage, model.keys.Top):
		offset = 0
	case key.Matches(keyMessage, model.keys.Bottom):
		offset = len(model.helpLines())
		if count > 0 {
			offset = count - 1
		}
	case key.Matches(keyMessage, model.keys.HalfDown):
		offset += repeat * max(1, rows/2)
	case key.Matches(keyMessage, model.keys.HalfUp):
		offset -= repeat * max(1, rows/2)
	case key.Matches(keyMessage, model.keys.PageDown):
		offset += repeat * rows
	case key.Matches(keyMessage, model.keys.PageUp):
		offset -= repeat * rows
	default:
		return
	}
	model.helpOffset = offset
	model.clampHelp()
}

// View implements tea.Model.
func (model *Model) View() tea.View {
	var view tea.View
	if model.width > 0 && model.height > 0 {
		view = tea.NewView(model.overlayCompletion(model.content()) + "\n" + model.bottomRow())
	}
	view.AltScreen = true
	if model.theme.Chosen && !model.theme.FromTerminal {
		// The terminal restores its own colours when strig exits.
		view.BackgroundColor = model.theme.Roles.Background
		view.ForegroundColor = model.theme.Roles.Text
	}
	return view
}

// content renders the area above the status bar, padded to full size.
func (model *Model) content() string {
	width, height := model.contentSize()
	var text string
	if model.showHelp {
		text = renderHelp(model.theme, model.helpLines(), height, model.helpOffset)
	} else {
		text = model.top().Render(width, height)
	}
	text = model.theme.Normal.MaxWidth(width).MaxHeight(height).Render(text)
	if count := height - lipgloss.Height(text); count > 0 {
		text += strings.Repeat("\n", count)
	}
	return text
}

func (model *Model) bar() statusBar {
	crumbs := make([]string, len(model.stack))
	for i, view := range model.stack {
		crumbs[i] = view.Title()
	}
	currentStatusBar := statusBar{
		crumbs:  strings.Join(crumbs, " › "),
		text:    model.status.text,
		kind:    model.status.kind,
		pending: model.matcher.Display(),
	}
	if currentStatusBar.kind == statusNone {
		currentStatusBar.text = shortHelp(model.top().ShortHelp())
	}
	if model.spinning {
		currentStatusBar.spinner = model.spin.View()
	}
	return currentStatusBar
}

func shortHelp(bindings []key.Binding) string {
	parts := []string{"? help"}
	for _, binding := range bindings {
		if help := binding.Help(); binding.Enabled() && help.Desc != "" {
			parts = append(parts, help.Key+" "+help.Desc)
		}
	}
	return strings.Join(parts, "  ")
}

// bottomRow is the status bar, or the command line while it is open.
func (model *Model) bottomRow() string {
	if model.confirm != nil {
		return model.theme.StatusInfo.Width(model.width).MaxWidth(model.width).MaxHeight(1).Render(model.confirm.render(model.width))
	}
	if model.commandLine.active {
		return model.theme.StatusCommand.Width(model.width).MaxWidth(model.width).MaxHeight(1).Render(model.commandLine.input.View())
	}
	return model.bar().render(model.theme, model.width)
}
