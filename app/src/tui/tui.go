// Package tui is the full-screen terminal interface: the day's digest,
// the per-domain table, a search box using the filter syntax, and stock
// and trend discovery views.
package tui

import (
	"context"
	"errors"
)

// Options configures a TUI session.
type Options struct {
	Root   string // configuration folder
	Tenant string // tenant to open; empty means tenant.Default
	Watch  string // watch to open; empty means the tenant's default_watch
}

// Run shows the interface until the user quits or ctx ends.
func Run(ctx context.Context, opts Options) error {
	return errors.New("tui: not implemented")
}
