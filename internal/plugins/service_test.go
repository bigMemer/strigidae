//go:build unix

package plugins

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
)

// lockedBuffer is a log the service's goroutines and the test share.
type lockedBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(data []byte) (int, error) {
	locked.mutex.Lock()
	defer locked.mutex.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedBuffer) String() string {
	locked.mutex.Lock()
	defer locked.mutex.Unlock()
	return locked.buffer.String()
}

// serviceHarness is a runner with plugins ask and summarize served by
// testdata/service.sh.
type serviceHarness struct {
	runner    *Runner
	directory string
	log       *lockedBuffer
	sent      chan string
}

func newServiceHarness(test *testing.T, timeout time.Duration) *serviceHarness {
	test.Helper()
	harness := &serviceHarness{directory: test.TempDir(), log: &lockedBuffer{}, sent: make(chan string, 8)}
	ask, summarize := scriptPlugin("ask", "service.sh", timeout), scriptPlugin("summarize", "service.sh", time.Minute)
	ask.Service, summarize.Service = "helper", "helper"
	harness.runner = New([]config.Plugin{ask, summarize}, Options{
		Environment: baseEnvironment(harness.directory),
		Logger:      slog.New(slog.NewTextHandler(harness.log, nil)),
		WaitDelay:   100 * time.Millisecond,
	})
	harness.runner.sent = func(name string) { harness.sent <- name }
	test.Cleanup(harness.runner.Stop)
	return harness
}

func (harness *serviceHarness) run(ctx context.Context, name, arguments string) outcome {
	effects, err := harness.runner.Run(ctx, name, Request{Command: name, Arguments: arguments})
	return outcome{effects, err}
}

// hold runs ask with "hold" in the background: the service answers it only
// at the next "release".
func (harness *serviceHarness) hold(ctx context.Context) <-chan outcome {
	results := make(chan outcome, 1)
	go func() { results <- harness.run(ctx, "ask", "hold") }()
	return results
}

// starts are the process identifiers of the service's starts, in order.
func (harness *serviceHarness) starts(test *testing.T) []int {
	test.Helper()
	data, err := os.ReadFile(filepath.Join(harness.directory, "starts"))
	if err != nil {
		test.Fatal(err)
	}
	var identifiers []int
	for _, field := range strings.Fields(string(data)) {
		identifier, err := strconv.Atoi(field)
		if err != nil {
			test.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	return identifiers
}

func answered(text string) []Effect { return []Effect{{Name: EffectAnswer, Text: text}} }

func TestServiceServesRunsAtOnceFromOneProcess(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	held := harness.hold(context.Background())
	receive(test, harness.sent)
	released := harness.run(context.Background(), "summarize", "release")
	if released.err != nil || !cmp.Equal(released.effects, answered("released")) {
		test.Errorf("second run = %v, %v", released.effects, released.err)
	}
	if first := receive(test, held); first.err != nil || !cmp.Equal(first.effects, answered("held")) {
		test.Errorf("held run = %v, %v", first.effects, first.err)
	}
	again := harness.run(context.Background(), "ask", "why")
	if again.err != nil || !cmp.Equal(again.effects, answered("ask:why")) {
		test.Errorf("third run = %v, %v", again.effects, again.err)
	}
	if starts := harness.starts(test); len(starts) != 1 {
		test.Errorf("the service started %d times, want once", len(starts))
	}
}

func TestServiceStartsAgainAfterItExits(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	held := harness.hold(context.Background())
	receive(test, harness.sent)
	if exited := harness.run(context.Background(), "summarize", "exit"); !errors.Is(exited.err, ErrExited) {
		test.Errorf("run that ended the service: %v", exited.err)
	}
	if waiting := receive(test, held); !errors.Is(waiting.err, ErrExited) || waiting.err.Error() != `plugin "ask": exited` {
		test.Errorf("run under way when it ended: %v", waiting.err)
	}
	if next := harness.run(context.Background(), "ask", "next"); next.err != nil || !cmp.Equal(next.effects, answered("ask:next")) {
		test.Errorf("next run = %v, %v", next.effects, next.err)
	}
	if starts := harness.starts(test); len(starts) != 2 {
		test.Errorf("the service started %d times, want twice", len(starts))
	}
}

func TestServiceTimeoutDropsTheLateAnswer(test *testing.T) {
	harness := newServiceHarness(test, 50*time.Millisecond)
	late := harness.run(context.Background(), "ask", "hold")
	if !errors.Is(late.err, ErrTimeout) {
		test.Fatalf("held run: %v, want a timeout", late.err)
	}
	if released := harness.run(context.Background(), "summarize", "release"); released.err != nil || !cmp.Equal(released.effects, answered("released")) {
		test.Errorf("next run = %v, %v", released.effects, released.err)
	}
	if !strings.Contains(harness.log.String(), "effect for no run under way") {
		test.Errorf("the late answer was not logged as dropped:\n%s", harness.log)
	}
	if starts := harness.starts(test); len(starts) != 1 {
		test.Errorf("the timeout restarted the service: %d starts", len(starts))
	}
}

func TestServiceCancelLeavesItRunning(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	held := harness.hold(ctx)
	receive(test, harness.sent)
	cancel()
	if result := receive(test, held); !errors.Is(result.err, context.Canceled) {
		test.Errorf("cancelled run: %v", result.err)
	}
	if next := harness.run(context.Background(), "ask", "next"); next.err != nil {
		test.Errorf("next run: %v", next.err)
	}
	if starts := harness.starts(test); len(starts) != 1 {
		test.Errorf("%d starts, want 1", len(starts))
	}
}

func TestServiceBadOutput(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	if result := harness.run(context.Background(), "ask", "bad"); result.err != nil || !cmp.Equal(result.effects, answered("ok")) {
		test.Errorf("lines naming no run failed the run: %v, %v", result.effects, result.err)
	}
	if got := strings.Count(harness.log.String(), "not an effect of a run"); got != 2 {
		test.Errorf("%d lines logged as skipped, want 2:\n%s", got, harness.log)
	}
	if result := harness.run(context.Background(), "ask", "bad-effect"); !errors.Is(result.err, ErrInvalidOutput) {
		test.Errorf("bad effect of a run: %v", result.err)
	}
	if result := harness.run(context.Background(), "ask", "after"); result.err != nil {
		test.Errorf("the service did not survive bad output: %v", result.err)
	}
	if starts := harness.starts(test); len(starts) != 1 {
		test.Errorf("%d starts, want 1", len(starts))
	}
}

func TestServiceLineOverTheLimit(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	if result := harness.run(context.Background(), "ask", "long"); !errors.Is(result.err, ErrOutputTooLarge) {
		test.Errorf("long line: %v", result.err)
	}
	if result := harness.run(context.Background(), "ask", "after"); result.err != nil {
		test.Errorf("run after the kill: %v", result.err)
	}
	if starts := harness.starts(test); len(starts) != 2 {
		test.Errorf("%d starts, want 2", len(starts))
	}
}

func TestStopKillsTheService(test *testing.T) {
	harness := newServiceHarness(test, time.Minute)
	if result := harness.run(context.Background(), "ask", "ready"); result.err != nil { // it has written its start
		test.Fatal(result.err)
	}
	receive(test, harness.sent)
	held := harness.hold(context.Background())
	receive(test, harness.sent)
	harness.runner.Stop()
	if result := receive(test, held); !errors.Is(result.err, ErrExited) {
		test.Errorf("run under way at Stop: %v", result.err)
	}
	identifier := harness.starts(test)[0]
	if err := syscall.Kill(identifier, 0); !errors.Is(err, syscall.ESRCH) {
		test.Errorf("the service process %d is still there: %v", identifier, err)
	}
	if result := harness.run(context.Background(), "ask", "after"); result.err == nil {
		test.Error("a run after Stop started the service again")
	}
}
