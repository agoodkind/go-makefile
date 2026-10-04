package seams_test

import (
	"testing"

	"example.com/testseam/seams"
)

func TestExternalReplacesField(t *testing.T) {
	config := seams.Default()
	config.Run = func() int { return 9 } // want `TestExternalReplacesField replaces the function field Run\.`
	if config.Run() != 9 {
		t.Fatal("Run")
	}
}

func TestExternalReplacesVariable(t *testing.T) {
	seams.Send = func(string) error { return nil } // want `TestExternalReplacesVariable replaces the package variable Send\.`
	if seams.Send("x") != nil {
		t.Fatal("Send failed")
	}
}

func TestExternalUsesProduction(t *testing.T) {
	if seams.Result() != 1 {
		t.Fatal("Result")
	}
}
