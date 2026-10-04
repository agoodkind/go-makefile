package cases_test

import (
	"os"
	"sync"
	"testing"

	"example.com/testassert/cases"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func requireDoubleT(t *testing.T, input int, want int) {
	t.Helper()
	if cases.Double(input) != want {
		t.Fatalf("Double(%d) != %d", input, want)
	}
}

func requireDoubleTB(tb testing.TB, input int, want int) {
	tb.Helper()
	if cases.Double(input) != want {
		tb.Fatalf("Double(%d) != %d", input, want)
	}
}

func checkDouble(t *testing.T) {
	t.Helper()
	requireDoubleT(t, 2, 4)
}

func TestDirectFatal(t *testing.T) {
	if cases.Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}

func TestErrorfInBranch(t *testing.T) {
	for _, input := range []int{1, 2} {
		if cases.Double(input) != input*2 {
			t.Errorf("Double(%d) is wrong", input)
		}
	}
}

func TestHelperWithT(t *testing.T) {
	requireDoubleT(t, 2, 4)
}

func TestHelperWithTB(t *testing.T) {
	requireDoubleTB(t, 2, 4)
}

func TestSubtestFailing(t *testing.T) {
	t.Run("double", func(t *testing.T) {
		if cases.Double(2) != 4 {
			t.Error("Double(2) != 4")
		}
	})
}

func TestSubtestNamedFunction(t *testing.T) {
	t.Run("double", checkDouble)
}

func TestSubtestWithoutFailingCall(t *testing.T) { // want `Test TestSubtestWithoutFailingCall has no statement able to fail the test\. A test must assert an outcome of the code under test\. Add a check that calls t\.Error, t\.Fatal, or a helper with a testing\.TB parameter\.`
	t.Run("double", func(t *testing.T) { // want `This subtest has no statement able to fail the subtest\. A subtest must assert an outcome of the code under test\. Add a check that calls t\.Error, t\.Fatal, or a helper with a testing\.TB parameter\.`
		_ = cases.Double(2)
	})
}

func TestSubtestOnlySkip(t *testing.T) {
	t.Run("double", func(t *testing.T) {
		t.Skip("not supported")
	})
	requireDoubleT(t, 2, 4)
}

func TestOnlySkip(t *testing.T) {
	t.Skip("not supported")
}

func TestNoAssertion(t *testing.T) { // want `Test TestNoAssertion has no statement able to fail the test\. A test must assert an outcome of the code under test\. Add a check that calls t\.Error, t\.Fatal, or a helper with a testing\.TB parameter\.`
	_ = cases.Double(2)
}

func TestOnlyLog(t *testing.T) { // want `Test TestOnlyLog has no statement able to fail the test\. A test must assert an outcome of the code under test\. Add a check that calls t\.Error, t\.Fatal, or a helper with a testing\.TB parameter\.`
	t.Parallel()
	t.Helper()
	t.Logf("Double(2) = %d", cases.Double(2))
}

func TestSkipThenLog(t *testing.T) { // want `Test TestSkipThenLog has no statement able to fail the test\. A test must assert an outcome of the code under test\. Add a check that calls t\.Error, t\.Fatal, or a helper with a testing\.TB parameter\.`
	t.Skip("not supported")
	t.Log("unreachable")
}

func TestDeferFailing(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Error("Double panicked")
		}
	}()
	_ = cases.Double(2)
}

func TestGoroutineFailing(t *testing.T) {
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		if cases.Double(2) != 4 {
			t.Error("Double(2) != 4")
		}
	}()
	group.Wait()
}

func BenchmarkDouble(b *testing.B) {
	for range b.N {
		_ = cases.Double(2)
	}
}

func ExampleDouble() {
	_ = cases.Double(2)
}
