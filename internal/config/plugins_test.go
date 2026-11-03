package config

import (
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestParsePlugins(test *testing.T) {
	for _, testCase := range []struct {
		name string
		yaml string
		want []Plugin
	}{
		{
			"all keys",
			"plugins:\n  - command: ask\n    on_run: [strig-ask, --fast, '']\n    target: lines\n    timeout: 2m\n",
			[]Plugin{{Command: "ask", OnRun: []string{"strig-ask", "--fast", ""}, Target: TargetLines, Timeout: 2 * time.Minute, Line: 2}},
		},
		{
			"absolute and home programs, relative arguments",
			"plugins:\n  - {command: a, on_run: [/usr/bin/ask, scripts/x, ./y], target: lines}\n  - {command: b, on_run: ['~/bin/ask'], target: lines}\n",
			[]Plugin{
				{Command: "a", OnRun: []string{"/usr/bin/ask", "scripts/x", "./y"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Line: 2},
				{Command: "b", OnRun: []string{"~/bin/ask"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Line: 3},
			},
		},
		{
			"timeout defaults to 60s",
			"plugins:\n  - {command: summarize, on_run: [summarizer], target: pull-request}\n",
			[]Plugin{{Command: "summarize", OnRun: []string{"summarizer"}, Target: TargetPullRequest, Timeout: 60 * time.Second, Line: 2}},
		},
		{
			"show thread",
			"plugins:\n  - {command: ask, on_run: [x], target: lines, show: thread}\n  - {command: plain, on_run: [y], target: lines}\n",
			[]Plugin{
				{Command: "ask", OnRun: []string{"x"}, Target: TargetLines, Show: ShowThread, Timeout: DefaultPluginTimeout, Line: 2},
				{Command: "plain", OnRun: []string{"y"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Line: 3},
			},
		},
		{
			"a service shared by two plugins",
			"plugins:\n  - {command: ask, on_run: [x, -v], target: lines, service: helper}\n  - {command: sum, on_run: [x, -v], target: pull-request, service: helper}\n  - {command: other, on_run: [y], target: lines, service: Other-2}\n",
			[]Plugin{
				{Command: "ask", OnRun: []string{"x", "-v"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Service: "helper", Line: 2},
				{Command: "sum", OnRun: []string{"x", "-v"}, Target: TargetPullRequest, Timeout: DefaultPluginTimeout, Service: "helper", Line: 3},
				{Command: "other", OnRun: []string{"y"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Service: "Other-2", Line: 4},
			},
		},
		{
			"two plugins keep file order",
			"plugins:\n  - {command: b-2, on_run: [x], target: lines}\n  - {command: A1, on_run: [y, '1'], target: lines}\n",
			[]Plugin{
				{Command: "b-2", OnRun: []string{"x"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Line: 2},
				{Command: "A1", OnRun: []string{"y", "1"}, Target: TargetLines, Timeout: DefaultPluginTimeout, Line: 3},
			},
		},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Parse("c.yml", []byte(testCase.yaml))
			if err != nil {
				test.Fatal(err)
			}
			if diff := cmp.Diff(testCase.want, got.Plugins); diff != "" {
				test.Errorf("plugins mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParsePluginsAbsent(test *testing.T) {
	got, err := Parse("c.yml", []byte("theme: x\n"))
	if err != nil {
		test.Fatal(err)
	}
	if got.Plugins != nil {
		test.Errorf("Plugins = %v, want none", got.Plugins)
	}
}

func TestParsePluginErrors(test *testing.T) {
	for _, testCase := range []struct {
		name string
		yaml string
		want string
	}{
		{"empty list", "plugins: []\n", "c.yml:1: plugins must be a non-empty list"},
		{"not a list", "plugins: ask\n", "c.yml:1: plugins must be a non-empty list"},
		{"not a mapping", "plugins:\n  - ask\n", "c.yml:2: a plugin must be a mapping of keys to values"},
		{"missing command", "plugins:\n  - {on_run: [x], target: lines}\n", "c.yml:2: plugin is missing a command"},
		{"missing on_run", "plugins:\n  - {command: ask, target: lines}\n", `c.yml:2: plugin "ask" is missing on_run`},
		{"missing target", "plugins:\n  - {command: ask, on_run: [x]}\n", `c.yml:2: plugin "ask" is missing a target`},
		{"command with a space", "plugins:\n  - {command: 'ask me', on_run: [x], target: lines}\n", `c.yml:2: plugin command "ask me" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"command with a colon", "plugins:\n  - {command: ':ask', on_run: [x], target: lines}\n", `c.yml:2: plugin command ":ask" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"command with an underscore", "plugins:\n  - {command: ask_me, on_run: [x], target: lines}\n", `c.yml:2: plugin command "ask_me" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"command starting with a hyphen", "plugins:\n  - {command: -ask, on_run: [x], target: lines}\n", `c.yml:2: plugin command "-ask" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"command of two hyphens", "plugins:\n  - {command: '--', on_run: [x], target: lines}\n", `c.yml:2: plugin command "--" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"relative program", "plugins:\n  - {command: ask, on_run: [scripts/ask.sh], target: lines}\n", `c.yml:2: on_run program "scripts/ask.sh" must be a bare name found on PATH, an absolute path, or start with ~/`},
		{"dot-relative program", "plugins:\n  - {command: ask, on_run: [./ask], target: lines}\n", `c.yml:2: on_run program "./ask" must be a bare name found on PATH, an absolute path, or start with ~/`},
		{"parent-relative program", "plugins:\n  - {command: ask, on_run: ['../ask'], target: lines}\n", `c.yml:2: on_run program "../ask" must be a bare name found on PATH, an absolute path, or start with ~/`},
		{"home alone is not a program", "plugins:\n  - {command: ask, on_run: ['~/'], target: lines}\n", `c.yml:2: on_run program "~/" must be a bare name found on PATH, an absolute path, or start with ~/`},
		{"empty command", "plugins:\n  - {command: '', on_run: [x], target: lines}\n", "c.yml:2: plugin command must be a non-empty text value"},
		{"duplicate command", "plugins:\n  - {command: ask, on_run: [x], target: lines}\n  - {command: ask, on_run: [y], target: lines}\n", `c.yml:3: duplicate plugin command "ask"`},
		{"on_run is text", "plugins:\n  - {command: ask, on_run: x, target: lines}\n", "c.yml:2: on_run must be a non-empty list"},
		{"on_run is empty", "plugins:\n  - {command: ask, on_run: [], target: lines}\n", "c.yml:2: on_run must be a non-empty list"},
		{"on_run program is empty", "plugins:\n  - {command: ask, on_run: [''], target: lines}\n", "c.yml:2: on_run program must be a non-empty text value"},
		{"on_run program is blank", "plugins:\n  - {command: ask, on_run: ['  ', x], target: lines}\n", "c.yml:2: on_run program must be a non-empty text value"},
		{"on_run argument is a list", "plugins:\n  - {command: ask, on_run: [x, [y]], target: lines}\n", "c.yml:2: every item of on_run must be text"},
		{"on_run argument is null", "plugins:\n  - {command: ask, on_run: [x, ~], target: lines}\n", "c.yml:2: every item of on_run must be text"},
		{"bad target", "plugins:\n  - {command: ask, on_run: [x], target: file}\n", `c.yml:2: target "file" is not one of: lines, pull-request`},
		{"zero timeout", "plugins:\n  - {command: ask, on_run: [x], target: lines, timeout: 0s}\n", "c.yml:2: plugin timeout must be a positive duration such as 10s or 1m30s"},
		{"timeout without unit", "plugins:\n  - {command: ask, on_run: [x], target: lines, timeout: 30}\n", "c.yml:2: plugin timeout must be a positive duration such as 10s or 1m30s"},
		{"negative timeout", "plugins:\n  - {command: ask, on_run: [x], target: lines, timeout: -1m}\n", "c.yml:2: plugin timeout must be a positive duration such as 10s or 1m30s"},
		{"unknown key", "plugins:\n  - {command: ask, on_run: [x], target: lines, display: inline}\n", `c.yml:2: unknown key "display" in a plugin (valid keys: command, on_run, target, show, timeout, service)`},
		{"show value unknown", "plugins:\n  - {command: ask, on_run: [x], target: lines, show: inline}\n", `c.yml:2: show "inline" is not one of: thread`},
		{"show is a list", "plugins:\n  - {command: ask, on_run: [x], target: lines, show: [thread]}\n", "c.yml:2: show must be a non-empty text value"},
		{"show needs lines", "plugins:\n  - {command: ask, on_run: [x], target: pull-request, show: thread}\n", `c.yml:2: plugin "ask": show thread needs target lines`},
		{"show on the second plugin is located there", "plugins:\n  - {command: a, on_run: [x], target: lines, show: thread}\n  - {command: b,\n    on_run: [x],\n    target: pull-request,\n    show: thread}\n", `c.yml:6: plugin "b": show thread needs target lines`},
		{"duplicate key", "plugins:\n  - {command: ask, command: b, on_run: [x], target: lines}\n", `c.yml:2: duplicate key "command" in a plugin`},
		{"service is not a word", "plugins:\n  - {command: ask, on_run: [x], target: lines, service: 'a b'}\n", `c.yml:2: plugin service "a b" must be a plain word (a letter, then letters, digits and hyphens)`},
		{"service is empty", "plugins:\n  - {command: ask, on_run: [x], target: lines, service: ''}\n", "c.yml:2: plugin service must be a non-empty text value"},
		{"one service, two programs", "plugins:\n  - {command: a, on_run: [x], target: lines, service: s}\n  - {command: b, on_run: [y], target: lines}\n  - {command: c, on_run: [x, -v], target: lines, service: s}\n", `c.yml:4: plugin "c": on_run differs from plugin "a" of service "s"; a service is one program`},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Parse("c.yml", []byte(testCase.yaml))
			var configError *Error
			if !errors.As(err, &configError) {
				test.Fatalf("err = %v, want *Error", err)
			}
			if diff := cmp.Diff(testCase.want, err.Error()); diff != "" {
				test.Errorf("message mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
