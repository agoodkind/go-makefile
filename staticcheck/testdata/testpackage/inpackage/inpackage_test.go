package inpackage // want `This test file declares package inpackage, the package under test\. A test must enter through the exported API\. Change the package clause to inpackage_test and call only exported identifiers\.`

import "testing"

func TestDouble(t *testing.T) {
	if double(2) != 4 {
		t.Fatal("double(2) != 4")
	}
}
