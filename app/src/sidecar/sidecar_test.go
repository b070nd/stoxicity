package sidecar

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// spec runs body as a shell program. The scripts write PID files and a "ready"
// file into $DIR; Ready looks for the latter.
func spec(t *testing.T, body string) (Spec, string) {
	t.Helper()
	dir := t.TempDir()
	return Spec{
		Name: "fake", Command: "/bin/sh", Args: []string{"-c", body},
		Env:     []string{"DIR=" + dir},
		LogPath: filepath.Join(dir, "logs", "fake.log"),
		Ready: func(context.Context) error {
			if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
				return errors.New("not yet")
			}
			return nil
		},
		Poll: 10 * time.Millisecond, StartTimeout: 5 * time.Second, StopTimeout: 2 * time.Second,
	}, dir
}

const serving = `echo $$ > "$DIR/parent"; sleep 300 & echo $! > "$DIR/child"; touch "$DIR/ready"; echo started; wait`

func alive(t *testing.T, file string) bool {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return syscall.Kill(pid, 0) == nil
}

func TestStartWaitsForReadyAndStopEndsTheWholeGroup(t *testing.T) {
	s, dir := spec(t, serving)
	p, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !alive(t, filepath.Join(dir, "parent")) || !alive(t, filepath.Join(dir, "child")) {
		t.Fatal("the helper is not running after Start")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	default:
		t.Error("Done is not closed after Stop")
	}
	// A killed parent is reaped by Wait; the child is checked after a moment
	// because init adopts it before it disappears.
	deadline := time.Now().Add(2 * time.Second)
	for alive(t, filepath.Join(dir, "child")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(t, filepath.Join(dir, "child")) {
		t.Error("the helper's child survived Stop")
	}
	if err := p.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
	log, _ := os.ReadFile(s.LogPath)
	if !strings.Contains(string(log), "started") {
		t.Errorf("the helper's output is not in the log: %q", log)
	}
}

func TestStopKillsAHelperThatIgnoresSIGTERM(t *testing.T) {
	s, dir := spec(t, `trap '' TERM; touch "$DIR/ready"; while :; do sleep 0.05; done`)
	s.StopTimeout = 100 * time.Millisecond
	p, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	_ = dir
	start := time.Now()
	p.Stop()
	if time.Since(start) > 3*time.Second {
		t.Errorf("Stop took %v", time.Since(start))
	}
	select {
	case <-p.Done():
	default:
		t.Error("the helper is still running")
	}
}

func TestEndingBeforeReadyIsAnErrorWithTheLogPath(t *testing.T) {
	s, _ := spec(t, `echo boom >&2; exit 3`)
	_, err := Start(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "ended while starting") || !strings.Contains(err.Error(), s.LogPath) {
		t.Fatalf("error = %v", err)
	}
}

func TestNotReadyInTimeStopsTheHelper(t *testing.T) {
	s, dir := spec(t, `echo $$ > "$DIR/parent"; sleep 300`)
	s.StartTimeout = 150 * time.Millisecond
	_, err := Start(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if alive(t, filepath.Join(dir, "parent")) {
		t.Error("the helper was left running")
	}
}

func TestContextCancelStopsTheStart(t *testing.T) {
	s, _ := spec(t, `sleep 300`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if _, err := Start(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestMissingCommandIsReported(t *testing.T) {
	s, _ := spec(t, "")
	s.Command = "definitely-not-installed-xyz"
	if _, err := Start(context.Background(), s); err == nil || !strings.Contains(err.Error(), "cannot find") {
		t.Fatalf("error = %v", err)
	}
}

func TestHelperEndingByItselfIsSeenOnDone(t *testing.T) {
	s, _ := spec(t, `touch "$DIR/ready"; sleep 0.2`)
	p, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done never closed")
	}
	p.Stop()
}

func TestPidFileLivesAsLongAsTheHelper(t *testing.T) {
	s, dir := spec(t, serving)
	s.PidFile = filepath.Join(dir, "helper.pid")
	p, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.PidFile)
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(p.PID()) {
		t.Fatalf("pid file = %q, %v; want %d", b, err, p.PID())
	}
	p.Stop()
	if _, err := os.Stat(s.PidFile); !os.IsNotExist(err) {
		t.Errorf("the pid file is still there after Stop: %v", err)
	}
}

func TestAdoptTakesOverAHelperLeftBehindAndStopsIt(t *testing.T) {
	s, dir := spec(t, serving)
	s.PidFile = filepath.Join(dir, "helper.pid")
	first, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	// The program that started the helper is gone: nothing calls first.Stop.
	// The helper is not a child of the program that adopts it.
	second := Adopt(s)
	if second == nil || second.PID() != first.PID() {
		t.Fatalf("Adopt = %v, want the helper with pid %d", second, first.PID())
	}
	second.Stop()
	select {
	case <-first.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the helper is still running after the adopter stopped it")
	}
	if _, err := os.Stat(s.PidFile); !os.IsNotExist(err) {
		t.Errorf("the pid file is still there: %v", err)
	}
}

func TestAdoptIgnoresStaleAndForeignProcesses(t *testing.T) {
	s, dir := spec(t, serving)
	s.PidFile = filepath.Join(dir, "helper.pid")

	if Adopt(s) != nil {
		t.Error("adopted without a pid file")
	}
	// A process that has ended.
	dead := exec.Command("/bin/sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(s.PidFile, []byte(strconv.Itoa(dead.Process.Pid)), 0o600)
	if Adopt(s) != nil {
		t.Error("adopted a process that has ended")
	}
	if _, err := os.Stat(s.PidFile); !os.IsNotExist(err) {
		t.Error("the stale pid file was kept")
	}
	// A live process that is not a group leader running the program: this one.
	os.WriteFile(s.PidFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
	if Adopt(s) != nil {
		t.Error("adopted a foreign process")
	}
	// Rubbish.
	os.WriteFile(s.PidFile, []byte("not a number"), 0o600)
	if Adopt(s) != nil {
		t.Error("adopted rubbish")
	}
	if err := syscall.Kill(os.Getpid(), 0); err != nil {
		t.Errorf("this process was signalled: %v", err)
	}
}
