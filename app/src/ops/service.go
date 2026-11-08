package ops

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// ServiceFile is one file of a service definition.
type ServiceFile struct {
	Name    string
	Content string
}

// ServiceSpec says what to run and where. Nothing here installs or starts
// anything: the files are for you to read, place and enable.
type ServiceSpec struct {
	Kind      string // launchd or systemd
	Binary    string // absolute path of the stoxicity program
	ConfigDir string // absolute path of the configuration folder (the one with config.yaml)
	LogDir    string // launchd only: where the output goes
	Mirror    string // optional second location for backups
	// Environment is the file with the settings; it is named, never read.
	EnvFile string // systemd only: EnvironmentFile
}

type job struct {
	name     string
	desc     string
	args     []string
	every    int    // seconds, launchd
	calendar string // systemd OnCalendar
	hour     int
	minute   int // launchd daily jobs
}

func (s ServiceSpec) jobs() []job {
	backup := []string{"ops", "backup", "-keep", "7", "-keep-weeks", "4"}
	if s.Mirror != "" {
		backup = append(backup, "-mirror", s.Mirror)
	}
	return []job{
		{name: "alerts", desc: "stoxicity: look for changes and deliver alerts", args: []string{"alerts", "scan", "-deliver"}, every: 900, calendar: "*:0/15"},
		{name: "backup", desc: "stoxicity: back up, verify, mirror and rotate", args: backup, calendar: "*-*-* 02:30:00", hour: 2, minute: 30},
	}
}

func (s ServiceSpec) argv(j job) []string {
	return append([]string{s.Binary, "-config-dir", s.ConfigDir}, j.args...)
}

// Service renders the service files for the spec.
func Service(s ServiceSpec) ([]ServiceFile, error) {
	if !strings.HasPrefix(s.Binary, "/") || !strings.HasPrefix(s.ConfigDir, "/") {
		return nil, fmt.Errorf("ops: the program and the configuration folder must be absolute paths")
	}
	switch s.Kind {
	case "launchd":
		return s.launchd()
	case "systemd":
		return s.systemd(), nil
	}
	return nil, fmt.Errorf("ops: the kind is launchd or systemd")
}

func xmlEsc(v string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(v))
	return b.String()
}

func (s ServiceSpec) launchd() ([]ServiceFile, error) {
	if s.LogDir == "" || !strings.HasPrefix(s.LogDir, "/") {
		return nil, fmt.Errorf("ops: launchd needs an absolute log folder")
	}
	var out []ServiceFile
	for _, j := range s.jobs() {
		var b strings.Builder
		label := "dev.stoxicity." + j.name
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n<plist version=\"1.0\">\n<dict>\n")
		fmt.Fprintf(&b, "  <key>Label</key><string>%s</string>\n  <key>ProgramArguments</key>\n  <array>\n", xmlEsc(label))
		for _, a := range s.argv(j) {
			fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEsc(a))
		}
		b.WriteString("  </array>\n")
		if j.every > 0 {
			fmt.Fprintf(&b, "  <key>StartInterval</key><integer>%d</integer>\n", j.every)
		} else {
			fmt.Fprintf(&b, "  <key>StartCalendarInterval</key>\n  <dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>\n", j.hour, j.minute)
		}
		fmt.Fprintf(&b, "  <key>StandardOutPath</key><string>%s</string>\n  <key>StandardErrorPath</key><string>%s</string>\n", xmlEsc(s.LogDir+"/"+j.name+".log"), xmlEsc(s.LogDir+"/"+j.name+".err"))
		b.WriteString("  <key>ProcessType</key><string>Background</string>\n  <key>LowPriorityIO</key><true/>\n</dict>\n</plist>\n")
		out = append(out, ServiceFile{Name: label + ".plist", Content: b.String()})
	}
	return out, nil
}

// sd quotes one word for a systemd command line.
func sd(w string) string {
	w = strings.ReplaceAll(w, `\`, `\\`)
	w = strings.ReplaceAll(w, `"`, `\"`)
	w = strings.ReplaceAll(w, `%`, `%%`)
	w = strings.ReplaceAll(w, `$`, `$$`)
	return `"` + w + `"`
}

func (s ServiceSpec) systemd() []ServiceFile {
	var out []ServiceFile
	for _, j := range s.jobs() {
		var svc strings.Builder
		fmt.Fprintf(&svc, "[Unit]\nDescription=%s\n\n[Service]\nType=oneshot\n", j.desc)
		if s.EnvFile != "" {
			fmt.Fprintf(&svc, "EnvironmentFile=-%s\n", s.EnvFile)
		}
		words := make([]string, 0, 8)
		for _, a := range s.argv(j) {
			words = append(words, sd(a))
		}
		fmt.Fprintf(&svc, "ExecStart=%s\nNice=10\nIOSchedulingClass=idle\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectHome=read-only\nReadWritePaths=%s\nTimeoutStartSec=30min\n", strings.Join(words, " "), s.ConfigDir)
		if s.Mirror != "" && j.name == "backup" {
			fmt.Fprintf(&svc, "ReadWritePaths=%s\n", s.Mirror)
		}
		out = append(out, ServiceFile{Name: "stoxicity-" + j.name + ".service", Content: svc.String()})
		tm := fmt.Sprintf("[Unit]\nDescription=%s\n\n[Timer]\nOnCalendar=%s\nPersistent=true\nRandomizedDelaySec=30\n\n[Install]\nWantedBy=timers.target\n", j.desc, j.calendar)
		out = append(out, ServiceFile{Name: "stoxicity-" + j.name + ".timer", Content: tm})
	}
	return out
}
