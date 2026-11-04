package main

import (
	"strings"
	"testing"
)

func TestUsageErrors(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"unknown command", []string{"frobnicate"}},
		{"unknown global flag", []string{"-nope", "version"}},
		{"search without query", []string{"search"}},
		{"search bad flag", []string{"search", "-nope", "x"}},
		{"search bad day", []string{"search", "-from", "yesterday", "x"}},
		{"search bad limit", []string{"search", "-limit", "0", "x"}},
		{"search reversed range", []string{"search", "-from", "2026-11-03", "-to", "2026-11-01", "x"}},
		{"digest bad day", []string{"digest", "-day", "11/03/2026"}},
		{"digest extra argument", []string{"digest", "now"}},
		{"fetch unknown source", []string{"fetch", "no-such-source"}},
		{"watch without subcommand", []string{"watch"}},
		{"watch unknown subcommand", []string{"watch", "frob"}},
		{"watch test without id", []string{"watch", "test"}},
		{"watch test bad since", []string{"watch", "test", "all-relevant", "-since", "soon"}},
		{"tenant without list", []string{"tenant"}},
		{"sources without list", []string{"sources"}},
		{"bad tenant id", []string{"-tenant", "../x", "tenant", "list"}},
		{"version with argument", []string{"version", "now"}},
		{"doctor with argument", []string{"doctor", "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.run(t, tt.args...)
			r.wantCode(t, exitUsage)
			if !strings.Contains(r.errOut, "usage") && !strings.Contains(r.errOut, "stoxicity:") {
				t.Errorf("stderr has no message:\n%s", r.errOut)
			}
			if r.out != "" {
				t.Errorf("stdout not empty:\n%s", r.out)
			}
		})
	}
}

func TestUsageListsCommands(t *testing.T) {
	r := runWith(t, runOptions{cwd: t.TempDir()})
	r.wantCode(t, exitUsage)
	for _, c := range commands() {
		if !strings.Contains(r.errOut, c.usage) {
			t.Errorf("usage lacks %q:\n%s", c.usage, r.errOut)
		}
	}
}

func TestHelpFlagPrintsUsageToStdout(t *testing.T) {
	r := runWith(t, runOptions{cwd: t.TempDir()}, "-h")
	r.wantCode(t, exitOK)
	r.wantOut(t, "commands:", "doctor")
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"-version"}} {
		r := runWith(t, runOptions{cwd: t.TempDir()}, args...)
		r.wantCode(t, exitOK)
		r.wantOut(t, "stoxicity "+version)
	}
}
