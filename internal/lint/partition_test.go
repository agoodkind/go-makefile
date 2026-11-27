package lint

import (
	"reflect"
	"testing"
)

func TestPartitionTestPackages(t *testing.T) {
	packages := []string{"example/daemon", "example/daemon/cli", "example/other"}
	shards := []testShard{
		{Name: "daemon", Packages: []string{"example/daemon/..."}},
		{Name: "remaining"},
	}
	want := map[string][]string{
		"daemon":    {"example/daemon", "example/daemon/cli"},
		"remaining": {"example/other"},
	}

	got, err := partitionTestPackages(packages, shards)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionTestPackages = %v, %v; want %v", got, err, want)
	}
	if _, err := partitionTestPackages(packages, shards[:1]); err == nil {
		t.Fatal("accepted an unassigned package")
	}
	shards[1].Packages = []string{"example/daemon/cli"}
	if _, err := partitionTestPackages(packages, shards); err == nil {
		t.Fatal("accepted a package in multiple shards")
	}
}
