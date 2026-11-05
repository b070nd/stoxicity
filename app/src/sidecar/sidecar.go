// Package sidecar starts a helper server for the length of a command and stops
// it again: the program starts it, waits until it answers, and on Stop ends it
// and everything it started.
//
// The helper gets a process group of its own, so the Ctrl-C that stops the
// program does not reach it first and cut it off in the middle of a request;
// Stop asks the whole group to end (SIGTERM) and kills it (SIGKILL) if it has
// not ended after Spec.StopTimeout. If the program itself is killed without a
// chance to clean up, the helper keeps running; a later start finds it
// answering and leaves it alone.
//
// macOS and Linux only.
package sidecar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Spec says what to start and how to tell that it is ready.
type Spec struct {
	// Name is how messages call the helper, such as "laya".
	Name string
	// Command is a program name looked up in PATH, or a path.
	Command string
	Args    []string
	// Env is added to the environment of the program, as NAME=value.
	Env []string
	// LogPath receives the standard output and error of the helper. The folder
	// is created. The text is not shown anywhere else, since a helper may
	// print what it was given.
	LogPath string
	// Ready returns nil when the helper answers.
	Ready func(ctx context.Context) error
	// StartTimeout is how long to wait for Ready; zero means a minute.
	StartTimeout time.Duration
	// StopTimeout is how long Stop waits after SIGTERM before it kills; zero
	// means ten seconds.
	StopTimeout time.Duration
	// Poll is how often Ready is asked; zero means half a second.
	Poll time.Duration
	// PidFile, when set, holds the process ID of the helper while it runs.
	PidFile string
}

// Process is a running helper, started by Start or taken over by Adopt.
type Process struct {
	name    string
	pid     int
	pidFile string
	cmd     *exec.Cmd // nil for an adopted helper
	log     *os.File  // nil for an adopted helper
	stop    time.Duration

	done chan struct{} // closed when the program has ended
	err  error         // how it ended; valid after done is closed

	once sync.Once
}

// Start runs the helper and waits until it is ready. If it ends first, or is
// not ready within StartTimeout, or ctx ends, it is stopped and the error says
// why; the error names the log file, never the environment.
//
// With Spec.PidFile the process ID is written there, so that a later Adopt can
// find a helper that a killed program left behind.
func Start(ctx context.Context, s Spec) (*Process, error) {
	path, err := exec.LookPath(s.Command)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot find %q", s.Name, s.Command)
	}
	if s.StartTimeout <= 0 {
		s.StartTimeout = time.Minute
	}
	if s.StopTimeout <= 0 {
		s.StopTimeout = 10 * time.Second
	}
	if s.Poll <= 0 {
		s.Poll = 500 * time.Millisecond
	}
	log, err := openLog(s.LogPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Name, err)
	}
	cmd := exec.Command(path, s.Args...)
	cmd.Env = append(os.Environ(), s.Env...)
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, fmt.Errorf("%s: cannot start %q: %w", s.Name, s.Command, err)
	}
	p := &Process{name: s.Name, pid: cmd.Process.Pid, pidFile: s.PidFile, cmd: cmd, log: log, stop: s.StopTimeout, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	if s.PidFile != "" {
		if err := os.WriteFile(s.PidFile, []byte(strconv.Itoa(p.pid)+"\n"), 0o600); err != nil {
			p.Stop()
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
	}

	deadline := time.NewTimer(s.StartTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(s.Poll)
	defer tick.Stop()
	for {
		rctx, cancel := context.WithTimeout(ctx, s.StartTimeout)
		err := s.Ready(rctx)
		cancel()
		if err == nil {
			return p, nil
		}
		select {
		case <-p.done:
			p.close()
			return nil, fmt.Errorf("%s: ended while starting (%v); see %s", s.Name, p.err, s.LogPath)
		case <-ctx.Done():
			p.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			p.Stop()
			return nil, fmt.Errorf("%s: not ready after %s; see %s", s.Name, s.StartTimeout, s.LogPath)
		case <-tick.C:
		}
	}
}

func openLog(path string) (*os.File, error) {
	if path == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// Done is closed when the helper has ended, by Stop or on its own.
func (p *Process) Done() <-chan struct{} { return p.done }

// PID is the process ID of the helper, which is also its process group.
func (p *Process) PID() int { return p.pid }

// Adopt takes over a helper that an earlier program started with the same
// Spec.PidFile and left running, so that this program ends it too. It returns
// nil when there is nothing to take over. Because a recorded process ID can
// have been given to something else since, the process must be the leader of
// its own process group and its command line must name the Spec.Command
// program; otherwise it is left alone and the stale file is removed.
func Adopt(s Spec) *Process {
	if s.PidFile == "" {
		return nil
	}
	raw, err := os.ReadFile(s.PidFile)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 || !ours(pid, s.Command) {
		os.Remove(s.PidFile)
		return nil
	}
	if s.StopTimeout <= 0 {
		s.StopTimeout = 10 * time.Second
	}
	p := &Process{name: s.Name, pid: pid, pidFile: s.PidFile, stop: s.StopTimeout, done: make(chan struct{})}
	go func() { // there is no Wait for a process that is not our child
		for syscall.Kill(pid, 0) == nil {
			time.Sleep(100 * time.Millisecond)
		}
		close(p.done)
	}()
	return p
}

// ours reports whether pid is a process group leader running the program.
func ours(pid int, command string) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if pg, err := syscall.Getpgid(pid); err != nil || pg != pid {
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.Contains(string(out), filepath.Base(command))
}

// Stop ends the helper and everything it started, and waits until it is gone.
// It is safe to call more than once and after the helper ended by itself.
func (p *Process) Stop() error {
	p.once.Do(func() {
		select {
		case <-p.done:
		default:
			p.signal(syscall.SIGTERM)
			t := time.NewTimer(p.stop)
			defer t.Stop()
			select {
			case <-p.done:
			case <-t.C:
				p.signal(syscall.SIGKILL)
				<-p.done
			}
		}
		// Anything the helper left behind in its group goes too.
		p.signal(syscall.SIGKILL)
		p.close()
	})
	return nil
}

func (p *Process) signal(sig syscall.Signal) {
	// A negative PID is the process group.
	_ = syscall.Kill(-p.pid, sig)
}

// close releases the log and the PID file.
func (p *Process) close() {
	if p.log != nil {
		p.log.Close()
	}
	if p.pidFile != "" {
		os.Remove(p.pidFile)
	}
}
