package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"

	"github.com/b070nd/stoxicity/app/src/scheduler"
	"github.com/b070nd/stoxicity/app/src/status"
)

// boardEvery is how often the board redraws, so that the elapsed times count.
const boardEvery = time.Second

// renderBoard draws every source as lines of at most width cells, at most
// maxRows of them besides the summary line: first those with work, with the
// time they have been at it, then the idle ones, with the time their timers
// run out and what their last poll came to.
func renderBoard(b scheduler.Board, zone *time.Location, width, maxRows int) []string {
	var running, waiting int
	for _, r := range b.Rows {
		switch r.State {
		case scheduler.Running:
			running++
		case scheduler.Idle:
		default:
			waiting++
		}
	}
	polling := fmt.Sprint(running)
	if b.Workers > 0 {
		polling = fmt.Sprintf("%d/%d", running, b.Workers) // of the most that may run at once
	}
	sum := fmt.Sprintf("%s  %s polling · %d waiting · %d idle", b.At.In(zone).Format(time.TimeOnly), polling, waiting, b.Idle)
	if b.Next != "" {
		sum += fmt.Sprintf(" · next %s in %s", b.Next, clock(b.NextDue.Sub(b.At)))
	}
	if m := status.Summarise(status.FromBoard(b, nil)); m.Green+m.Yellow+m.Red > 0 {
		sum += fmt.Sprintf(" · health %d ok, %d stale, %d failing", m.Green, m.Yellow, m.Red)
	}
	lines := []string{sum}
	for i, r := range b.Rows {
		if i == maxRows && len(b.Rows) > maxRows {
			lines = append(lines, fmt.Sprintf("   … %d more", len(b.Rows)-i))
			break
		}
		lines = append(lines, "   "+boardRow(r, b.At, zone))
	}
	for i, l := range lines {
		lines[i] = cut(l, width)
	}
	return lines
}

// boardRow is one source on the board.
func boardRow(r scheduler.Activity, at time.Time, zone *time.Location) string {
	id := fmt.Sprintf("%-24s", r.SourceID)
	switch r.State {
	case scheduler.Running:
		return fmt.Sprintf("▶ %s polling   %s", id, clock(at.Sub(r.Since)))
	case scheduler.Late:
		return fmt.Sprintf("◷ %s late      %s  (timer ran out while polling; starts again when it ends)", id, clock(at.Sub(r.Since)))
	case scheduler.Resting:
		return fmt.Sprintf("⏸ %s resting   %s left  (%s is cooling down)", id, clock(r.Until.Sub(at)), r.Host)
	case scheduler.Waiting:
		return fmt.Sprintf("… %s waiting   %s", id, clock(at.Sub(r.Since)))
	}
	switch {
	case r.Parked:
		return fmt.Sprintf("✗ %s parked    until refreshed · %s", id, oneLine(r.LastError))
	case r.Failures > 0:
		return fmt.Sprintf("! %s idle      retry in %s · failed %d× · %s", id, clock(r.Until.Sub(at)), r.Failures, oneLine(r.LastError))
	case r.LastOK.IsZero():
		return fmt.Sprintf("· %s idle      next in %s · not polled yet", id, clock(r.Until.Sub(at)))
	}
	return fmt.Sprintf("· %s idle      next in %s · last %s, %d new", id, clock(r.Until.Sub(at)), r.LastOK.In(zone).Format("15:04:05"), r.LastNew)
}

// clock writes a duration as m:ss, or h:mm:ss from an hour.
func clock(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	h, m, s := int(d/time.Hour), int(d/time.Minute)%60, int(d/time.Second)%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// cut shortens s to at most width cells (runes, which is exact for this text);
// a line that wrapped would break the count of lines to erase.
func cut(s string, width int) string {
	if width <= 1 || utf8.RuneCountInString(s) < width {
		return s
	}
	r := []rune(s)
	return string(r[:width-2]) + "…"
}

// liveBoard keeps the board at the bottom of a terminal while lines scroll
// above it: every write erases the board, writes the line and draws the board
// again. It is the writer that stdout and stderr (and with them the log) of
// "run" go through, so nothing else writes between the erase and the redraw.
type liveBoard struct {
	mu    sync.Mutex
	out   io.Writer
	size  func() (width, height int)
	snap  func() scheduler.Board
	zone  *time.Location
	shown int // lines of the board on screen now
}

// Write puts p above the board.
func (b *liveBoard) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.erase()
	n, err := b.out.Write(p)
	if err == nil && len(p) > 0 && p[len(p)-1] != '\n' {
		_, err = b.out.Write([]byte("\n"))
	}
	b.draw()
	return n, err
}

// Source sets where the board gets its rows.
func (b *liveBoard) Source(snap func() scheduler.Board) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snap = snap
}

// Redraw updates the board in place.
func (b *liveBoard) Redraw() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.erase()
	b.draw()
}

// Close takes the board off the screen.
func (b *liveBoard) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.erase()
}

func (b *liveBoard) erase() {
	if b.shown > 0 {
		fmt.Fprintf(b.out, "\x1b[%dA\r\x1b[J", b.shown)
		b.shown = 0
	}
}

func (b *liveBoard) draw() {
	w, h := b.size()
	lines := renderBoard(b.snap(), b.zone, w, max(h-6, 4))
	fmt.Fprint(b.out, strings.Join(lines, "\n")+"\n")
	b.shown = len(lines)
}

// terminalBoard returns the board for a run whose stdout and stderr are both a
// terminal, or nil.
func terminalBoard(stdout, stderr io.Writer, snap func() scheduler.Board, zone *time.Location) *liveBoard {
	out, ok1 := stdout.(interface{ Fd() uintptr })
	errf, ok2 := stderr.(interface{ Fd() uintptr })
	if !ok1 || !ok2 || !term.IsTerminal(out.Fd()) || !term.IsTerminal(errf.Fd()) {
		return nil
	}
	return &liveBoard{
		out: stdout,
		size: func() (int, int) {
			if w, h, err := term.GetSize(out.Fd()); err == nil && w > 0 && h > 0 {
				return w, h
			}
			return 80, 24
		},
		snap: snap,
		zone: zone,
	}
}
