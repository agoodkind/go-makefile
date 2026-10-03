package directive

import "testing"

// want +1 `The //testdouble:external directive needs a reason of at least three words that states which service outside the host the type fakes\.`
//testdouble:external
type missingReasonFake struct{} // want `The type missingReasonFake is a test double for the interface directive\.Sink\.`

func (missingReasonFake) Write(string) {}

// want +1 `The //testdouble:external directive needs a reason`
//testdouble:external two words
type shortReasonFake struct{} // want `The type shortReasonFake is a test double for the interface directive\.Sink\.`

func (shortReasonFake) Write(string) {}

//testdouble:external fake payment gateway
type validFake struct{}

func (validFake) Write(string) {}

func TestDirective(t *testing.T) {
	Send(missingReasonFake{})
	Send(shortReasonFake{})
	Send(validFake{})
}
