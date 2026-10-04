package main

import "testing"

func TestExitCode(t *testing.T) { // want `Test TestExitCode calls the unexported function exitCode of the package under test\.`
	if exitCode() != 0 {
		t.Fatal("exitCode() != 0")
	}
}
