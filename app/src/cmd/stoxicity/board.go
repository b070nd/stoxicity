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
)

// boardRows is the most rows of work the board shows; more are counted.
const boardRows = 14

// boardEvery is how often the board redraws, so that the elapsed times count.
const boardEvery = time.Second

// renderBoard draws the work in progress as lines of at most width cells: a
// summary line, then one row for each source that is being polled or is due and
// waiting, with the time it has been at it. A source that has finished has no
// row; it is idle until its timer runs out.
func renderBoard(b scheduler.Board, zone *time.Location, width int) []string {
	var running, waiting int
	for _, r := range b.Rows {
		if r.State == scheduler.Running {
			running++
		} else {
			waiting++
		}
	}
	sum := fmt.Sprintf("%s  %d polling · %d waiting · %d idle", b.At.In(zone).Format(time.TimeOnly), running, waiting, b.Idle)
	if b.Next != "" {
		sum += fmt.Sprintf(" · next %s in %s", b.Next, clock(b.NextDue.Sub(b.At)))
	}
	lines := []string{sum}
	for i, r := range b.Rows {
		if i == boardRows {
			lines = append(lines, fmt.Sprintf("   … %d more", len(b.Rows)-boardRows))
			break
		}
		var what string
		switch r.State {
		case scheduler.Running:
			what = fmt.Sprintf("▶ %-24s polling   %s", r.SourceID, clock(b.At.Sub(r.Since)))
		case scheduler.Late:
			what = fmt.Sprintf("◷ %-24s late      %s  (timer ran out while polling; starts again when it ends)", r.SourceID, clock(b.At.Sub(r.Since)))
		case scheduler.Resting:
			what = fmt.Sprintf("⏸ %-24s resting   %s left  (%s is cooling down)", r.SourceID, clock(r.Until.Sub(b.At)), r.Host)
		default:
			what = fmt.Sprintf("… %-24s waiting   %s", r.SourceID, clock(b.At.Sub(r.Since)))
		}
		lines = append(lines, "   "+what)
	}
	for i, l := range lines {
		lines[i] = cut(l, width)
	}
	return lines
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
	width func() int
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
	lines := renderBoard(b.snap(), b.zone, b.width())
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
		width: func() int {
			if w, _, err := term.GetSize(out.Fd()); err == nil && w > 0 {
				return w
			}
			return 80
		},
		snap: snap,
		zone: zone,
	}
}
