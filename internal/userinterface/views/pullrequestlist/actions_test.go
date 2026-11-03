package pullrequestlist

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/key"

	"github.com/MikePors/strigidae/internal/github"
	"github.com/MikePors/strigidae/internal/userinterface/keys"
	"github.com/MikePors/strigidae/internal/userinterface/theme"
)

func TestActionNames(test *testing.T) {
	global := keys.DefaultGlobal()
	view := New(nil, github.Repository{}, nil, theme.Default(), Options{})
	if err := keys.CheckActions(append(global.Actions(), view.bindings.actions()...)); err != nil {
		test.Error(err)
	}
}

// Every binding of the list is named exactly once, so one added later
// can't be left without a name.
func TestActionsCoverEveryBinding(test *testing.T) {
	view := New(nil, github.Repository{}, nil, theme.Default(), Options{})
	named := map[uintptr]int{}
	for _, action := range view.bindings.actions() {
		if action.Binding == nil {
			continue // a command with no key
		}
		named[reflect.ValueOf(action.Binding).Pointer()]++
	}
	value := reflect.ValueOf(&view.bindings).Elem()
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.Type != reflect.TypeFor[key.Binding]() {
			continue
		}
		binding := value.Field(i).Addr().Pointer()
		if named[binding] != 1 {
			test.Errorf("bindings.%s is named %d times, want once", field.Name, named[binding])
		}
		delete(named, binding)
	}
	if len(named) != 0 {
		test.Errorf("%d actions point outside the list bindings", len(named))
	}
}
