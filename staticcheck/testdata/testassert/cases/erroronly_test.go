package cases_test

import (
	"errors"
	"strconv"
	"testing"
)

var errNegative = errors.New("negative")

func parse(input string) (int, error) {
	value, err := strconv.Atoi(input)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, errNegative
	}
	return value, nil
}

func TestOnlySuccessCheck(t *testing.T) { // want `Test TestOnlySuccessCheck asserts only that an error is nil or not nil\.`
	_, err := parse("4")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
}

func TestOnlyFailureCheck(t *testing.T) { // want `Test TestOnlyFailureCheck asserts only that an error is nil or not nil\.`
	if _, err := parse("x"); err == nil {
		t.Fatal("parse accepted x")
	}
}

func TestErrorAndValue(t *testing.T) {
	value, err := parse("4")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if value != 4 {
		t.Fatalf("value = %d", value)
	}
}

func TestErrorKind(t *testing.T) {
	_, err := parse("-1")
	if !errors.Is(err, errNegative) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorThenHelper(t *testing.T) {
	_, err := parse("4")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	requireDoubleT(t, 2, 4)
}
