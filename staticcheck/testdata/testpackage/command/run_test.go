package main_test

import (
	"os/exec"
	"testing"
)

func TestCommandExitsZero(t *testing.T) {
	if err := exec.Command("go", "run", ".").Run(); err != nil {
		t.Fatalf("go run . failed: %v", err)
	}
}
