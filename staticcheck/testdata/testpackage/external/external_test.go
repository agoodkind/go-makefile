package external_test

import (
	"testing"

	"example.com/testpackage/external"
)

func TestDouble(t *testing.T) {
	if external.Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}
