package inpackage // want `This test file declares package inpackage, the package under test\. A test must enter through the exported API\. Change the package clause to inpackage_test and call only exported identifiers\.`

import "testing"

func TestDoubleOfThree(t *testing.T) {
	if Double(3) != 6 {
		t.Fatal("Double(3) != 6")
	}
}
