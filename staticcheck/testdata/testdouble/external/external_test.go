package external_test

import (
	"testing"

	"example.com/testdouble/external"
)

type sinkFake struct{} // want `The type sinkFake is a test double for the interface external\.Sink\.`

func (sinkFake) Write(string) {}

func TestExternal(t *testing.T) {
	external.Send(sinkFake{})
}
