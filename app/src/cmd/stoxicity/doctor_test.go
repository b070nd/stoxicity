package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// line returns the output line that contains name, or "".
func (r result) line(name string) string {
	for _, l := range strings.Split(r.out, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

func writeEnvLocal(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, ".env.local")
	// The file holds no real value; the test only needs its permissions.
	if err := os.WriteFile(path, []byte("STOXICITY_CONTACT=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorHealthy(t *testing.T) {
	e := newTestEnv(t)
	writeEnvLocal(t, e.dir, 0o600)
	r := e.run(t, "doctor")
	r.wantCode(t, exitOK)
	r.wantNotOut(t, "FAIL", "WARN")
	for _, name := range []string{"config", "timezone", "data dir", ".env.local", "sources", "tenants", "watches", "contact"} {
		if l := r.line(name); !strings.HasPrefix(l, "OK") {
			t.Errorf("line for %q is %q, want OK", name, l)
		}
	}
	if _, err := os.Stat(filepath.Join(e.dir, "data")); err != nil {
		t.Errorf("data dir was not created: %v", err)
	}
}

func TestDoctorWarnsAboutLooseEnvLocal(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604} {
		e := newTestEnv(t)
		writeEnvLocal(t, e.dir, mode)
		r := e.run(t, "doctor")
		r.wantCode(t, exitOK) // a warning is not a failure
		l := r.line(".env.local")
		if !strings.HasPrefix(l, "WARN") || !strings.Contains(l, "chmod 600") {
			t.Errorf("mode %04o: line is %q, want a WARN that says chmod 600", mode, l)
		}
		if r.line("FAIL") != "" {
			t.Errorf("mode %04o: unexpected failure:\n%s", mode, r.out)
		}
	}
}

func TestDoctorFailsOnBrokenSources(t *testing.T) {
	e := newTestEnv(t)
	if err := os.WriteFile(filepath.Join(e.dir, "sources.yaml"), []byte("sources:\n  - id: Bad ID\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run(t, "doctor")
	r.wantCode(t, exitError)
	if l := r.line("sources"); !strings.HasPrefix(l, "FAIL") {
		t.Errorf("sources line is %q, want FAIL", l)
	}
}

func TestDoctorFailsOnBadConfig(t *testing.T) {
	e := newTestEnv(t)
	if err := os.WriteFile(filepath.Join(e.dir, "config.yaml"), []byte("timezone: Mars/Olympus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := e.run(t, "doctor")
	r.wantCode(t, exitError)
	if l := r.line("config"); !strings.HasPrefix(l, "FAIL") {
		t.Errorf("config line is %q, want FAIL", l)
	}
}

func TestDoctorContact(t *testing.T) {
	e := newTestEnv(t)
	t.Setenv("STOXICITY_CONTACT", "")

	// A source is enabled, so fetching is expected to work: failure.
	r := e.run(t, "doctor")
	r.wantCode(t, exitError)
	if l := r.line("contact"); !strings.HasPrefix(l, "FAIL") || !strings.Contains(l, "STOXICITY_CONTACT") {
		t.Errorf("contact line is %q", l)
	}

	// Nothing is enabled: only a warning.
	data, err := os.ReadFile(filepath.Join(e.dir, "sources.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	off := strings.ReplaceAll(string(data), "enabled: true", "enabled: false")
	if err := os.WriteFile(filepath.Join(e.dir, "sources.yaml"), []byte(off), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run(t, "doctor")
	r.wantCode(t, exitOK)
	if l := r.line("contact"); !strings.HasPrefix(l, "WARN") {
		t.Errorf("contact line is %q, want WARN", l)
	}
}

func TestDoctorChecksLayaOnlyWhenEnabled(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "doctor")
	r.wantCode(t, exitOK)
	if l := r.line("laya"); l != "" {
		t.Errorf("laya checked while disabled: %q", l)
	}
}
