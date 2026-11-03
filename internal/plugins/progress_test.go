//go:build unix

package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/config"
)

// progressRun runs name in the background with a progress that passes on what
// it is given.
func progressRun(runner *Runner, name, arguments string) (<-chan Effect, <-chan outcome) {
	early := make(chan Effect, 8)
	results := make(chan outcome, 1)
	go func() {
		effects, err := runner.RunWithProgress(context.Background(), name, Request{Command: name, Arguments: arguments}, func(effect Effect) { early <- effect })
		results <- outcome{effects, err}
	}()
	return early, results
}

// The effects reach progress while the plugin still runs, in order; the run's
// result is what Run would return.
func TestProgressGetsEffectsAsTheyArePrinted(test *testing.T) {
	for _, testCase := range []struct {
		name      string
		plugin    func() config.Plugin
		arguments string
	}{
		{"one-off run", func() config.Plugin { return scriptPlugin("p", "progress.sh", guard) }, ""},
		{"service", func() config.Plugin {
			plugin := scriptPlugin("p", "service.sh", guard)
			plugin.Service = "helper"
			return plugin
		}, "progress"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			directory := test.TempDir()
			runner := New([]config.Plugin{testCase.plugin()}, Options{Environment: baseEnvironment(directory), WaitDelay: 100 * time.Millisecond})
			test.Cleanup(runner.Stop)
			early, results := progressRun(runner, "p", testCase.arguments)
			if got := receive(test, early); got != (Effect{Name: EffectNotice, Text: "working"}) {
				test.Errorf("first effect = %+v", got)
			}
			if err := os.WriteFile(filepath.Join(directory, "gate"), nil, 0o600); err != nil {
				test.Fatal(err)
			}
			result := receive(test, results)
			want := []Effect{{Name: EffectNotice, Text: "working"}, {Name: EffectAnswer, Text: "done"}}
			if result.err != nil || !cmp.Equal(result.effects, want) {
				test.Errorf("result = %+v, %v", result.effects, result.err)
			}
			if got := receive(test, early); got != want[1] {
				test.Errorf("second effect = %+v", got)
			}
			select {
			case extra := <-early:
				test.Errorf("progress got %+v besides the known effects", extra)
			default:
			}
		})
	}
}
