package exported_test

import (
	"testing"

	"example.com/testpackage/exported"
)

func TestDoubleWithFactor(t *testing.T) {
	exported.SetFactor(3)
	if exported.Double(2) != 6 {
		t.Fatal("Double(2) != 6")
	}
}
