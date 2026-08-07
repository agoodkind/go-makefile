package main

import "testing"

func TestGolangciCacheUsable(t *testing.T) {
	testCases := []struct {
		name         string
		passed       bool
		status       int
		findingCount int
		dropped      int
		want         bool
	}{
		{name: "clean run", passed: true, want: true},
		{name: "genuine findings", status: 1, findingCount: 2, want: true},
		{name: "dropped findings", passed: true, status: 1, dropped: 1, want: true},
		{name: "tool failure", passed: true, status: 2, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := golangciCacheUsable(
				testCase.passed,
				testCase.status,
				testCase.findingCount,
				testCase.dropped,
			)
			if got != testCase.want {
				t.Fatalf("golangciCacheUsable() = %t, want %t", got, testCase.want)
			}
		})
	}
}
