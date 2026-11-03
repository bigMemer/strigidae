// Package plugins runs the programs behind the user's configured command
// plugins. A run starts the program, writes one JSON request to its standard
// input and reads effects, JSON lines on its standard output, that say what
// the plugin would like strig to do. A plugin suggests; strig decides: strig
// gives a plugin no GitHub token and writes nothing for it, and what a plugin
// asks for goes through strig's own rules. A plugin is still the user's own
// program, running as the user, so it can do whatever the user can, such as
// running gh itself; only plugins the user trusts belong in the config.
//
// The package is non-UI and has no Bubble Tea import. Run blocks, so a caller
// puts it inside a tea.Cmd.
package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MikePors/strigidae/internal/config"
	"github.com/MikePors/strigidae/internal/environment"
)

// Protocol is the version written as "protocol" in every request.
const Protocol = 1

// MaximumOutputSize is the most standard output one run may print. Past it
// the process is killed and the run fails.
const MaximumOutputSize = 1 << 20

// MaximumErrorOutputSize is the most standard error of one run that reaches
// the log. The rest is dropped, with a note.
const MaximumErrorOutputSize = 64 << 10

// DefaultWaitDelay is how long strig keeps reading after the plugin process
// exited. Something the plugin started can hold standard output open for as
// long as it likes; once this has passed strig stops reading.
const DefaultWaitDelay = time.Second

// Effect names a plugin may print.
const (
	// EffectNotice asks for a short message in the status bar.
	EffectNotice = "notice"
	// EffectError reports a failure; the text is the reason.
	EffectError = "error"
	// EffectReload asks strig to reload what is on screen.
	EffectReload = "reload"
	// EffectAnswer carries an answer to show to the user.
	EffectAnswer = "answer"
	// EffectCommand asks strig to run one of its commands that write nothing;
	// Line is the command line.
	EffectCommand = "command"
)

// Errors a run can fail with, to be tested with errors.Is.
var (
	// ErrUnknownPlugin means no plugin has the requested name.
	ErrUnknownPlugin = errors.New("no such plugin")
	// ErrFailed means the plugin exited with a failure status.
	ErrFailed = errors.New("failed")
	// ErrTimeout means the plugin ran past its timeout and was killed.
	ErrTimeout = errors.New("timed out")
	// ErrOutputTooLarge means the plugin printed more than MaximumOutputSize
	// and was killed.
	ErrOutputTooLarge = errors.New("printed too much output")
	// ErrInvalidOutput means a line the plugin printed is not an effect.
	ErrInvalidOutput = errors.New("printed a line that is not an effect")
)

// Request is the JSON object a plugin reads from standard input.
type Request struct {
	// Protocol is set by the Runner; whatever the caller puts here is replaced.
	Protocol int `json:"protocol"`
	// Command is the name the user typed, the plugin's command.
	Command string `json:"command"`
	// Arguments is the rest of the command line, as one string.
	Arguments string `json:"arguments"`
	// Repository is the repository of the pull request.
	Repository Repository `json:"repository"`
	// Number is the pull request's number.
	Number int `json:"number"`
	// Head is the head commit the pull request had when the user ran the command.
	Head string `json:"head"`
	// Target is what the user pointed at. It is absent for a plugin whose
	// target is the pull request as a whole.
	Target *Target `json:"target,omitempty"`
}

// Repository names the repository a request is about.
type Repository struct {
	Host  string `json:"host"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// Target is the diff lines a `lines` plugin acts on.
type Target struct {
	// Path is the file's path in the pull request.
	Path string `json:"path"`
	// Side is "LEFT" for the old file and "RIGHT" for the new one.
	Side string `json:"side"`
	// Line is the last line of the selection on Side and StartLine the first;
	// they are equal for a single line.
	Line      int `json:"line"`
	StartLine int `json:"startLine"`
	// Excerpt is the selected lines with up to three lines of the hunk on each
	// side, one per line of text, each starting with "+", "-" or a space.
	Excerpt string `json:"excerpt"`
}

// Effect is one thing a plugin asked for. Text and Line are empty for effects
// that have none. It is untrusted: sanitise it where it is shown. The same goes for the
// text of an error effect that reaches the caller as the text of an error
// returned by Run: it is plugin-controlled, up to MaximumOutputSize long and
// may hold terminal escapes, so clean it (terminaltext.Clean) where it is shown.
type Effect struct {
	Name string
	Text string
	// Line is the command line of a command effect, without the ":". It is
	// as untrusted as Text.
	Line string
}

// Options are the inputs a Runner takes instead of reading globals.
type Options struct {
	// Environment is the environment in os.Environ form. The GitHub token
	// variables are removed before a plugin sees it.
	Environment []string
	// Logger receives the plugins' standard error and strig's notes about
	// their output. Nil discards it. Each line a plugin prints on standard
	// error becomes one record with the plugin's name as an attribute, so the
	// text is quoted by the log handler and cannot pass as a log line of
	// strig's own. It is never given a request.
	Logger *slog.Logger
	// WaitDelay overrides DefaultWaitDelay when positive.
	WaitDelay time.Duration
	// Home returns the home directory, for a program written as "~/...". It is
	// called only then. Nil means such a program cannot be started.
	Home func() (string, error)
}

// Runner runs the configured plugins, one run at a time per plugin.
type Runner struct {
	entries     map[string]*entry
	environment []string
	logger      *slog.Logger
	waitDelay   time.Duration
	home        func() (string, error)

	// Seams for tests: called when a run joins a queue, and to start a run's
	// timeout.
	queued      func(name string)
	withTimeout func(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc)
}

type entry struct {
	plugin config.Plugin
	queue  queue
}

// New returns a Runner for the plugins, which config has already validated.
func New(plugins []config.Plugin, options Options) *Runner {
	runner := &Runner{
		entries:     make(map[string]*entry, len(plugins)),
		environment: environment.Scrub(options.Environment),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		waitDelay:   DefaultWaitDelay,
		home:        options.Home,
		queued:      func(string) {},
		withTimeout: context.WithTimeout,
	}
	if options.Logger != nil {
		runner.logger = options.Logger
	}
	if options.WaitDelay > 0 {
		runner.waitDelay = options.WaitDelay
	}
	for _, plugin := range plugins {
		runner.entries[plugin.Command] = &entry{plugin: plugin}
	}
	return runner
}

// Run runs the plugin called name with the request and returns its effects in
// the order it printed them. If another run of the same plugin is under way,
// it waits its turn; cancelling ctx while waiting leaves the queue without
// running anything. The plugin's timeout starts when the run starts.
//
// A run fails, with no effects, if the plugin cannot be started, times out,
// prints too much or a line that is not an effect, or exits with a failure
// status. If ctx ends while the plugin runs, the plugin is killed and Run
// returns ctx.Err(). But a plugin that had already exited 0 when ctx ended has
// succeeded, so a cancelled caller can still receive effects with a nil
// error; it must not assume that a cancel means there are none.
//
// An error effect is an ordinary effect: a run can return one and succeed. If
// the plugin exits with a failure status after printing an error effect, the
// error carries that effect's text.
func (runner *Runner) Run(ctx context.Context, name string, request Request) ([]Effect, error) {
	request.Protocol = Protocol
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode request for plugin %q: %w", name, err)
	}
	found, ok := runner.entries[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q: %w", name, ErrUnknownPlugin)
	}
	// The newline lets a shell script read the request with a plain read.
	return runner.execute(ctx, found, append(body, '\n'))
}

func (runner *Runner) execute(ctx context.Context, found *entry, body []byte) ([]Effect, error) {
	name := found.plugin.Command
	if err := found.queue.acquire(ctx, func() { runner.queued(name) }); err != nil {
		return nil, err
	}
	defer found.queue.release()

	runContext, cancel := runner.withTimeout(ctx, found.plugin.Timeout)
	defer cancel()
	return runner.start(ctx, runContext, found.plugin, body)
}

// start runs the process. parent is the caller's context, runContext also has
// the timeout; comparing them tells a timeout from a cancellation.
func (runner *Runner) start(parent, runContext context.Context, plugin config.Plugin, body []byte) ([]Effect, error) {
	stopContext, stop := context.WithCancel(runContext)
	defer stop()

	program, err := runner.program(plugin.OnRun[0])
	if err != nil {
		return nil, fmt.Errorf("start plugin %q: %w", plugin.Command, err)
	}
	output := &limitedBuffer{limit: MaximumOutputSize, stop: stop}
	command := exec.CommandContext(stopContext, program, plugin.OnRun[1:]...)
	command.Env = runner.environment
	// A plugin that never reads its input gets EPIPE on the write; exec
	// ignores that, and the exit status decides.
	command.Stdin = bytes.NewReader(body)
	command.Stdout = output
	standardError, flushLog := runner.standardErrorSink(plugin.Command)
	defer flushLog()
	command.Stderr = &boundedLog{writer: standardError, remaining: MaximumErrorOutputSize, cutOff: func() {
		flushLog() // the line that was cut comes before the note about it
		runner.logger.Warn("plugin standard error cut off", "plugin", plugin.Command, "limit", MaximumErrorOutputSize)
	}}
	command.WaitDelay = runner.waitDelay
	detach(command)
	// Only a kill by strig counts as one: a plugin that crashed on a signal of
	// its own is a failure, whatever the clock says by the time the wait ends.
	var cancelKilled atomic.Bool
	command.Cancel = func() error {
		err := command.Process.Kill()
		if err == nil {
			cancelKilled.Store(true)
		}
		return err
	}

	waitError := command.Run()

	state := command.ProcessState
	switch classify(
		output.exceeded(),
		state != nil,
		// A process that exited on its own, even one not yet reaped when the
		// kill arrived, was not ended by the kill.
		cancelKilled.Load() && state != nil && !state.Exited(),
		state != nil && state.Success(),
		parent.Err() != nil,
		runContext.Err() != nil,
	) {
	case endTooLarge:
		return nil, fmt.Errorf("plugin %q: %w (limit %d bytes)", plugin.Command, ErrOutputTooLarge, MaximumOutputSize)
	case endCancelled:
		return nil, parent.Err()
	case endTimedOut:
		return nil, fmt.Errorf("plugin %q: %w after %s", plugin.Command, ErrTimeout, plugin.Timeout)
	case endSucceeded:
		// What the plugin printed is complete, also if only WaitDelay ended the
		// wait for a child holding standard output.
		effects, err := runner.parse(plugin.Command, output.bytes())
		if err != nil {
			return nil, err
		}
		return effects, nil
	case endFailed:
		return nil, runner.exitFailure(plugin.Command, output.bytes())
	default:
		return nil, fmt.Errorf("start plugin %q: %w", plugin.Command, waitError)
	}
}

// ending is how a process run ended.
type ending int

const (
	endTooLarge ending = iota
	endCancelled
	endTimedOut
	endSucceeded
	endFailed
	endNotStarted
)

// classify decides how a run ended. Unless strig killed the plugin (killed),
// its own ending decides, whatever the contexts say by then: they may have
// ended while strig waited out WaitDelay for a child holding standard output,
// and a plugin that crashed on a signal of its own was not killed by strig.
// started is whether the process ran at all.
func classify(tooLarge, started, killed, succeeded, cancelled, timedOut bool) ending {
	ended := killed || !started // strig ended the run, or it never began
	switch {
	case tooLarge:
		return endTooLarge
	case ended && cancelled:
		return endCancelled
	case ended && timedOut:
		return endTimedOut
	case succeeded:
		return endSucceeded
	case started:
		return endFailed
	default:
		return endNotStarted
	}
}

// program is the program to start. A bare name is found on PATH and an
// absolute path is used as it is (config accepts nothing else, so a checkout
// cannot supply the program); "~/" is the home directory.
func (runner *Runner) program(name string) (string, error) {
	rest, isHome := strings.CutPrefix(name, "~/")
	if !isHome {
		return name, nil
	}
	if runner.home == nil {
		return "", errors.New("no home directory to expand ~ in the program")
	}
	home, err := runner.home()
	if err != nil {
		return "", fmt.Errorf("expand ~ in the program: %w", err)
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("the home directory is not an absolute path")
	}
	return filepath.Join(home, rest), nil
}

// exitFailure is the error for a plugin that exited with a failure status:
// the text of the first error effect before any bad line, else a fixed message.
func (runner *Runner) exitFailure(name string, output []byte) error {
	effects, _ := runner.parse(name, output)
	for _, effect := range effects {
		if effect.Name == EffectError {
			return fmt.Errorf("plugin %q: %w", name, &reportedError{text: effect.Text})
		}
	}
	return fmt.Errorf("plugin %q: %w", name, ErrFailed)
}

// reportedError is a failure whose text the plugin chose.
type reportedError struct{ text string }

func (reported *reportedError) Error() string { return reported.text }

func (reported *reportedError) Unwrap() error { return ErrFailed }

// parse reads the effect lines in output. On a bad line it returns the effects
// before it and ErrInvalidOutput.
func (runner *Runner) parse(name string, output []byte) ([]Effect, error) {
	var effects []Effect
	for number, line := range bytes.Split(output, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		effect, known, err := decodeEffect(line)
		if err != nil {
			return effects, fmt.Errorf("plugin %q: line %d: %w", name, number+1, ErrInvalidOutput)
		}
		if !known {
			runner.skippedEffect(name, effect.Name)
			continue
		}
		effects = append(effects, effect)
	}
	return effects, nil
}

// decodeEffect decodes one line. known is false for an effect name this
// version does not have; a newer plugin can print those.
func decodeEffect(line []byte) (effect Effect, known bool, err error) {
	var decoded struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(line, &decoded); err != nil {
		return Effect{}, false, err
	}
	if decoded.Name == "" {
		return Effect{}, false, errors.New("effect has no name")
	}
	effect.Name = decoded.Name
	switch decoded.Name {
	case EffectReload:
		if err := decodeData(decoded.Data, &struct{}{}); err != nil {
			return Effect{}, false, err
		}
		return effect, true, nil
	case EffectNotice, EffectError, EffectAnswer:
		var data struct {
			Text string `json:"text"`
		}
		if err := decodeData(decoded.Data, &data); err != nil {
			return Effect{}, false, err
		}
		if data.Text == "" {
			return Effect{}, false, errors.New("effect has no text")
		}
		effect.Text = data.Text
		return effect, true, nil
	case EffectCommand:
		var data struct {
			Line string `json:"line"`
		}
		if err := decodeData(decoded.Data, &data); err != nil {
			return Effect{}, false, err
		}
		if strings.TrimSpace(data.Line) == "" {
			return Effect{}, false, errors.New("effect has no line")
		}
		effect.Line = data.Line
		return effect, true, nil
	default:
		return effect, false, nil
	}
}

// decodeData decodes the data of an effect into target. Missing data and null
// are the same as an empty object; anything else must be an object.
func decodeData(raw json.RawMessage, target any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] != '{' {
		return errors.New("data is not an object")
	}
	return json.Unmarshal(raw, target)
}

// queue lets one run at a time hold a plugin and hands it to the waiting runs
// in the order they arrived.
type queue struct {
	mutex   sync.Mutex
	busy    bool
	waiting []chan struct{}
}

// acquire waits for the plugin to be free. joined is called, without the lock,
// once the run is in the line. It returns ctx.Err() if ctx ends first, having
// left the line.
func (line *queue) acquire(ctx context.Context, joined func()) error {
	line.mutex.Lock()
	if !line.busy {
		line.busy = true
		line.mutex.Unlock()
		return nil
	}
	turn := make(chan struct{})
	line.waiting = append(line.waiting, turn)
	line.mutex.Unlock()
	joined()

	select {
	case <-turn:
		return nil
	case <-ctx.Done():
	}
	line.mutex.Lock()
	select {
	case <-turn:
		// The turn arrived as the context ended: pass it on.
		line.mutex.Unlock()
		line.release()
	default:
		for i, waiter := range line.waiting {
			if waiter == turn {
				line.waiting = append(line.waiting[:i], line.waiting[i+1:]...)
				break
			}
		}
		line.mutex.Unlock()
	}
	return ctx.Err()
}

// release hands the plugin to the next waiting run, or frees it.
func (line *queue) release() {
	line.mutex.Lock()
	defer line.mutex.Unlock()
	if len(line.waiting) == 0 {
		line.busy = false
		return
	}
	next := line.waiting[0]
	line.waiting = line.waiting[1:]
	close(next)
}

// limitedBuffer keeps a plugin's output up to a limit. Past it, it stops the
// process and refuses more, so output is never buffered without bound. exec
// writes from one goroutine and joins it before Run returns, and everything
// here is read after that, so it needs no lock.
type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	stop     context.CancelFunc
	overflow bool
}

var errOutputLimit = errors.New("output limit reached")

func (limited *limitedBuffer) Write(data []byte) (int, error) {
	if limited.overflow || limited.buffer.Len()+len(data) > limited.limit {
		limited.overflow = true
		limited.stop()
		return 0, errOutputLimit
	}
	return limited.buffer.Write(data)
}

func (limited *limitedBuffer) exceeded() bool { return limited.overflow }

func (limited *limitedBuffer) bytes() []byte { return limited.buffer.Bytes() }

// boundedLog passes the first remaining bytes of a plugin's standard error to
// the log, adds one note, and drops the rest. It always reports success: a
// chatty plugin or a failing log must not decide the run, and the plugin must
// never get EPIPE from it.
type boundedLog struct {
	writer    io.Writer
	remaining int
	truncated bool
	cutOff    func() // logs strig's own note, once
}

func (bounded *boundedLog) Write(data []byte) (int, error) {
	total := len(data)
	if bounded.remaining > 0 {
		kept := min(total, bounded.remaining)
		_, _ = bounded.writer.Write(data[:kept])
		bounded.remaining -= kept
		data = data[kept:]
	}
	if len(data) > 0 && !bounded.truncated {
		bounded.truncated = true
		bounded.cutOff()
	}
	return total, nil
}
