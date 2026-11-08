package widget_test

import "testing"

func TestSelected(t *testing.T) {
	if t.Name() == "" {
		t.Fatal("missing name")
	}
	t.Log("selected")
}
