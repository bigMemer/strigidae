package userinterface

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsWriteTimeout(test *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"a write that timed out", WriteError(context.DeadlineExceeded), true},
		{"the same, wrapped", fmt.Errorf("start review: %w", WriteError(context.DeadlineExceeded)), true},
		{"a read that timed out", fmt.Errorf("check review state: %w", context.DeadlineExceeded), false},
		{"another write failure", WriteError(errors.New("boom")), false},
	}
	for _, testCase := range tests {
		test.Run(testCase.name, func(test *testing.T) {
			if got := IsWriteTimeout(testCase.err); got != testCase.want {
				test.Errorf("IsWriteTimeout(%v) = %v, want %v", testCase.err, got, testCase.want)
			}
		})
	}
}
