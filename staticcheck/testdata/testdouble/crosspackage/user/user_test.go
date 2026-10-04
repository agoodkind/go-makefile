package user_test

import (
	"testing"

	"example.com/testdouble/crosspackage/user"
)

type memoryStore struct{} // want `The type memoryStore is a test double for the interface api\.Store\.`

func (memoryStore) Load(key string) (string, error) {
	return key, nil
}

func TestName(t *testing.T) {
	name, err := user.Name(memoryStore{})
	if err != nil || name != "name" {
		t.Fatalf("Name = %q, %v", name, err)
	}
}
