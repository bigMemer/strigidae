package keys

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/key"
)

func TestGlobalActionNames(test *testing.T) {
	global := DefaultGlobal()
	if err := CheckActions(global.Actions()); err != nil {
		test.Error(err)
	}
}

func TestCheckActions(test *testing.T) {
	binding := key.NewBinding()
	tests := []struct {
		name  string
		names []string
		valid bool
	}{
		{"words joined by hyphens", []string{"down", "half-page-down"}, true},
		{"empty", []string{""}, false},
		{"upper case", []string{"Down"}, false},
		{"trailing hyphen", []string{"down-"}, false},
		{"used twice", []string{"down", "up", "down"}, false},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			var actions []Action
			for _, name := range testCase.names {
				actions = append(actions, Action{Name: name, Binding: &binding})
			}
			if err := CheckActions(actions); (err == nil) != testCase.valid {
				test.Errorf("CheckActions(%q) = %v, want valid %v", testCase.names, err, testCase.valid)
			}
		})
	}
}

func TestCheckActionsCommands(test *testing.T) {
	tests := []struct {
		name    string
		actions []Action
		valid   bool
	}{
		{"command named like its action", []Action{{Name: "merge", Command: "merge"}}, true},
		{"command with another name", []Action{{Name: "resolve-thread", Command: "resolve"}}, true},
		{"no command", []Action{{Name: "down"}}, true},
		{"two actions share a command", []Action{{Name: "resolve", Command: "resolve"}, {Name: "resolve-thread", Command: "resolve"}}, false},
		{"bad command name", []Action{{Name: "merge", Command: "Merge"}}, false},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			if err := CheckActions(testCase.actions); (err == nil) != testCase.valid {
				test.Errorf("CheckActions = %v, want valid %v", err, testCase.valid)
			}
		})
	}
}

// Every binding field of Global except Count is named exactly once, so a
// binding added later can't be left without a name.
func TestGlobalActionsCoverEveryBinding(test *testing.T) {
	global := DefaultGlobal()
	named := map[uintptr]int{} // by address, so unexported fields need no Interface
	for _, action := range global.Actions() {
		named[reflect.ValueOf(action.Binding).Pointer()]++
	}
	value := reflect.ValueOf(&global).Elem()
	bindingType := reflect.TypeFor[key.Binding]()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Type != bindingType || field.Name == "Count" || field.Name == "Complete" {
			continue
		}
		binding := value.Field(i).Addr().Pointer()
		if named[binding] != 1 {
			test.Errorf("Global.%s is named %d times, want once", field.Name, named[binding])
		}
		delete(named, binding)
	}
	if len(named) != 0 {
		test.Errorf("%d actions point outside Global's binding fields", len(named))
	}
}

func TestGlobalActionChangesItsBinding(test *testing.T) {
	global := DefaultGlobal()
	for _, action := range global.Actions() {
		if action.Name == "down" {
			action.Binding.SetKeys("ctrl+n")
		}
	}
	if keys := global.Down.Keys(); len(keys) != 1 || keys[0] != "ctrl+n" {
		test.Errorf("Down keys = %v, want [ctrl+n]", keys)
	}
}
