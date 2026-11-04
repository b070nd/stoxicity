// Package tui is the full-screen terminal interface: the day's digest,
// the per-domain table, a search box using the filter syntax, and stock
// and trend discovery views.
//
// The interface reads and writes through a Backend, so it knows nothing of
// databases or configuration files; package workspace supplies the real one.
// Screens are built by pure functions (Build), the Model is driven by messages,
// and only Run touches the terminal.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Options configures a TUI session.
type Options struct {
	// Backend supplies the data. It is required.
	Backend Backend
	// Watch is the watch to open; empty means the Backend's default.
	Watch string
	// Days is the length of the window the screens cover; 0 means 14.
	Days int
	// Refresh is how often the screen reloads; 0 means a minute.
	Refresh time.Duration
	// OpenURL opens a web address in the browser; nil means the system's
	// opener.
	OpenURL func(url string) error
}

func (o Options) withDefaults() Options {
	if o.Days <= 0 {
		o.Days = defaultDays
	}
	if o.Refresh <= 0 {
		o.Refresh = defaultRefresh
	}
	if o.OpenURL == nil {
		o.OpenURL = systemOpen
	}
	return o
}

// Run shows the interface until the user quits or ctx ends.
func Run(ctx context.Context, opts Options) error {
	if opts.Backend == nil {
		return errors.New("tui: no backend")
	}
	p := tea.NewProgram(New(ctx, opts), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

// systemOpen hands a web address to the platform's opener. The address is a
// single argument, never a shell command line.
func systemOpen(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", cmd.Path, err)
	}
	go cmd.Wait() // reap the child; its result does not matter
	return nil
}
