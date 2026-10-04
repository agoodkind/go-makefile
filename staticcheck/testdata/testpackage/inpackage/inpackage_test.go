package inpackage

import "testing"

func TestDouble(t *testing.T) { // want `Test TestDouble calls the unexported function double of the package under test\. A test must enter through the exported API or the built command\. Call the exported entry point that uses double\.`
	if double(2) != 4 {
		t.Fatal("double(2) != 4")
	}
}

func TestExportedDouble(t *testing.T) {
	if Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}
