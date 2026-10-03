package main // want `This test file declares package main, the package under test\. A test of a command must enter through the built command\. Change the package clause to main_test and run the command\.`

import "testing"

func TestExitCode(t *testing.T) {
	if exitCode() != 0 {
		t.Fatal("exitCode() != 0")
	}
}
