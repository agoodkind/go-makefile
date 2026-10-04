package flagged

import "testing"

type argumentFake struct{} // want `The type argumentFake is a test double for the interface flagged\.Sink\. A test must run the production implementation with real dependencies\. Use the production type, or add //testdouble:external <reason> above the type when it fakes a service outside the host\.`

func (argumentFake) Write(string) {}

type variableFake struct{} // want `The type variableFake is a test double for the interface flagged\.Sink\.`

func (variableFake) Write(string) {}

type fieldFake struct{} // want `The type fieldFake is a test double for the interface flagged\.Sink\.`

func (fieldFake) Write(string) {}

type resultFake struct{} // want `The type resultFake is a test double for the interface flagged\.Sink\.`

func (resultFake) Write(string) {}

type pointerFake struct{ calls int } // want `The type pointerFake is a test double for the interface flagged\.Sink\.`

func (fake *pointerFake) Write(string) { fake.calls++ }

func newResult() Sink {
	return resultFake{}
}

func TestFlagged(t *testing.T) {
	Send(argumentFake{})
	Send(argumentFake{})

	var sink Sink = variableFake{}
	sink.Write("message")

	holder := Holder{Sink: fieldFake{}}
	holder.Sink.Write("message")

	newResult().Write("message")

	Send(&pointerFake{})
}
