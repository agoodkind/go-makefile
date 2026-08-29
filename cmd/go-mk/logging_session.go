package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	traceSessionDirectoryMode os.FileMode = 0o700
	traceSessionFileMode      os.FileMode = 0o600
)

type traceProcess struct {
	pid       int
	parentPID int
	name      string
}

type traceSessionStore struct {
	root         string
	processAlive func(int) (bool, error)
	processID    func(int) (string, error)
}

type traceSessionClaim struct {
	store       traceSessionStore
	outerPID    int
	processID   string
	traceparent string
	owner       bool
	lock        *os.File
}

// findOutermostMakePID returns the oldest make-family ancestor. Nested make
// calls share that process, so they share one trace session.
func findOutermostMakePID(startPID int, lookup func(int) (traceProcess, error)) int {
	pid := startPID
	seen := make(map[int]bool)
	outerPID := 0
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		process, err := lookup(pid)
		if err != nil {
			return outerPID
		}
		if isMakeName(process.name) {
			outerPID = pid
		}
		if process.parentPID == pid {
			return outerPID
		}
		pid = process.parentPID
	}
	return outerPID
}

func currentOutermostMakePID() int {
	return findOutermostMakePID(os.Getpid(), readTraceProcess)
}

func readTraceProcess(pid int) (traceProcess, error) {
	parentPID, parentErr := parentPID(pid)
	if parentErr != nil {
		return traceProcess{}, parentErr
	}
	slog.Debug("lookup process name", slog.Int("pid", pid))
	output, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return traceProcess{}, err
	}
	return traceProcess{
		pid:       pid,
		parentPID: parentPID,
		name:      strings.TrimSpace(string(output)),
	}, nil
}

func isMakeName(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	return base == "make" || base == "gmake" || base == "gnumake"
}

func parentPID(pid int) (int, error) {
	slog.Debug("lookup parent pid", slog.Int("pid", pid))
	output, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(output)))
}

func defaultTraceSessionStore() traceSessionStore {
	store := newTraceSessionStore(
		traceSessionRoot(os.UserCacheDir, os.TempDir),
		traceProcessAlive,
	)
	store.processID = traceProcessID
	return store
}

func traceSessionRoot(userCacheDir func() (string, error), temporaryDir func() string) string {
	base, err := userCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = temporaryDir()
	}
	return filepath.Join(base, "go-makefile", "traces")
}

func newTraceSessionStore(root string, processAlive func(int) (bool, error)) traceSessionStore {
	return traceSessionStore{
		root:         root,
		processAlive: processAlive,
		processID: func(pid int) (string, error) {
			return strconv.Itoa(pid), nil
		},
	}
}

// claim joins an active session, imports a headered legacy session during an
// upgrade, or elects one visible process to publish a new session.
func (store traceSessionStore) claim(outerPID int, create bool, legacy string) (traceSessionClaim, error) {
	if outerPID <= 1 {
		return traceSessionClaim{}, nil
	}
	if err := os.MkdirAll(store.root, traceSessionDirectoryMode); err != nil {
		return traceSessionClaim{}, err
	}
	if err := os.Chmod(store.root, traceSessionDirectoryMode); err != nil {
		return traceSessionClaim{}, err
	}
	if err := store.pruneExited(); err != nil {
		return traceSessionClaim{}, err
	}
	processID, err := store.processID(outerPID)
	if err != nil {
		return traceSessionClaim{}, err
	}
	lock, err := os.OpenFile(store.lockPath(outerPID), os.O_CREATE|os.O_RDWR, traceSessionFileMode)
	if err != nil {
		return traceSessionClaim{}, err
	}
	if err := lock.Chmod(traceSessionFileMode); err != nil {
		_ = lock.Close()
		return traceSessionClaim{}, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return traceSessionClaim{}, err
	}

	claim := traceSessionClaim{store: store, outerPID: outerPID, processID: processID, lock: lock}
	traceparent, err := store.readActive(outerPID, processID)
	if err != nil {
		claim.close()
		return traceSessionClaim{}, err
	}
	if traceparent != "" {
		claim.traceparent = traceparent
		claim.close()
		return claim, nil
	}
	if !create {
		claim.close()
		return claim, nil
	}
	if legacy != "" {
		if err := claim.write(legacy); err != nil {
			claim.close()
			return traceSessionClaim{}, err
		}
		claim.traceparent = legacy
		claim.close()
		return claim, nil
	}
	claim.owner = true
	return claim, nil
}

// pruneExited removes only records whose recorded make process is confirmed
// gone. A failed liveness probe leaves the record intact.
func (store traceSessionStore) pruneExited() error {
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".traceparent") {
			continue
		}
		pidText := strings.TrimSuffix(entry.Name(), ".traceparent")
		pid, parseErr := strconv.Atoi(pidText)
		if parseErr != nil || pid <= 1 {
			continue
		}
		alive, aliveErr := store.processAlive(pid)
		if aliveErr != nil || alive {
			continue
		}
		if err := store.removeStale(pid); err != nil {
			return err
		}
	}
	return nil
}

func (store traceSessionStore) removeStale(pid int) error {
	lock, err := os.OpenFile(store.lockPath(pid), os.O_CREATE|os.O_RDWR, traceSessionFileMode)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	alive, aliveErr := store.processAlive(pid)
	if aliveErr != nil {
		return nil
	}
	if alive {
		processID, identityErr := store.processID(pid)
		if identityErr != nil {
			return nil
		}
		body, readErr := os.ReadFile(store.sessionPath(pid))
		if readErr != nil {
			return readErr
		}
		_, recordedID := parseTraceSession(string(body))
		if recordedID == processID {
			return nil
		}
	}
	slog.Debug("prune stale trace session", slog.Int("pid", pid))
	if err := os.Remove(store.sessionPath(pid)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (store traceSessionStore) readActive(outerPID int, processID string) (string, error) {
	path := store.sessionPath(outerPID)
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	alive, aliveErr := store.processAlive(outerPID)
	if aliveErr != nil {
		return "", aliveErr
	}
	traceparent, recordedID := parseTraceSession(string(body))
	if !alive || recordedID != processID {
		slog.Debug("prune stale trace session", slog.Int("pid", outerPID))
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", nil
	}
	if traceparent == "" {
		return "", errors.New("trace session has no traceparent")
	}
	return traceparent, nil
}

func parseTraceSession(body string) (string, string) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 2 {
		return "", ""
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
}

func (store traceSessionStore) sessionPath(outerPID int) string {
	return filepath.Join(store.root, strconv.Itoa(outerPID)+".traceparent")
}

func (store traceSessionStore) lockPath(outerPID int) string {
	return filepath.Join(store.root, strconv.Itoa(outerPID)+".lock")
}

func (claim *traceSessionClaim) persist(traceparent string) error {
	if !claim.owner {
		return nil
	}
	if strings.TrimSpace(traceparent) == "" {
		return errors.New("trace session traceparent is empty")
	}
	if err := claim.write(traceparent); err != nil {
		return err
	}
	claim.traceparent = traceparent
	return nil
}

func (claim *traceSessionClaim) write(traceparent string) error {
	slog.Debug("write trace session", slog.String("path", claim.store.sessionPath(claim.outerPID)))
	body := traceparent + "\n" + claim.processID + "\n"
	if err := os.WriteFile(claim.store.sessionPath(claim.outerPID), []byte(body), traceSessionFileMode); err != nil {
		return err
	}
	return os.Chmod(claim.store.sessionPath(claim.outerPID), traceSessionFileMode)
}

func (claim *traceSessionClaim) close() {
	if claim.lock == nil {
		return
	}
	_ = syscall.Flock(int(claim.lock.Fd()), syscall.LOCK_UN)
	_ = claim.lock.Close()
	claim.lock = nil
}

func traceProcessAlive(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func traceProcessID(pid int) (string, error) {
	slog.Debug("lookup process start identity", slog.Int("pid", pid))
	output, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		alive, aliveErr := traceProcessAlive(pid)
		if aliveErr != nil {
			return "", aliveErr
		}
		if !alive {
			return "", errors.New("make process exited")
		}
		return "", err
	}
	identity := strings.TrimSpace(string(output))
	if identity == "" {
		return "", errors.New("make process has no start identity")
	}
	return identity, nil
}

// legacyTraceparent imports only a trace whose old engine printed a header.
// The old capability probe wrote .traceparent without .run, so it stays hidden.
func legacyTraceparent(root string, outerPID int, started time.Time) string {
	traceBody, traceErr := os.ReadFile(filepath.Join(root, traceparentFile))
	runPath := filepath.Join(root, runSentinel)
	runBody, runErr := os.ReadFile(runPath)
	if traceErr != nil || runErr != nil {
		return ""
	}
	if !started.IsZero() {
		info, err := os.Stat(runPath)
		if err != nil || info.ModTime().Unix() < started.Unix() {
			return ""
		}
	}
	lines := strings.Split(strings.TrimSpace(string(traceBody)), "\n")
	if len(lines) < 2 {
		return ""
	}
	traceparent := strings.TrimSpace(lines[0])
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 || parts[1] == "" {
		return ""
	}
	if strings.TrimSpace(string(runBody)) != parts[1] {
		return ""
	}
	for _, ownerText := range strings.Split(lines[1], ",") {
		owner, err := strconv.Atoi(strings.TrimSpace(ownerText))
		if err == nil && owner == outerPID {
			return traceparent
		}
	}
	return ""
}

func loadLegacyTraceparent(outerPID int) string {
	started, err := traceProcessStart(outerPID)
	if err != nil {
		return ""
	}
	directory, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if traceparent := legacyTraceparent(directory, outerPID, started); traceparent != "" {
			return traceparent
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
		directory = parent
	}
}

func traceProcessStart(pid int) (time.Time, error) {
	identity, err := traceProcessID(pid)
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("Mon Jan 2 15:04:05 2006", identity, time.Local)
}
