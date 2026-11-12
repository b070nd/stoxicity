package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	supervise "github.com/b070nd/stoxicity/app/src/sidecar"
)

// TestLayaHelperProcess is not a test: it is the fake laya-serve that
// fakeLayaCommand starts. It serves /health on LAYA_HOST:LAYA_PORT, records
// what it was given in $FAKE_LAYA_DIR, and ends on SIGTERM.
func TestLayaHelperProcess(t *testing.T) {
	dir := os.Getenv("FAKE_LAYA_DIR")
	if dir == "" {
		t.Skip("helper process")
	}
	record := fmt.Sprintf("host=%s port=%s key=%s", os.Getenv("LAYA_HOST"), os.Getenv("LAYA_PORT"), os.Getenv("LAYA_API_KEY"))
	os.WriteFile(filepath.Join(dir, "started"), []byte(record), 0o600)
	var env []string
	for _, k := range []string{"LAYA_DEVICE", "LAYA_THREADS", "LAYA_PRELOAD", "LAYA_AUTO_TASK", "LAYA_MODELS", "LAYA_DEFAULT_MODEL", "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE", "PYTHONHASHSEED"} {
		env = append(env, k+"="+os.Getenv(k))
	}
	os.WriteFile(filepath.Join(dir, "env"), []byte(strings.Join(env, "\n")), 0o600)
	ln, err := net.Listen("tcp", os.Getenv("LAYA_HOST")+":"+os.Getenv("LAYA_PORT"))
	if err != nil {
		os.WriteFile(filepath.Join(dir, "error"), []byte(err.Error()), 0o600)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	go http.Serve(ln, mux)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	<-sig
	os.WriteFile(filepath.Join(dir, "terminated"), nil, 0o600)
	os.Exit(0)
}

// fakeLayaCommand writes a script that runs TestLayaHelperProcess and returns
// its path and the folder the helper records into.
func fakeLayaCommand(t *testing.T) (command, dir string) {
	t.Helper()
	dir = t.TempDir()
	script := filepath.Join(dir, "laya-serve")
	// No exec: the shell stays the group leader, and its command line names the
	// script, as the command line of the real laya-serve does.
	body := fmt.Sprintf("#!/bin/sh\n%q -test.run=TestLayaHelperProcess\n", os.Args[0])
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_LAYA_DIR", dir)
	return script, dir
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func newLayaApp(t *testing.T, e *testEnv, url, command string) (*app, *syncBuffer, *syncBuffer) {
	t.Helper()
	writeLayaConfig(t, e, url)
	t.Setenv("STOXICITY_LAYA_API_KEY", "test-key-123")
	t.Setenv("STOXICITY_LAYA_COMMAND", command)
	var out, errOut syncBuffer
	a := &app{stdout: &out, stderr: &errOut, configRoot: e.dir, opts: runOptions{
		cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
	}}
	t.Cleanup(a.close)
	a.bootstrap()
	if err := a.startupError(); err != nil {
		t.Fatal(err)
	}
	return a, &out, &errOut
}

func TestStartLayaStartsTheServerAndStopsIt(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	a, out, errOut := newLayaApp(t, e, "http://127.0.0.1:"+freePort(t), command)

	stop := a.startLaya(context.Background())
	got, err := os.ReadFile(filepath.Join(dir, "started"))
	if err != nil {
		t.Fatalf("the server was not started\nstdout:\n%s\nstderr:\n%s", out, errOut)
	}
	if !strings.HasPrefix(string(got), "host=127.0.0.1 port=") || !strings.HasSuffix(string(got), "key=test-key-123") {
		t.Errorf("the server was given %q", got)
	}
	if strings.Contains(out.String()+errOut.String(), "test-key-123") {
		t.Error("the key was printed")
	}
	if !strings.Contains(out.String(), "laya: ready") {
		t.Errorf("stdout:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir(), layaLog)); err != nil {
		t.Errorf("no log file: %v", err)
	}

	stop()
	// Stop returns when the shell that leads the group has ended. The helper
	// under it got the same SIGTERM and records it a moment later, so the
	// marker is waited for instead of being expected at once.
	marker := filepath.Join(dir, "terminated")
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the server did not get SIGTERM: %v", err)
	}
	if !strings.Contains(out.String(), "laya: stopped") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestStartLayaLeavesARunningServerAlone(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	sc := newSidecar(t, confidentAnswers("results", "earnings"))
	a, out, _ := newLayaApp(t, e, sc.URL, command)

	stop := a.startLaya(context.Background())
	stop()
	if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
		t.Error("a second server was started")
	}
	if !strings.Contains(out.String(), "already running") {
		t.Errorf("stdout:\n%s", out)
	}
	if _, err := http.Get(sc.URL + "/health"); err != nil {
		t.Errorf("the server that was already running was stopped: %v", err)
	}
}

func TestStartLayaDoesNothingWhenSwitchedOffOrRemote(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	for name, url := range map[string]string{"remote": "https://laya.example.com"} {
		t.Run(name, func(t *testing.T) {
			a, out, _ := newLayaApp(t, e, url, command)
			a.startLaya(context.Background())()
			if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
				t.Error("a server was started")
			}
			if !strings.Contains(out.String(), "not on this machine") {
				t.Errorf("stdout:\n%s", out)
			}
		})
	}
	t.Run("manage false", func(t *testing.T) {
		a, _, _ := newLayaApp(t, e, "http://127.0.0.1:"+freePort(t), command)
		a.cfg.Laya.Manage = false
		a.startLaya(context.Background())()
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			t.Error("a server was started")
		}
	})
}

func TestStartLayaReportsAMissingCommandAndCarriesOn(t *testing.T) {
	e := newTestEnv(t)
	a, out, errOut := newLayaApp(t, e, "http://127.0.0.1:"+freePort(t), "no-such-laya-serve-xyz")
	a.startLaya(context.Background())()
	if !strings.Contains(errOut.String(), "scoring with the rules alone") || !strings.Contains(errOut.String(), "STOXICITY_LAYA_COMMAND") {
		t.Errorf("stderr:\n%s\nstdout:\n%s", errOut, out)
	}
}

func TestRunStartsLayaAndStopsItWhenRunEnds(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	writeLayaConfig(t, e, "http://127.0.0.1:"+freePort(t))
	t.Setenv("STOXICITY_LAYA_API_KEY", "test-key-123")
	t.Setenv("STOXICITY_LAYA_COMMAND", command)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut syncBuffer
	codes := make(chan int, 1)
	go func() {
		codes <- run(ctx, []string{"-config-dir", e.dir, "run"}, &out, &errOut, runOptions{
			cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
			rand: func() float64 { return 0 },
		})
	}()
	deadline := time.After(15 * time.Second)
	for !strings.Contains(out.String(), "news-feed") {
		select {
		case <-deadline:
			t.Fatalf("no poll within 15s\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
		case code := <-codes:
			t.Fatalf("run ended early with %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); err != nil {
		t.Fatalf("run did not start laya\nstdout:\n%s", out.String())
	}
	cancel()
	select {
	case <-codes:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
	if _, err := os.Stat(filepath.Join(dir, "terminated")); err != nil {
		t.Errorf("laya was still running when run ended: %v", err)
	}
	if !strings.Contains(out.String(), "laya: stopped") {
		t.Errorf("stdout:\n%s", out.String())
	}
}

func TestStartLayaTakesOverAServerAnEarlierRunLeftBehindAndStopsIt(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	url := "http://127.0.0.1:" + freePort(t)
	a, out, _ := newLayaApp(t, e, url, command)

	// What a killed run leaves: a server answering, and its pid file.
	t.Setenv("LAYA_HOST", "127.0.0.1")
	t.Setenv("LAYA_PORT", url[strings.LastIndex(url, ":")+1:])
	left, err := supervise.Start(context.Background(), supervise.Spec{
		Name: "laya", Command: command, Ready: func(ctx context.Context) error {
			_, err := os.Stat(filepath.Join(dir, "started"))
			return err
		},
		PidFile: filepath.Join(a.dataDir(), layaPid), LogPath: filepath.Join(a.dataDir(), layaLog),
		Poll: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { left.Stop() })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url + "/health"); err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop := a.startLaya(context.Background())
	if !strings.Contains(out.String(), "taking over") {
		t.Fatalf("stdout:\n%s", out)
	}
	stop()
	select {
	case <-left.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the server left behind is still running")
	}
}

// layaEnv reads what the fake server saw of its settings.
func layaEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatalf("the server was not started: %v", err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	return got
}

func TestStartLayaMakesTheServerRepeatable(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	a, _, _ := newLayaApp(t, e, "http://127.0.0.1:"+freePort(t), command)
	stop := a.startLaya(context.Background())
	defer stop()

	got := layaEnv(t, dir)
	for k, want := range map[string]string{
		"LAYA_DEVICE": "cpu", "LAYA_PRELOAD": "1", "LAYA_AUTO_TASK": "0", "PYTHONHASHSEED": "0",
		"HF_HUB_OFFLINE": "1", "TRANSFORMERS_OFFLINE": "1", "LAYA_THREADS": "", "LAYA_MODELS": "",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

func TestStartLayaPassesTheConfiguredDeviceThreadsAndModel(t *testing.T) {
	e := newTestEnv(t)
	command, dir := fakeLayaCommand(t)
	writeLayaConfig(t, e, "http://127.0.0.1:"+freePort(t))
	path := filepath.Join(e.dir, "config.yaml")
	raw, _ := os.ReadFile(path)
	raw = []byte(strings.Replace(string(raw), "  batch_size: 16\n",
		"  batch_size: 16\n  device: mps\n  threads: 4\n  offline: false\n  model: news-v1\n", 1))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STOXICITY_LAYA_API_KEY", "test-key-123")
	t.Setenv("STOXICITY_LAYA_COMMAND", command)
	var out, errOut syncBuffer
	a := &app{stdout: &out, stderr: &errOut, configRoot: e.dir, opts: runOptions{
		cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
	}}
	t.Cleanup(a.close)
	a.bootstrap()
	if err := a.startupError(); err != nil {
		t.Fatal(err)
	}
	stop := a.startLaya(context.Background())
	defer stop()

	got := layaEnv(t, dir)
	for k, want := range map[string]string{
		"LAYA_DEVICE": "mps", "LAYA_THREADS": "4", "HF_HUB_OFFLINE": "", "TRANSFORMERS_OFFLINE": "",
		"LAYA_MODELS": "news-v1", "LAYA_DEFAULT_MODEL": "news-v1",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
