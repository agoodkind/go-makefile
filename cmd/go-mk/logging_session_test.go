package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestFindOutermostMakePID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		start int
		list  map[int]traceProcess
		want  int
	}{
		{
			name:  "make",
			start: 30,
			list: map[int]traceProcess{
				30: {pid: 30, parentPID: 20, name: "go-mk"},
				20: {pid: 20, parentPID: 1, name: "make"},
			},
			want: 20,
		},
		{
			name:  "gmake",
			start: 30,
			list: map[int]traceProcess{
				30: {pid: 30, parentPID: 20, name: "go-mk"},
				20: {pid: 20, parentPID: 1, name: "gmake"},
			},
			want: 20,
		},
		{
			name:  "gnumake",
			start: 30,
			list: map[int]traceProcess{
				30: {pid: 30, parentPID: 20, name: "go-mk"},
				20: {pid: 20, parentPID: 1, name: "gnumake"},
			},
			want: 20,
		},
		{
			name:  "nested make uses outermost process",
			start: 50,
			list: map[int]traceProcess{
				50: {pid: 50, parentPID: 40, name: "go-mk"},
				40: {pid: 40, parentPID: 30, name: "make"},
				30: {pid: 30, parentPID: 20, name: "sh"},
				20: {pid: 20, parentPID: 1, name: "gmake"},
			},
			want: 20,
		},
		{
			name:  "no make ancestor",
			start: 30,
			list: map[int]traceProcess{
				30: {pid: 30, parentPID: 20, name: "go-mk"},
				20: {pid: 20, parentPID: 1, name: "sh"},
			},
			want: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lookup := func(pid int) (traceProcess, error) {
				process, ok := test.list[pid]
				if !ok {
					return traceProcess{}, errors.New("missing process")
				}
				return process, nil
			}
			if got := findOutermostMakePID(test.start, lookup); got != test.want {
				t.Fatalf("findOutermostMakePID() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTraceSessionStoreReusesActiveSessionWithoutMAKEFLAGS(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) { return pid == 42, nil })
	first, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim first session: %v", err)
	}
	if !first.owner {
		t.Fatal("first session owner = false, want true")
	}
	const want = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	if err := first.persist(want); err != nil {
		t.Fatalf("persist first session: %v", err)
	}
	first.close()

	joined, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim active session: %v", err)
	}
	defer joined.close()
	if joined.owner {
		t.Fatal("active session owner = true, want false")
	}
	if joined.traceparent != want {
		t.Fatalf("active session traceparent = %q, want %q", joined.traceparent, want)
	}
}

func TestTraceSessionStoreHeaderlessClaimDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(int) (bool, error) { return true, nil })
	claim, err := store.claim(42, false, "")
	if err != nil {
		t.Fatalf("claim headerless session: %v", err)
	}
	defer claim.close()
	if claim.owner || claim.traceparent != "" {
		t.Fatalf("headerless claim = %#v, want no owner or traceparent", claim)
	}
	if _, err := os.Stat(store.sessionPath(42)); !os.IsNotExist(err) {
		t.Fatalf("headerless claim created a session: %v", err)
	}
	claim, err = store.claim(43, false, "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	if err != nil {
		t.Fatalf("claim headerless legacy session: %v", err)
	}
	defer claim.close()
	if claim.owner || claim.traceparent != "" {
		t.Fatalf("headerless legacy claim = %#v, want no owner or traceparent", claim)
	}
	if _, err := os.Stat(store.sessionPath(43)); !os.IsNotExist(err) {
		t.Fatalf("headerless legacy claim created a session: %v", err)
	}
}

func TestTraceSessionStoreReplacesOnlyConfirmedStaleSession(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) { return false, nil })
	if err := os.WriteFile(store.sessionPath(42), []byte("00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01\n"), 0o600); err != nil {
		t.Fatalf("write stale session: %v", err)
	}

	claim, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim stale session: %v", err)
	}
	defer claim.close()
	if !claim.owner {
		t.Fatal("stale session owner = false, want true")
	}
	if claim.traceparent != "" {
		t.Fatalf("stale session traceparent = %q, want empty", claim.traceparent)
	}
}

func TestTraceSessionStorePrunesOnlyConfirmedExitedMakes(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) {
		if pid == 42 {
			return false, nil
		}
		if pid == 43 {
			return true, nil
		}
		return false, errors.New("cannot inspect process")
	})
	for _, pid := range []int{42, 43} {
		body := "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01\n" + strconv.Itoa(pid) + "\n"
		if err := os.WriteFile(store.sessionPath(pid), []byte(body), 0o600); err != nil {
			t.Fatalf("write session %d: %v", pid, err)
		}
	}

	claim, err := store.claim(43, true, "")
	if err != nil {
		t.Fatalf("claim active session: %v", err)
	}
	defer claim.close()
	if claim.owner {
		t.Fatal("active session became a new owner")
	}
	if _, err := os.Stat(store.sessionPath(42)); !os.IsNotExist(err) {
		t.Fatalf("exited session remains: %v", err)
	}
	if _, err := os.Stat(store.sessionPath(43)); err != nil {
		t.Fatalf("active session was pruned: %v", err)
	}
}

func TestTraceSessionStoreReplacesReusedPID(t *testing.T) {
	t.Parallel()

	identity := "first"
	store := newTraceSessionStore(t.TempDir(), func(int) (bool, error) { return true, nil })
	store.processID = func(int) (string, error) { return identity, nil }
	first, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim first process: %v", err)
	}
	if err := first.persist("00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"); err != nil {
		first.close()
		t.Fatalf("persist first process: %v", err)
	}
	first.close()

	identity = "second"
	second, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim reused pid: %v", err)
	}
	defer second.close()
	if !second.owner || second.traceparent != "" {
		t.Fatalf("reused pid claim = %#v, want new owner", second)
	}
}

func TestTraceSessionStoreWaitsForLockContention(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) { return true, nil })
	first, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim first session: %v", err)
	}
	if !first.owner {
		t.Fatal("first session owner = false, want true")
	}

	result := make(chan traceSessionClaim, 1)
	errors := make(chan error, 1)
	go func() {
		claim, claimErr := store.claim(42, true, "")
		if claimErr != nil {
			errors <- claimErr
			return
		}
		result <- claim
	}()

	select {
	case <-result:
		t.Fatal("contending claim completed before the owner persisted its session")
	case claimErr := <-errors:
		t.Fatalf("contending claim: %v", claimErr)
	case <-time.After(50 * time.Millisecond):
	}

	const want = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	if err := first.persist(want); err != nil {
		t.Fatalf("persist first session: %v", err)
	}
	first.close()

	select {
	case joined := <-result:
		defer joined.close()
		if joined.owner {
			t.Fatal("contending session owner = true, want false")
		}
		if joined.traceparent != want {
			t.Fatalf("contending session traceparent = %q, want %q", joined.traceparent, want)
		}
	case claimErr := <-errors:
		t.Fatalf("contending claim: %v", claimErr)
	case <-time.After(time.Second):
		t.Fatal("contending claim did not complete after the owner persisted its session")
	}
}

func TestTraceSessionStoreImportsOnlyHeaderedLegacyTrace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	logs := filepath.Join(root, ".make", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	const traceparent = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	if err := os.WriteFile(filepath.Join(logs, ".traceparent"), []byte(traceparent+"\n42\n"), 0o644); err != nil {
		t.Fatalf("write legacy traceparent: %v", err)
	}
	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) { return pid == 42, nil })

	claim, err := store.claim(42, true, legacyTraceparent(root, 42, time.Time{}))
	if err != nil {
		t.Fatalf("claim unheadered legacy trace: %v", err)
	}
	if !claim.owner {
		claim.close()
		t.Fatal("unheadered legacy trace owner = false, want true")
	}
	claim.close()

	if err := os.WriteFile(filepath.Join(logs, ".run"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o644); err != nil {
		t.Fatalf("write legacy header proof: %v", err)
	}
	if got := legacyTraceparent(root, 42, time.Time{}); got != traceparent {
		t.Fatalf("matching legacy traceparent = %q, want %q", got, traceparent)
	}
	staleTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(logs, ".run"), staleTime, staleTime); err != nil {
		t.Fatalf("age legacy header proof: %v", err)
	}
	if got := legacyTraceparent(root, 42, time.Now()); got != "" {
		t.Fatalf("stale legacy traceparent = %q, want empty", got)
	}
	if err := os.Chtimes(filepath.Join(logs, ".run"), time.Now(), time.Now()); err != nil {
		t.Fatalf("refresh legacy header proof: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logs, ".run"), []byte("cccccccccccccccccccccccccccccccc\n"), 0o644); err != nil {
		t.Fatalf("write mismatched legacy header proof: %v", err)
	}
	if got := legacyTraceparent(root, 42, time.Time{}); got != "" {
		t.Fatalf("mismatched legacy traceparent = %q, want empty", got)
	}
	if err := os.WriteFile(filepath.Join(logs, ".run"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o644); err != nil {
		t.Fatalf("restore matching legacy header proof: %v", err)
	}
	claim, err = store.claim(42, true, legacyTraceparent(root, 42, time.Time{}))
	if err != nil {
		t.Fatalf("claim headered legacy trace: %v", err)
	}
	defer claim.close()
	if claim.owner {
		t.Fatal("headered legacy trace owner = true, want false")
	}
	if claim.traceparent != traceparent {
		t.Fatalf("headered legacy traceparent = %q, want %q", claim.traceparent, traceparent)
	}
}

func TestTraceSessionStoreFallsBackWhenUserCacheIsUnavailable(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	path := traceSessionRoot(func() (string, error) {
		return "", errors.New("cache unavailable")
	}, func() string {
		return temporary
	})
	want := filepath.Join(temporary, "go-makefile", "traces")
	if path != want {
		t.Fatalf("traceSessionRoot() = %q, want %q", path, want)
	}
}

func TestTraceSessionStoreUsesPrivatePermissions(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(int) (bool, error) { return true, nil })
	claim, err := store.claim(42, true, "")
	if err != nil {
		t.Fatalf("claim session: %v", err)
	}
	if err := claim.persist("00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"); err != nil {
		claim.close()
		t.Fatalf("persist session: %v", err)
	}
	claim.close()

	info, err := os.Stat(store.root)
	if err != nil {
		t.Fatalf("stat session root: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("session root permissions = %o, want 700", info.Mode().Perm())
	}
	info, err = os.Stat(store.sessionPath(42))
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestTraceSessionStoreKeepsConcurrentOuterMakesSeparate(t *testing.T) {
	t.Parallel()

	store := newTraceSessionStore(t.TempDir(), func(pid int) (bool, error) { return pid == 42 || pid == 43, nil })
	var waitGroup sync.WaitGroup
	claims := make(chan traceSessionClaim, 2)
	for _, pid := range []int{42, 43} {
		waitGroup.Add(1)
		go func(outerPID int) {
			defer waitGroup.Done()
			claim, err := store.claim(outerPID, true, "")
			if err != nil {
				t.Errorf("claim %d: %v", outerPID, err)
				return
			}
			claims <- claim
		}(pid)
	}
	waitGroup.Wait()
	close(claims)

	seen := make(map[int]bool)
	for claim := range claims {
		if !claim.owner {
			claim.close()
			t.Fatal("new outer make did not own its session")
		}
		if err := claim.persist("00-" + strconv.Itoa(len(seen)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"); err != nil {
			claim.close()
			t.Fatalf("persist concurrent session: %v", err)
		}
		seen[claim.outerPID] = true
		claim.close()
	}
	if len(seen) != 2 {
		t.Fatalf("session count = %d, want 2", len(seen))
	}
}
