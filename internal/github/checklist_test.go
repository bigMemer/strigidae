package github

import "testing"

func TestCheckResultGroups(test *testing.T) {
	tests := []struct {
		result CheckResult
		want   CheckGroup
	}{
		{CheckFailed, CheckGroupFailing},
		{CheckTimedOut, CheckGroupFailing},
		{CheckActionRequired, CheckGroupFailing},
		{CheckRunning, CheckGroupPending},
		{CheckPassed, CheckGroupPassing},
		{CheckCancelled, CheckGroupInactive},
		{CheckSkipped, CheckGroupInactive},
		{CheckNeutral, CheckGroupInactive},
	}
	for _, testCase := range tests {
		if got := testCase.result.Group(); got != testCase.want {
			test.Errorf("%s group = %d, want %d", testCase.result, got, testCase.want)
		}
	}
}
