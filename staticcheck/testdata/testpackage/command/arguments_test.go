package main // want `This test file declares package main, the package under test\. A test of a command must enter through the built command\. Change the package clause to main_test and run the command\.`

import (
	"os"
	"testing"
)

func TestHasProgramName(t *testing.T) {
	if len(os.Args) == 0 {
		t.Fatal("os.Args is empty")
	}
}
