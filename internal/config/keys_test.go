package config

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseKeys(test *testing.T) {
	got, err := Parse("c.yml", []byte("keys:\n  - {key: 'g  a', command: ask why}\n  - {key: ctrl+a, command: ':checks'}\n  - {key: '0', command: x}\n  - {key: 1x, command: y}\n"))
	if err != nil {
		test.Fatal(err)
	}
	want := []KeyMapping{
		{Key: "g a", Command: "ask why", Line: 2},
		{Key: "ctrl+a", Command: "checks", Line: 3},
		{Key: "0", Command: "x", Line: 4},
		{Key: "1x", Command: "y", Line: 5},
	}
	if diff := cmp.Diff(want, got.Keys); diff != "" {
		test.Errorf("keys (-want +got):\n%s", diff)
	}
}

func TestParseKeysErrors(test *testing.T) {
	for _, testCase := range []struct{ name, yaml, want string }{
		{"empty list", "keys: []\n", "c.yml:1: keys must be a non-empty list"},
		{"not a mapping", "keys:\n  - g a\n", "c.yml:2: a key mapping must be a mapping of keys to values"},
		{"missing key", "keys:\n  - {command: ask}\n", "c.yml:2: key mapping is missing a key"},
		{"missing command", "keys:\n  - {key: g a}\n", `c.yml:2: key mapping "g a" is missing a command`},
		{"empty key", "keys:\n  - {key: '', command: ask}\n", "c.yml:2: key must be a non-empty text value"},
		{"command is only a colon", "keys:\n  - {key: g a, command: ':'}\n", "c.yml:2: command must be a non-empty text value"},
		{"a count", "keys:\n  - {key: '5', command: ask}\n", `c.yml:2: key "5" is a count; map another key`},
		{"a count then a key", "keys:\n  - {key: 2 j, command: ask}\n", `c.yml:2: key "2 j" is a count; map another key`},
		{"duplicate", "keys:\n  - {key: g a, command: ask}\n  - {key: 'g  a', command: b}\n", `c.yml:3: duplicate key "g a"`},
		{"unknown field", "keys:\n  - {key: g a, command: ask, view: list}\n", `c.yml:2: unknown key "view" in a key mapping (valid keys: key, command)`},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Parse("c.yml", []byte(testCase.yaml))
			var configError *Error
			if !errors.As(err, &configError) {
				test.Fatalf("err = %v, want *Error", err)
			}
			if diff := cmp.Diff(testCase.want, err.Error()); diff != "" {
				test.Errorf("message (-want +got):\n%s", diff)
			}
		})
	}
}
