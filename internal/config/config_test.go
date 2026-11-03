package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/MikePors/strigidae/internal/options"
)

func newSection(name, query string, limit int, sort Sort, order Order) Section {
	return Section{Name: name, Query: query, Limit: limit, Sort: sort, Order: order}
}

func defaultsFor(user string) []Section {
	return []Section{
		newSection("Review requested", "is:pr is:open review-requested:"+user, 30, SortUpdated, OrderDescending),
		newSection("My PRs", "is:pr is:open author:"+user, 30, SortUpdated, OrderDescending),
		newSection("Involved", "is:pr is:open involves:"+user+" -author:"+user, 30, SortUpdated, OrderDescending),
	}
}

func TestParseValid(test *testing.T) {
	tests := []struct {
		name string
		yaml string
		want Config
	}{
		{"empty file is defaults", "", Default()},
		{"comments only is defaults", "# nothing\n", Default()},
		{
			"teammate tab templates the default sections",
			"tabs:\n  - name: Me\n  - name: Alice\n    user: alice\n",
			Config{Tabs: []Tab{
				{Name: "Me", User: "@me", Sections: defaultsFor("@me")},
				{Name: "Alice", User: "alice", Sections: defaultsFor("alice")},
			}},
		},
		{
			"timeouts are zero when absent",
			"tabs:\n  - name: Me\n",
			Config{Tabs: []Tab{{Name: "Me", User: "@me", Sections: defaultsFor("@me")}}},
		},
		{
			"request override alone",
			"timeouts:\n  request: 5s\n",
			func() Config {
				configuration := Default()
				configuration.Timeouts.Request = 5 * time.Second
				return configuration
			}(),
		},
		{
			"load override alone",
			"timeouts:\n  load: 1m30s\n",
			func() Config {
				configuration := Default()
				configuration.Timeouts.Load = 90 * time.Second
				return configuration
			}(),
		},
		{
			"both overrides",
			"timeouts:\n  request: 2s\n  load: 1m\n",
			func() Config {
				configuration := Default()
				configuration.Timeouts = Timeouts{Request: 2 * time.Second, Load: time.Minute}
				return configuration
			}(),
		},
		{
			"is:pr is added when missing, not when present",
			`tabs:
  - name: A
    user: a
    sections:
      - {name: Bug, query: 'label:bug author:{user}'}
      - {name: Has, query: 'author:{user} IS:PR'}
      - {name: Type, query: 'type:pr label:x'}
`,
			Config{Tabs: []Tab{{Name: "A", User: "a", Sections: []Section{
				newSection("Bug", "is:pr label:bug author:a", 30, SortUpdated, OrderDescending),
				newSection("Has", "author:a IS:PR", 30, SortUpdated, OrderDescending),
				newSection("Type", "type:pr label:x", 30, SortUpdated, OrderDescending),
			}}}},
		},
		{
			"numeric scalars are text",
			"tabs:\n  - name: 2024\n    user: 12345\n",
			Config{Tabs: []Tab{{Name: "2024", User: "12345", Sections: defaultsFor("12345")}}},
		},
		{
			"theme alone keeps the default tabs",
			"theme: tokyo-night\n",
			func() Config { configuration := Default(); configuration.Theme = "tokyo-night"; return configuration }(),
		},
		{
			"theme is stored as written",
			"theme: ~/themes/mine.yaml\ntabs:\n  - name: Me\n",
			Config{Theme: "~/themes/mine.yaml", Tabs: []Tab{{Name: "Me", User: "@me", Sections: defaultsFor("@me")}}},
		},
		{
			"wrap on",
			"wrap: true\n",
			func() Config {
				configuration := Default()
				configuration.Options = options.Values{options.Wrap: true}
				return configuration
			}(),
		},
		{
			"wrap off",
			"wrap: false\n",
			func() Config {
				configuration := Default()
				configuration.Options = options.Values{options.Wrap: false}
				return configuration
			}(),
		},
		{
			"names are trimmed",
			"tabs:\n  - name: '  Me  '\n",
			Config{Tabs: []Tab{{Name: "Me", User: "@me", Sections: defaultsFor("@me")}}},
		},
		{
			"custom sections with options and {user}",
			`tabs:
  - name: Bob
    user: bob
    sections:
      - name: Theirs
        query: is:pr author:{user} -author:{user}-bot
        limit: 5
        sort: comments
        order: asc
      - name: Plain
        query: is:pr label:bug
`,
			Config{Tabs: []Tab{{Name: "Bob", User: "bob", Sections: []Section{
				newSection("Theirs", "is:pr author:bob -author:bob-bot", 5, SortComments, OrderAscending),
				newSection("Plain", "is:pr label:bug", 30, SortUpdated, OrderDescending),
			}}}},
		},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Parse("c.yml", []byte(testCase.yaml))
			if err != nil {
				test.Fatalf("Parse: %v", err)
			}
			if diff := cmp.Diff(testCase.want, got); diff != "" {
				test.Errorf("config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseErrors(test *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"malformed YAML", "tabs:\n  - name: Me\n   user: x\n", "c.yml:1: invalid YAML: did not find expected '-' indicator"},
		// yaml.v3 reports the line of the construct it was parsing, so for the
		// unclosed flow list on line 3 it says line 2, the enclosing block. The line is approximate.
		{"syntax error after line 3", "tabs:\n  - name: A\n    sections: [\n      {name: S}\n    other: 1\n", "c.yml:2: invalid YAML: did not find expected ',' or ']'"},
		{"multiple documents", "tabs:\n  - name: A\n---\ntabz: 1\n", "c.yml:3: the config file must contain a single YAML document"},
		{"empty first document", "---\n---\ntabs: []\n", "c.yml:2: the config file must contain a single YAML document"},
		{"anchor", "tabs:\n  - &t {name: A}\n", "c.yml:2: YAML aliases and merge keys are not supported"},
		{"alias", "tabs:\n  - &t {name: A}\n  - *t\n", "c.yml:2: YAML aliases and merge keys are not supported"},
		{"merge key", "tabs:\n  - <<: {name: A}\n", "c.yml:2: YAML aliases and merge keys are not supported"},
		{"quoted limit", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q, limit: '5'}\n", "c.yml:4: limit must be a whole number from 1 to 100"},
		{"null name", "tabs:\n  - name:\n", "c.yml:2: tab name must be a non-empty text value"},
		{"too large", "# " + strings.Repeat("x", MaximumFileSize) + "\n", "c.yml: config file is too large (limit 1048576 bytes)"},
		{"duplicate section", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q}\n      - {name: ' S ', query: r}\n", `c.yml:5: duplicate section name "S" in this tab`},
		{"duplicate tab after trimming", "tabs:\n  - name: A\n  - name: ' A'\n", `c.yml:3: duplicate tab name "A"`},
		{"sort in query", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: 'is:pr Sort:created-asc'}\n", "c.yml:4: query must not contain sort:; use the sort and order keys"},
		{"unclosed bracket", "tabs: [\n", "c.yml:1: invalid YAML: did not find expected node content"},
		{"not a mapping", "- a\n", "c.yml:1: the config file must be a mapping of keys to values"},
		{"empty theme", "theme:\n", "c.yml:1: theme must be a non-empty text value"},
		{"wrap as text", "wrap: 'true'\n", "c.yml:1: wrap must be true or false"},
		{"wrap as yes", "wrap: yes\n", "c.yml:1: wrap must be true or false"},
		{"empty wrap", "wrap:\n", "c.yml:1: wrap must be true or false"},
		{"unknown top-level key", "tabz: []\n", `c.yml:1: unknown key "tabz" in the config file (valid keys: autoviewed, hideresolved, hidereviewed, hideviewed, hlsearch, plugins, relativenumber, skipresolved, tabs, theme, timeouts, wrap, wrapscan)`},
		{"zero request", "timeouts:\n  request: 0s\n", "c.yml:2: request timeout must be a positive duration such as 10s or 1m30s"},
		{"bare zero request", "timeouts:\n  request: 0\n", "c.yml:2: request timeout must be a positive duration such as 10s or 1m30s"},
		{"negative load", "timeouts:\n  load: -5s\n", "c.yml:2: load timeout must be a positive duration such as 10s or 1m30s"},
		{"unparseable load", "timeouts:\n  load: soon\n", "c.yml:2: load timeout must be a positive duration such as 10s or 1m30s"},
		{"empty request", "timeouts:\n  request:\n", "c.yml:2: request timeout must be a positive duration such as 10s or 1m30s"},
		{"unknown timeouts key", "timeouts:\n  read: 5s\n", `c.yml:2: unknown key "read" in timeouts (valid keys: request, load)`},
		{"timeouts not a mapping", "timeouts: 5s\n", "c.yml:1: timeouts must be a mapping of keys to values"},
		{"unknown tab key", "tabs:\n  - name: Me\n    usr: a\n", `c.yml:3: unknown key "usr" in a tab (valid keys: name, user, sections)`},
		{"unknown section key", "tabs:\n  - name: Me\n    sections:\n      - name: A\n        query: q\n        limt: 3\n", `c.yml:6: unknown key "limt" in a section (valid keys: name, query, limit, sort, order)`},
		{"duplicate key", "tabs:\n  - name: A\ntabs: []\n", `c.yml:3: duplicate key "tabs" in the config file`},
		{"empty tabs", "tabs: []\n", "c.yml:1: tabs must be a non-empty list"},
		{"tab without name", "tabs:\n  - user: a\n", "c.yml:2: tab is missing a name"},
		{"duplicate tab", "tabs:\n  - name: A\n  - name: A\n", `c.yml:3: duplicate tab name "A"`},
		{"bad user", "tabs:\n  - name: A\n    user: not a login\n", `c.yml:3: user "not a login" is not a GitHub login (letters, digits and hyphens) or "@me"`},
		{"empty sections", "tabs:\n  - name: A\n    sections: []\n", "c.yml:3: sections must be a non-empty list"},
		{"section without query", "tabs:\n  - name: A\n    sections:\n      - name: S\n", `c.yml:4: section "S" is missing a query`},
		{"limit too big", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q, limit: 500}\n", "c.yml:4: limit must be a whole number from 1 to 100"},
		{"limit not a number", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q, limit: lots}\n", "c.yml:4: limit must be a whole number from 1 to 100"},
		{"bad sort", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q, sort: best}\n", `c.yml:4: sort "best" is not one of: updated, created, comments, reactions, interactions`},
		{"bad order", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: q, order: up}\n", `c.yml:4: order "up" is not one of: asc, desc`},
		{"unknown variable", "tabs:\n  - name: A\n    sections:\n      - {name: S, query: 'author:{usr}'}\n", "c.yml:4: unknown variable {usr} in query (the only variable is {user})"},
	}
	for _, testCase := range tests {
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

func TestLoad(test *testing.T) {
	directory := test.TempDir()
	good := filepath.Join(directory, "good.yml")
	if err := os.WriteFile(good, []byte("tabs:\n  - name: Only\n    user: zed\n"), 0o600); err != nil {
		test.Fatal(err)
	}
	missing := filepath.Join(directory, "missing.yml")

	test.Run("valid file", func(test *testing.T) {
		got, err := Load(good, true)
		if err != nil {
			test.Fatal(err)
		}
		want := Config{Tabs: []Tab{{Name: "Only", User: "zed", Sections: defaultsFor("zed")}}}
		if diff := cmp.Diff(want, got); diff != "" {
			test.Errorf("(-want +got):\n%s", diff)
		}
	})
	test.Run("missing file gives defaults", func(test *testing.T) {
		got, err := Load(missing, false)
		if err != nil {
			test.Fatal(err)
		}
		if diff := cmp.Diff(Default(), got); diff != "" {
			test.Errorf("(-want +got):\n%s", diff)
		}
	})
	test.Run("missing explicit file is an error", func(test *testing.T) {
		if _, err := Load(missing, true); !errors.Is(err, os.ErrNotExist) {
			test.Errorf("err = %v, want ErrNotExist", err)
		}
	})
}

func TestExampleParses(test *testing.T) {
	got, err := Load(filepath.Join("..", "..", "docs", "config.example.yml"), true)
	if err != nil {
		test.Fatal(err)
	}
	if len(got.Tabs) != 3 || got.Tabs[1].User != "alice" || len(got.Tabs[2].Sections) != 2 {
		test.Errorf("unexpected example config: %+v", got)
	}
}

func TestPath(test *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	tests := []struct {
		name        string
		environment string
		want        string
	}{
		{"xdg", "/x/cfg", "/x/cfg/strig/config.yml"},
		{"unset", "", "/home/u/.config/strig/config.yml"},
		{"relative xdg ignored", "rel", "/home/u/.config/strig/config.yml"},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			got, err := Path(func(string) string { return testCase.environment }, home)
			if err != nil {
				test.Fatal(err)
			}
			if got != testCase.want {
				test.Errorf("Path = %q, want %q", got, testCase.want)
			}
		})
	}
}
