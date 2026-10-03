package clean

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// readerFake implements only a standard library interface.
type readerFake struct{ reader io.Reader }

func (fake readerFake) Read(buffer []byte) (int, error) { return fake.reader.Read(buffer) }

// unusedFake is never converted to an interface.
type unusedFake struct{}

func (unusedFake) Write(string) {}

// recorder is declared in a test file.
type recorder interface {
	Record()
}

type recorderFake struct{}

func (recorderFake) Record() {}

func record(target recorder) { target.Record() }

// externalFake is a fake for a billing service outside the host.
//
//testdouble:external fake billing service
type externalFake struct{}

func (externalFake) Write(string) {}

//testdouble:external fake billing service
type unconvertedExternalFake struct{}

type stringerFake struct{}

func (stringerFake) String() string { return "fake" }

func TestClean(t *testing.T) {
	_, err := io.ReadAll(readerFake{reader: strings.NewReader("data")})
	if err != nil {
		t.Fatal(err)
	}

	var concrete unusedFake = unusedFake{}
	concrete.Write("message")

	record(recorderFake{})
	Send(externalFake{})
	Send(Production{})
	_ = unconvertedExternalFake{}

	_ = fmt.Sprint(stringerFake{})
}
