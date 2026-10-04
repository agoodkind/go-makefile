package seams

import (
	"testing"
	"time"
)

func TestReplacesVariable(t *testing.T) {
	previous := Send
	t.Cleanup(func() { Send = previous })
	Send = func(string) error { return nil } // want `TestReplacesVariable replaces the package variable Send\.`
	if Send("x") != nil {
		t.Fatal("Send failed")
	}
}

func TestReplacesFieldInLiteral(t *testing.T) {
	deps := dependencies{run: func() int { return 7 }} // want `TestReplacesFieldInLiteral replaces the function field run\. Production code sets that field to one value\.`
	if deps.run() != 7 {
		t.Fatal("run")
	}
}

func TestReplacesFieldByAssignment(t *testing.T) {
	deps := newDependencies()
	deps.run = otherRun // want `TestReplacesFieldByAssignment replaces the function field run\.`
	if deps.run() != 2 {
		t.Fatal("run")
	}
}

func TestKeepsProductionValue(t *testing.T) {
	deps := dependencies{run: realRun}
	if deps.run() != 1 {
		t.Fatal("run")
	}
}

func TestExternalServiceDirective(t *testing.T) {
	//testseam:external fakes the mail relay
	Send = func(string) error { return nil }
	if Send("x") != nil {
		t.Fatal("Send failed")
	}
}

func TestShortDirective(t *testing.T) {
	//testseam:external too short // want `The //testseam:external directive needs a reason of at least three words`
	Send = func(string) error { return nil } // want `TestShortDirective replaces the package variable Send\.`
	if Send("x") != nil {
		t.Fatal("Send failed")
	}
}

func TestClockReplacement(t *testing.T) {
	fixed := time.Unix(1000, 0)
	Now = func() time.Time { return fixed }                           // want `TestClockReplacement replaces the package variable Now\.`
	Wait = func(time.Duration) {}                                     // want `TestClockReplacement replaces the package variable Wait\.`
	deps := clockDependencies{now: func() time.Time { return fixed }} // want `TestClockReplacement replaces the function field now\.`
	if !deps.now().Equal(Now()) {
		t.Fatal("clock")
	}
}

func TestCallbackAndOptionAreNotSeams(t *testing.T) {
	callback := Callback{Done: func() int { return 3 }}
	options := Options{Now: func() int { return 4 }}
	if Use(options, callback, Default()) != 8 {
		t.Fatal("Use")
	}
}
