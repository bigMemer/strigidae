//go:build unix

package plugins

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
)

// guard bounds a wait that only a broken implementation would not finish.
const guard = 20 * time.Second

func scriptPlugin(command, script string, timeout time.Duration) config.Plugin {
	return config.Plugin{
		Command: command,
		OnRun:   []string{filepath.Join("testdata", script)},
		Target:  config.TargetLines,
		Timeout: timeout,
	}
}

func baseEnvironment(directory string, extra ...string) []string {
	return append([]string{"PATH=" + os.Getenv("PATH"), "FAKE_DIRECTORY=" + directory}, extra...)
}

func receive[Value any](test *testing.T, channel <-chan Value) Value {
	test.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(guard):
		test.Fatal("timed out waiting")
		panic("unreachable")
	}
}

type outcome struct {
	effects []Effect
	err     error
}

func runInBackground(ctx context.Context, runner *Runner, name string, body string) <-chan outcome {
	results := make(chan outcome, 1)
	go func() {
		effects, err := runner.execute(ctx, runner.entries[name], []byte(body))
		results <- outcome{effects, err}
	}()
	return results
}

func TestRunEffects(test *testing.T) {
	notice := func(text string) Effect { return Effect{Name: EffectNotice, Text: text} }
	for _, testCase := range []struct {
		name      string
		output    string
		exit      int
		want      []Effect
		wantError string
		wantIs    error
		wantLog   string
	}{
		{name: "no output", want: nil},
		{name: "notice", output: `{"name":"notice","data":{"text":"hi"}}` + "\n", want: []Effect{notice("hi")}},
		{name: "answer", output: `{"name":"answer","data":{"text":"42"}}` + "\n", want: []Effect{{Name: EffectAnswer, Text: "42"}}},
		{name: "reload", output: `{"name":"reload","data":{}}` + "\n", want: []Effect{{Name: EffectReload}}},
		{name: "reload without data", output: `{"name":"reload"}` + "\n", want: []Effect{{Name: EffectReload}}},
		{name: "last line without newline", output: `{"name":"notice","data":{"text":"hi"}}`, want: []Effect{notice("hi")}},
		{
			name:   "error is an effect, not a failure",
			output: `{"name":"error","data":{"text":"nope"}}` + "\n",
			want:   []Effect{{Name: EffectError, Text: "nope"}},
		},
		{
			name: "order is kept, blank lines and carriage returns skipped",
			output: `{"name":"notice","data":{"text":"1"}}` + "\r\n\n   \n" +
				`{"name":"answer","data":{"text":"2"}}` + "\n" +
				`{"name":"reload","data":{}}` + "\n" +
				`{"name":"notice","data":{"text":"3"}}` + "\n",
			want: []Effect{notice("1"), {Name: EffectAnswer, Text: "2"}, {Name: EffectReload}, notice("3")},
		},
		{
			name: "unknown effect is skipped and logged",
			output: `{"name":"notice","data":{"text":"a"}}` + "\n" +
				`{"name":"future","data":{"x":1}}` + "\n" +
				`{"name":"notice","data":{"text":"b"}}` + "\n",
			want:    []Effect{notice("a"), notice("b")},
			wantLog: `effect=future`,
		},
		{name: "not JSON", output: "hello\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{
			name:      "bad line after a good one",
			output:    `{"name":"notice","data":{"text":"a"}}` + "\nhello\n",
			wantIs:    ErrInvalidOutput,
			wantError: `plugin "p": line 2: printed a line that is not an effect`,
		},
		{name: "not an object", output: "[1]\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "no name", output: `{"data":{}}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "text effect with data that is not an object", output: `{"name":"notice","data":"hi"}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "notice without data", output: `{"name":"notice"}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "notice with empty data", output: `{"name":"notice","data":{}}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "notice with null data", output: `{"name":"notice","data":null}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "notice with empty text", output: `{"name":"notice","data":{"text":""}}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "notice with text that is not text", output: `{"name":"notice","data":{"text":5}}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "answer with a list as data", output: `{"name":"answer","data":[]}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "reload with a number as data", output: `{"name":"reload","data":5}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "reload with a list as data", output: `{"name":"reload","data":[]}` + "\n", wantIs: ErrInvalidOutput, wantError: `plugin "p": line 1: printed a line that is not an effect`},
		{name: "reload with null data", output: `{"name":"reload","data":null}` + "\n", want: []Effect{{Name: EffectReload}}},
		{name: "error with empty text is no error effect", output: `{"name":"error","data":{}}` + "\n", exit: 1, wantIs: ErrFailed, wantError: `plugin "p": failed`},
		{name: "error effect after a bad line is not used", output: "hello\n" + `{"name":"error","data":{"text":"late"}}` + "\n", exit: 1, wantIs: ErrFailed, wantError: `plugin "p": failed`},
		{name: "failure without an error effect", exit: 2, wantIs: ErrFailed, wantError: `plugin "p": failed`},
		{
			name:      "failure with other effects only",
			output:    `{"name":"notice","data":{"text":"a"}}` + "\n",
			exit:      1,
			wantIs:    ErrFailed,
			wantError: `plugin "p": failed`,
		},
		{
			name:      "failure reports the error effect's text",
			output:    `{"name":"notice","data":{"text":"a"}}` + "\n" + `{"name":"error","data":{"text":"no model"}}` + "\n",
			exit:      1,
			wantIs:    ErrFailed,
			wantError: `plugin "p": no model`,
		},
		{name: "failure with unreadable output", output: "garbage\n", exit: 1, wantIs: ErrFailed, wantError: `plugin "p": failed`},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			var log bytes.Buffer
			runner := New(
				[]config.Plugin{scriptPlugin("p", "emit.sh", guard)},
				Options{
					Environment: baseEnvironment(test.TempDir(), "FAKE_OUTPUT="+testCase.output, "FAKE_EXIT="+strconv.Itoa(testCase.exit)),
					Logger:      textLogger(&log),
				},
			)
			got, err := runner.Run(context.Background(), "p", Request{})
			if testCase.wantError == "" {
				if err != nil {
					test.Fatalf("Run: %v", err)
				}
			} else {
				if err == nil || err.Error() != testCase.wantError {
					test.Fatalf("error = %v, want %q", err, testCase.wantError)
				}
				if !errors.Is(err, testCase.wantIs) {
					test.Errorf("error %v is not %v", err, testCase.wantIs)
				}
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("effects mismatch (-want +got):\n%s", diff)
			}
			if !strings.Contains(log.String(), testCase.wantLog) {
				test.Errorf("log = %q, want it to contain %q", log.String(), testCase.wantLog)
			}
		})
	}
}

func TestRunWritesProtocolRequest(test *testing.T) {
	linesRequest := Request{
		Protocol:   99, // the caller cannot change the version
		Command:    "ask",
		Arguments:  "why is this <here>?",
		Repository: Repository{Host: "ghe.example.com", Owner: "acme", Name: "widgets"},
		Number:     7,
		Head:       "abc123",
		Target:     &Target{Path: "a/b.go", Side: "RIGHT", Line: 12, StartLine: 10, Excerpt: " one\n+two\n-three"},
	}
	for _, testCase := range []struct {
		name    string
		request Request
		want    string
	}{
		{
			"lines",
			linesRequest,
			`{"protocol":1,"command":"ask","arguments":"why is this \u003chere\u003e?","repository":{"host":"ghe.example.com","owner":"acme","name":"widgets"},` +
				`"number":7,"head":"abc123","target":{"path":"a/b.go","side":"RIGHT","line":12,"startLine":10,"excerpt":" one\n+two\n-three"}}`,
		},
		{
			"pull request has no target",
			Request{Command: "summarize", Repository: Repository{Host: "github.com", Owner: "acme", Name: "widgets"}, Number: 7, Head: "abc123"},
			`{"protocol":1,"command":"summarize","arguments":"","repository":{"host":"github.com","owner":"acme","name":"widgets"},"number":7,"head":"abc123"}`,
		},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			directory := test.TempDir()
			runner := New([]config.Plugin{scriptPlugin("p", "save-input.sh", guard)}, Options{Environment: baseEnvironment(directory)})
			if _, err := runner.Run(context.Background(), "p", testCase.request); err != nil {
				test.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(directory, "input"))
			if err != nil {
				test.Fatal(err)
			}
			if diff := cmp.Diff(testCase.want+"\n", string(got)); diff != "" {
				test.Errorf("stdin mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunUnknownPlugin(test *testing.T) {
	runner := New(nil, Options{})
	_, err := runner.Run(context.Background(), "nothing", Request{})
	if !errors.Is(err, ErrUnknownPlugin) {
		test.Errorf("error = %v, want ErrUnknownPlugin", err)
	}
}

func TestRunStartFailure(test *testing.T) {
	runner := New([]config.Plugin{{Command: "p", OnRun: []string{"testdata/does-not-exist"}, Timeout: guard}}, Options{})
	_, err := runner.Run(context.Background(), "p", Request{})
	if err == nil || !strings.Contains(err.Error(), `start plugin "p"`) {
		test.Errorf("error = %v, want a start failure", err)
	}
	if errors.Is(err, ErrFailed) {
		test.Errorf("a plugin that never started did not fail by exit status: %v", err)
	}
}

func TestRunStderrGoesToTheLogOnly(test *testing.T) {
	var log bytes.Buffer
	runner := New([]config.Plugin{scriptPlugin("p", "noisy-failure.sh", guard)}, Options{Logger: textLogger(&log)})
	_, err := runner.Run(context.Background(), "p", Request{})
	if !errors.Is(err, ErrFailed) {
		test.Fatalf("error = %v, want ErrFailed", err)
	}
	if !strings.Contains(log.String(), "details only for the log") {
		test.Errorf("log = %q, want the plugin's standard error", log.String())
	}
	if strings.Contains(err.Error(), "details only") {
		test.Errorf("error %q carries standard error", err)
	}
	if strings.Contains(log.String(), "protocol") {
		test.Errorf("log %q contains the request", log.String())
	}
}

func TestRunRemovesTokenVariables(test *testing.T) {
	runner := New([]config.Plugin{scriptPlugin("p", "show-token.sh", guard)}, Options{
		Environment: baseEnvironment(test.TempDir(), "GH_TOKEN=a", "GITHUB_TOKEN=b", "GH_ENTERPRISE_TOKEN=c", "GITHUB_ENTERPRISE_TOKEN=d", "KEPT=yes"),
	})
	got, err := runner.Run(context.Background(), "p", Request{})
	if err != nil {
		test.Fatal(err)
	}
	want := []Effect{{Name: EffectNotice, Text: "unset|unset|unset|unset|yes"}}
	if diff := cmp.Diff(want, got); diff != "" {
		test.Errorf("effects mismatch (-want +got):\n%s", diff)
	}
}

func TestRunTimeout(test *testing.T) {
	runner := New([]config.Plugin{scriptPlugin("p", "hang.sh", 200*time.Millisecond)}, Options{})
	_, err := runner.Run(context.Background(), "p", Request{})
	if !errors.Is(err, ErrTimeout) {
		test.Errorf("error = %v, want ErrTimeout", err)
	}
}

// The script ignores SIGPIPE, so it only stops if strig kills it; the
// timeout is far above guard, so a missing kill fails the test.
func TestRunOutputLimit(test *testing.T) {
	runner := New([]config.Plugin{scriptPlugin("p", "ignore-sigpipe.sh", time.Hour)}, Options{})
	result := receive(test, runInBackground(context.Background(), runner, "p", "{}"))
	if !errors.Is(result.err, ErrOutputTooLarge) {
		test.Errorf("error = %v, want ErrOutputTooLarge", result.err)
	}
	if result.effects != nil {
		test.Errorf("effects = %v, want none from a failed run", result.effects)
	}
}

func TestRunStderrIsBounded(test *testing.T) {
	var log bytes.Buffer
	runner := New([]config.Plugin{scriptPlugin("p", "stderr-flood.sh", guard)}, Options{Logger: textLogger(&log)})
	got, err := runner.Run(context.Background(), "p", Request{})
	if err != nil {
		test.Fatal(err)
	}
	if diff := cmp.Diff([]Effect{{Name: EffectNotice, Text: "ok"}}, got); diff != "" {
		test.Errorf("effects mismatch (-want +got):\n%s", diff)
	}
	if log.Len() > 4*MaximumErrorOutputSize {
		test.Errorf("log holds %d bytes, want about %d", log.Len(), MaximumErrorOutputSize)
	}
	if strings.Count(log.String(), `msg="plugin standard error cut off" plugin=p`) != 1 {
		test.Errorf("log does not hold exactly one cut-off record of strig's own")
	}
	// The note is strig's record, never a line the plugin printed.
	if strings.Contains(log.String(), `line="plugin`) || strings.Contains(log.String(), `line=\"plugin`) {
		test.Errorf("the cut-off note was logged as a line of the plugin's standard error")
	}
}

// The line that was cut is logged before strig's note about the cut.
func TestRunCutOffNoteComesAfterTheCutLine(test *testing.T) {
	var log bytes.Buffer
	runner := New([]config.Plugin{scriptPlugin("p", "stderr-long-line.sh", guard)}, Options{Logger: textLogger(&log)})
	if _, err := runner.Run(context.Background(), "p", Request{}); err != nil {
		test.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(log.String(), "\n"), "\n")
	if len(lines) != 2 {
		test.Fatalf("log has %d records, want the cut line and the note:\n%.300s", len(lines), log.String())
	}
	if !strings.Contains(lines[0], `msg="plugin standard error" plugin=p line=aaaa`) {
		test.Errorf("first record = %.120s, want the line that was cut", lines[0])
	}
	if !strings.Contains(lines[1], `msg="plugin standard error cut off" plugin=p`) {
		test.Errorf("second record = %.200s, want the note", lines[1])
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestRunLogFailureDoesNotDecideTheRun(test *testing.T) {
	runner := New([]config.Plugin{scriptPlugin("p", "stderr-flood.sh", guard)}, Options{Logger: textLogger(failingWriter{})})
	got, err := runner.Run(context.Background(), "p", Request{})
	if err != nil {
		test.Fatalf("Run: %v", err)
	}
	if diff := cmp.Diff([]Effect{{Name: EffectNotice, Text: "ok"}}, got); diff != "" {
		test.Errorf("effects mismatch (-want +got):\n%s", diff)
	}
}

func TestRunAfterAFailedRun(test *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, testCase := range []struct {
		name        string
		script      string
		environment []string
		timeout     time.Duration
		context     context.Context
	}{
		{"exit status", "emit.sh", []string{"FAKE_EXIT=1"}, guard, context.Background()},
		{"timeout", "hang.sh", nil, 100 * time.Millisecond, context.Background()},
		{"cancelled", "hang.sh", nil, guard, cancelled},
		{"too much output", "ignore-sigpipe.sh", nil, time.Hour, context.Background()},
		{"start failure", "does-not-exist", nil, guard, context.Background()},
		{"bad output", "emit.sh", []string{"FAKE_OUTPUT=hello"}, guard, context.Background()},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			runner := New(
				[]config.Plugin{scriptPlugin("p", testCase.script, testCase.timeout)},
				Options{Environment: baseEnvironment(test.TempDir(), testCase.environment...)},
			)
			first := receive(test, runInBackground(testCase.context, runner, "p", "{}"))
			if first.err == nil {
				test.Fatal("the first run succeeded, want a failure")
			}
			// The same plugin must be free again.
			runner.entries["p"].plugin = scriptPlugin("p", "ignore-input.sh", guard)
			if second := receive(test, runInBackground(context.Background(), runner, "p", "{}")); second.err != nil {
				test.Errorf("second run: %v", second.err)
			}
		})
	}
}

func TestQueueTurnArrivingWithCancel(test *testing.T) {
	line := &queue{}
	if err := line.acquire(context.Background(), func() {}); err != nil {
		test.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	joined := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- line.acquire(ctx, func() { close(joined) }) }()
	receive(test, joined)

	// Cancel and hand the turn over while the waiter has to take the lock.
	line.mutex.Lock()
	cancel()
	next := line.waiting[0]
	line.waiting = line.waiting[1:]
	close(next)
	line.mutex.Unlock()

	// The waiter may have seen either: nil means it took the turn, which it
	// must give back; Canceled means it passed the turn on itself.
	switch err := receive(test, result); {
	case err == nil:
		line.release()
	case !errors.Is(err, context.Canceled):
		test.Fatalf("waiter error = %v, want nil or context.Canceled", err)
	}
	free := make(chan error, 1)
	go func() { free <- line.acquire(context.Background(), func() {}) }()
	if err := receive(test, free); err != nil {
		test.Errorf("acquire after the race: %v", err)
	}
}

func TestBareProgramIsNotFoundInTheWorkingDirectory(test *testing.T) {
	directory := test.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "ignore-input.sh"))
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "local-plugin"), script, 0o700); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", "/usr/bin:/bin")
	test.Chdir(directory)
	runner := New([]config.Plugin{{Command: "p", OnRun: []string{"local-plugin"}, Timeout: guard}}, Options{})
	_, runError := runner.Run(context.Background(), "p", Request{})
	if !errors.Is(runError, exec.ErrNotFound) {
		test.Errorf("error = %v, want exec.ErrNotFound", runError)
	}
}

func TestRunExpandsHomeInTheProgram(test *testing.T) {
	home := test.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "emit.sh"))
	if err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "plugin.sh"), script, 0o700); err != nil {
		test.Fatal(err)
	}
	plugin := config.Plugin{Command: "p", OnRun: []string{"~/plugin.sh"}, Timeout: guard}
	environment := baseEnvironment(home, `FAKE_OUTPUT={"name":"reload"}`)

	runner := New([]config.Plugin{plugin}, Options{Environment: environment, Home: func() (string, error) { return home, nil }})
	got, runError := runner.Run(context.Background(), "p", Request{})
	if runError != nil || len(got) != 1 {
		test.Errorf("Run = %v, %v, want one effect", got, runError)
	}

	withoutHome := New([]config.Plugin{plugin}, Options{Environment: environment})
	if _, runError := withoutHome.Run(context.Background(), "p", Request{}); runError == nil {
		test.Error("Run without a home directory succeeded")
	}
}

func TestRunExitedPluginIsNotTimedOut(test *testing.T) {
	directory := test.TempDir()
	killChild(test, directory)
	// The plugin exits at once but a child holds standard output, so Run waits
	// out WaitDelay; the timeout passes meanwhile.
	runner := New(
		[]config.Plugin{scriptPlugin("p", "leave-child.sh", time.Second)},
		Options{Environment: baseEnvironment(directory), WaitDelay: 2 * time.Second},
	)
	result := receive(test, runInBackground(context.Background(), runner, "p", "{}"))
	if result.err != nil {
		test.Fatalf("Run: %v", result.err)
	}
	if diff := cmp.Diff([]Effect{{Name: EffectNotice, Text: "done"}}, result.effects); diff != "" {
		test.Errorf("effects mismatch (-want +got):\n%s", diff)
	}
}

// The plugin exits 0 at once and a child holds standard output; the caller's
// context ends while Run waits out WaitDelay. The exit status decides.
func TestRunCancelledDuringTheWaitAfterExit(test *testing.T) {
	directory := test.TempDir()
	killChild(test, directory)
	runner := New(
		[]config.Plugin{scriptPlugin("p", "leave-child.sh", guard)},
		Options{Environment: baseEnvironment(directory), WaitDelay: 3 * time.Second},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(time.Second, cancel)
	defer timer.Stop()
	result := receive(test, runInBackground(ctx, runner, "p", "{}"))
	if result.err != nil {
		test.Fatalf("Run: %v", result.err)
	}
	if diff := cmp.Diff([]Effect{{Name: EffectNotice, Text: "done"}}, result.effects); diff != "" {
		test.Errorf("effects mismatch (-want +got):\n%s", diff)
	}
}

func TestLimitedBufferStopsAtTheLimit(test *testing.T) {
	stopped := false
	buffer := &limitedBuffer{limit: 5, stop: func() { stopped = true }}
	if _, err := buffer.Write([]byte("abcde")); err != nil || stopped {
		test.Fatalf("write at the limit: err = %v, stopped = %v", err, stopped)
	}
	if _, err := buffer.Write([]byte("f")); err == nil || !stopped || !buffer.exceeded() {
		test.Errorf("write past the limit: err = %v, stopped = %v, exceeded = %v", err, stopped, buffer.exceeded())
	}
	if diff := cmp.Diff("abcde", string(buffer.bytes())); diff != "" {
		test.Errorf("kept bytes mismatch (-want +got):\n%s", diff)
	}
}

// killChild stops the child a script left behind, so no test leaves a process.
func killChild(test *testing.T, directory string) {
	test.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(directory, "child-process"))
		if err != nil {
			return
		}
		if processIdentifier, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			_ = syscall.Kill(processIdentifier, syscall.SIGKILL)
		}
	})
}

func TestRunChildHoldingStandardOutput(test *testing.T) {
	for _, testCase := range []struct {
		name   string
		script string
		want   []Effect
		wantIs error
		// useDefaultDelay leaves Options.WaitDelay unset; then the run must end
		// within a tolerant limit well above the default of about a second.
		useDefaultDelay bool
	}{
		{"plugin exits 0: its effects are complete", "leave-child.sh", []Effect{{Name: EffectNotice, Text: "done"}}, nil, false},
		{"plugin exits 1: still a failure", "leave-child-failing.sh", nil, ErrFailed, false},
		{"default wait delay", "leave-child.sh", []Effect{{Name: EffectNotice, Text: "done"}}, nil, true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			directory := test.TempDir()
			killChild(test, directory)
			options := Options{Environment: baseEnvironment(directory), WaitDelay: 100 * time.Millisecond}
			if testCase.useDefaultDelay {
				options.WaitDelay = 0
			}
			runner := New([]config.Plugin{scriptPlugin("p", testCase.script, guard)}, options)
			started := time.Now()
			results := runInBackground(context.Background(), runner, "p", "{}")
			result := receive(test, results)
			if elapsed := time.Since(started); testCase.useDefaultDelay && elapsed > 5*time.Second {
				test.Errorf("run took %s, want the default wait delay of about a second", elapsed)
			}
			if testCase.wantIs == nil && result.err != nil {
				test.Fatalf("Run: %v", result.err)
			}
			if testCase.wantIs != nil && !errors.Is(result.err, testCase.wantIs) {
				test.Fatalf("error = %v, want %v", result.err, testCase.wantIs)
			}
			if diff := cmp.Diff(testCase.want, result.effects); diff != "" {
				test.Errorf("effects mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunPluginThatIgnoresItsInput(test *testing.T) {
	runner := New([]config.Plugin{scriptPlugin("p", "ignore-input.sh", guard)}, Options{})
	// Larger than a pipe buffer, so the write cannot finish before the exit.
	body := strings.Repeat("x", 4<<20)
	result := receive(test, runInBackground(context.Background(), runner, "p", body))
	if result.err != nil || result.effects != nil {
		test.Errorf("result = %+v, want a plain success", result)
	}
}

func TestRunPlainReadGetsTheWholeRequest(test *testing.T) {
	directory := test.TempDir()
	runner := New([]config.Plugin{scriptPlugin("p", "read-request.sh", guard)}, Options{Environment: baseEnvironment(directory)})
	if _, err := runner.Run(context.Background(), "p", Request{}); err != nil {
		test.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(directory, "line"))
	if err != nil {
		test.Fatal(err)
	}
	want := `{"protocol":1,"command":"","arguments":"","repository":{"host":"","owner":"","name":""},"number":0,"head":""}`
	if diff := cmp.Diff(want, string(got)); diff != "" {
		test.Errorf("request read mismatch (-want +got):\n%s", diff)
	}
}

// The plugin printed its reason and exited 1 by itself; a child holds standard
// output past the timeout. Its exit status decides, not the timeout.
func TestRunExitedWithErrorPastTheTimeout(test *testing.T) {
	directory := test.TempDir()
	killChild(test, directory)
	runner := New(
		[]config.Plugin{scriptPlugin("p", "error-and-leave-child.sh", 500*time.Millisecond)},
		Options{Environment: baseEnvironment(directory), WaitDelay: 1500 * time.Millisecond},
	)
	result := receive(test, runInBackground(context.Background(), runner, "p", "{}"))
	if result.err == nil || result.err.Error() != `plugin "p": no model` {
		test.Errorf("error = %v, want the plugin's text", result.err)
	}
}

func TestClassify(test *testing.T) {
	for _, testCase := range []struct {
		name                                                      string
		tooLarge, started, killed, succeeded, cancelled, timedOut bool
		want                                                      ending
	}{
		{"exit 0", false, true, false, true, false, false, endSucceeded},
		{"exit 0 as the timeout lands", false, true, false, true, false, true, endSucceeded},
		{"exit 0 as the caller cancels", false, true, false, true, true, false, endSucceeded},
		{"exit 1", false, true, false, false, false, false, endFailed},
		{"exit 1 past the timeout", false, true, false, false, false, true, endFailed},
		{"exit 1 after a cancel", false, true, false, false, true, false, endFailed},
		{"killed by the timeout", false, true, true, false, false, true, endTimedOut},
		{"killed by a cancel", false, true, true, false, true, false, endCancelled},
		{"cancel and timeout together is a cancel", false, true, true, false, true, true, endCancelled},
		{"crashed by a signal", false, true, false, false, false, false, endFailed},
		{"crashed by a signal past the timeout", false, true, false, false, false, true, endFailed},
		{"crashed by a signal after a cancel", false, true, false, false, true, false, endFailed},
		{"too much output beats a context that ended", true, true, true, false, true, true, endTooLarge},
		{"never started", false, false, false, false, false, false, endNotStarted},
		{"cancelled before starting", false, false, false, false, true, false, endCancelled},
		{"timed out before starting", false, false, false, false, false, true, endTimedOut},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			got := classify(testCase.tooLarge, testCase.started, testCase.killed, testCase.succeeded, testCase.cancelled, testCase.timedOut)
			if got != testCase.want {
				test.Errorf("classify = %d, want %d", got, testCase.want)
			}
		})
	}
}

// The plugin dies on a signal strig did not send, a child holds standard
// output, and the timeout passes while strig waits out WaitDelay. It crashed;
// it was not killed for being slow, and its own text is the reason.
func TestRunCrashedPluginPastTheTimeout(test *testing.T) {
	directory := test.TempDir()
	killChild(test, directory)
	runner := New(
		[]config.Plugin{scriptPlugin("p", "crash-and-leave-child.sh", 500*time.Millisecond)},
		Options{Environment: baseEnvironment(directory), WaitDelay: 1500 * time.Millisecond},
	)
	result := receive(test, runInBackground(context.Background(), runner, "p", "{}"))
	if result.err == nil || result.err.Error() != `plugin "p": out of memory` {
		test.Errorf("error = %v, want the plugin's text", result.err)
	}
	if errors.Is(result.err, ErrTimeout) {
		test.Errorf("error = %v, want a failure and not a timeout", result.err)
	}
}

// With a logger, each line of standard error is one record that names the
// plugin, and nothing the plugin wrote can pass as a record of strig's own or
// reach a terminal.
func TestRunStandardErrorGoesThroughTheLogger(test *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	runner := New([]config.Plugin{scriptPlugin("p", "hostile-stderr.sh", guard)}, Options{Logger: logger})
	if _, err := runner.Run(context.Background(), "p", Request{}); err != nil {
		test.Fatal(err)
	}
	if strings.ContainsRune(log.String(), '\x1b') {
		test.Errorf("log holds a raw escape: %q", log.String())
	}
	lines := strings.Split(strings.TrimSuffix(log.String(), "\n"), "\n")
	if len(lines) != 3 {
		test.Fatalf("log has %d lines, want one record per line of standard error:\n%s", len(lines), log.String())
	}
	for _, line := range lines {
		if !strings.Contains(line, `msg="plugin standard error" plugin=p line=`) {
			test.Errorf("record %q does not name the plugin and hold the text as an attribute", line)
		}
	}
	if !strings.Contains(lines[2], `line="no newline at the end"`) {
		test.Errorf("last record = %q, want the unfinished line", lines[2])
	}
}

// The plugin has a session of its own, so it has no controlling terminal.
func TestRunPluginIsASessionLeader(test *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		test.Skip("no ps to read the session from")
	}
	runner := New([]config.Plugin{scriptPlugin("p", "session.sh", guard)}, Options{Environment: baseEnvironment(test.TempDir())})
	effects, err := runner.Run(context.Background(), "p", Request{})
	if err != nil || len(effects) != 1 {
		test.Fatalf("Run: effects = %v, err = %v", effects, err)
	}
	processText, sessionText, _ := strings.Cut(effects[0].Text, " ")
	if _, err := strconv.Atoi(sessionText); err != nil {
		test.Skipf("ps cannot print a session number: %q", effects[0].Text)
	}
	if processText != sessionText {
		test.Errorf("process %s is in session %s, want it to lead its own", processText, sessionText)
	}
	own, err := exec.Command("ps", "-o", "sid=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		test.Fatal(err)
	}
	if sessionText == strings.TrimSpace(string(own)) {
		test.Errorf("plugin shares strig's session %s", sessionText)
	}
}

func TestRunRelativeHomeIsRefused(test *testing.T) {
	plugin := config.Plugin{Command: "p", OnRun: []string{"~/bin/x"}, Timeout: guard}
	for _, home := range []string{"", "relative/home"} {
		runner := New([]config.Plugin{plugin}, Options{Home: func() (string, error) { return home, nil }})
		_, err := runner.Run(context.Background(), "p", Request{})
		if err == nil || !strings.Contains(err.Error(), "not an absolute path") {
			test.Errorf("home %q: error = %v, want a refusal", home, err)
		}
	}
}

func textLogger(writer io.Writer) *slog.Logger { return slog.New(slog.NewTextHandler(writer, nil)) }

// Two plugins write to one log at once; under -race a data race is found.
func TestRunPluginsShareOneLog(test *testing.T) {
	var log bytes.Buffer
	runner := New(
		[]config.Plugin{scriptPlugin("a", "stderr-flood.sh", guard), scriptPlugin("b", "stderr-flood.sh", guard)},
		Options{Logger: textLogger(&log)},
	)
	first := runInBackground(context.Background(), runner, "a", "{}")
	second := runInBackground(context.Background(), runner, "b", "{}")
	for _, channel := range []<-chan outcome{first, second} {
		if result := receive(test, channel); result.err != nil {
			test.Errorf("run: %v", result.err)
		}
	}
}

// queueHarness runs gated.sh, which holds its plugin until the test creates
// the gate file.
type queueHarness struct {
	runner    *Runner
	directory string
	started   chan string
	queued    chan string
}

func newQueueHarness(test *testing.T, names ...string) *queueHarness {
	test.Helper()
	directory := test.TempDir()
	pipePath := filepath.Join(directory, "started")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		test.Fatal(err)
	}
	// Read-write, so opening never blocks and the script's writes never do.
	file, err := os.OpenFile(pipePath, os.O_RDWR, 0)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = file.Close() })
	harness := &queueHarness{directory: directory, started: make(chan string, 64), queued: make(chan string, 64)}
	go func() {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			harness.started <- scanner.Text()
		}
	}()

	var plugins []config.Plugin
	for _, name := range names {
		plugins = append(plugins, scriptPlugin(name, "gated.sh", guard))
	}
	// A failing test may not reach openGate; let the scripts finish anyway.
	test.Cleanup(func() { _ = os.WriteFile(filepath.Join(directory, "gate"), nil, 0o600) })
	harness.runner = New(plugins, Options{Environment: baseEnvironment(directory)})
	harness.runner.queued = func(name string) { harness.queued <- name }
	return harness
}

func (harness *queueHarness) openGate(test *testing.T) {
	test.Helper()
	if err := os.WriteFile(filepath.Join(harness.directory, "gate"), nil, 0o600); err != nil {
		test.Fatal(err)
	}
}

func (harness *queueHarness) read(test *testing.T, name string) string {
	test.Helper()
	data, err := os.ReadFile(filepath.Join(harness.directory, name))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		test.Fatal(err)
	}
	return string(data)
}

func TestQueueRunsOneAtATimeInOrder(test *testing.T) {
	harness := newQueueHarness(test, "p")
	results := map[string]<-chan outcome{}
	results["a"] = runInBackground(context.Background(), harness.runner, "p", "a")
	receive(test, harness.started)
	for _, label := range []string{"b", "c", "d"} {
		results[label] = runInBackground(context.Background(), harness.runner, "p", label)
		receive(test, harness.queued)
	}
	harness.openGate(test)
	for label, channel := range results {
		if result := receive(test, channel); result.err != nil {
			test.Errorf("run %s: %v", label, result.err)
		}
	}
	if diff := cmp.Diff("a\nb\nc\nd\n", harness.read(test, "order")); diff != "" {
		test.Errorf("start order mismatch (-want +got):\n%s", diff)
	}
	if overlap := harness.read(test, "overlap"); overlap != "" {
		test.Errorf("runs overlapped: %q", overlap)
	}
}

func TestQueueCancelledRunLeavesWithoutRunning(test *testing.T) {
	harness := newQueueHarness(test, "p")
	first := runInBackground(context.Background(), harness.runner, "p", "a")
	receive(test, harness.started)

	cancelledContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := runInBackground(cancelledContext, harness.runner, "p", "b")
	receive(test, harness.queued)
	after := runInBackground(context.Background(), harness.runner, "p", "c")
	receive(test, harness.queued)

	cancel()
	// The first run still holds the plugin, so only leaving the queue can
	// end this one.
	if result := receive(test, cancelled); !errors.Is(result.err, context.Canceled) {
		test.Errorf("cancelled run error = %v, want context.Canceled", result.err)
	}

	harness.openGate(test)
	for _, channel := range []<-chan outcome{first, after} {
		if result := receive(test, channel); result.err != nil {
			test.Errorf("run: %v", result.err)
		}
	}
	if diff := cmp.Diff("a\nc\n", harness.read(test, "order")); diff != "" {
		test.Errorf("runs mismatch (-want +got):\n%s", diff)
	}
}

func TestQueueAlreadyCancelledRunDoesNotRun(test *testing.T) {
	harness := newQueueHarness(test, "p")
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	result := receive(test, runInBackground(cancelledContext, harness.runner, "p", "a"))
	if !errors.Is(result.err, context.Canceled) {
		test.Errorf("error = %v, want context.Canceled", result.err)
	}
	if order := harness.read(test, "order"); order != "" {
		test.Errorf("the plugin ran: %q", order)
	}
}

func TestQueueCancellingARunningPlugin(test *testing.T) {
	harness := newQueueHarness(test, "p")
	runContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	running := runInBackground(runContext, harness.runner, "p", "a")
	receive(test, harness.started)
	cancel()
	if result := receive(test, running); !errors.Is(result.err, context.Canceled) {
		test.Errorf("error = %v, want context.Canceled", result.err)
	}
}

func TestQueueDifferentPluginsAreIndependent(test *testing.T) {
	harness := newQueueHarness(test, "slow", "other")
	slow := runInBackground(context.Background(), harness.runner, "slow", "a")
	receive(test, harness.started)

	// "slow" holds its turn at the gate. "other" has its own queue, so it starts
	// at once; the shared lock directory makes it record an overlap here, which
	// is expected.
	other := runInBackground(context.Background(), harness.runner, "other", "b")
	receive(test, harness.started)
	harness.openGate(test)
	for _, channel := range []<-chan outcome{slow, other} {
		if result := receive(test, channel); result.err != nil {
			test.Errorf("run: %v", result.err)
		}
	}
	select {
	case name := <-harness.queued:
		test.Errorf("a run of %q queued behind another plugin", name)
	default:
	}
}

func TestQueueTimeoutStartsWithTheRun(test *testing.T) {
	harness := newQueueHarness(test, "p")
	gate := filepath.Join(harness.directory, "gate")
	gateOpen := make(chan bool, 8)
	harness.runner.withTimeout = func(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
		_, err := os.Stat(gate)
		gateOpen <- err == nil
		return context.WithTimeout(ctx, duration)
	}

	first := runInBackground(context.Background(), harness.runner, "p", "a")
	receive(test, harness.started)
	second := runInBackground(context.Background(), harness.runner, "p", "b")
	receive(test, harness.queued)
	harness.openGate(test)
	for _, channel := range []<-chan outcome{first, second} {
		if result := receive(test, channel); result.err != nil {
			test.Errorf("run: %v", result.err)
		}
	}
	// The first run's timeout started before the gate opened; the second
	// run's only after it, as the second run waited for the first.
	if firstStarted, secondStarted := receive(test, gateOpen), receive(test, gateOpen); firstStarted || !secondStarted {
		test.Errorf("gate open when the timeouts started: first %v, second %v; want false, true", firstStarted, secondStarted)
	}
}
