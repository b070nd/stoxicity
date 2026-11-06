package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/scheduler"
)

var boardNow = time.Date(2026, 11, 6, 13, 44, 47, 0, time.UTC)

func TestRenderBoardShowsElapsedTimeAndWhatIsWaiting(t *testing.T) {
	b := scheduler.Board{
		At: boardNow,
		Rows: []scheduler.Activity{
			{SourceID: "sec-edgar", State: scheduler.Running, Since: boardNow.Add(-7 * time.Second)},
			{SourceID: "boe-news", State: scheduler.Running, Since: boardNow.Add(-75 * time.Second)},
			{SourceID: "fed-press", State: scheduler.Waiting, Since: boardNow.Add(-3 * time.Second)},
			{SourceID: "boe-news", State: scheduler.Late, Since: boardNow.Add(-20 * time.Second)},
			{SourceID: "ecb-press", State: scheduler.Resting, Host: "www.ecb.europa.eu", Until: boardNow.Add(2*time.Minute + 5*time.Second)},
		},
		Idle: 12, Next: "snb-press", NextDue: boardNow.Add(4*time.Minute + 12*time.Second),
	}
	got := strings.Join(renderBoard(b, time.UTC, 200, 14), "\n")
	for _, want := range []string{
		"13:44:47  2 polling · 3 waiting · 12 idle · next snb-press in 4:12",
		"sec-edgar", "polling   0:07",
		"boe-news                 polling   1:15",
		"late      0:20",
		"fed-press                waiting   0:03",
		"resting   2:05 left  (www.ecb.europa.eu is cooling down)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("board lacks %q:\n%s", want, got)
		}
	}
}

func TestRenderBoardCapsRowsAndFitsTheWidth(t *testing.T) {
	var rows []scheduler.Activity
	for range 20 {
		rows = append(rows, scheduler.Activity{SourceID: "a-source-with-a-name", State: scheduler.Waiting, Since: boardNow})
	}
	lines := renderBoard(scheduler.Board{At: boardNow, Rows: rows}, time.UTC, 30, 14)
	if len(lines) != 1+14+1 || !strings.Contains(lines[len(lines)-1], "6 more") {
		t.Fatalf("%d lines, last %q", len(lines), lines[len(lines)-1])
	}
	for _, l := range lines {
		if n := len([]rune(l)); n >= 30 {
			t.Errorf("line of %d cells does not fit width 30: %q", n, l)
		}
	}
}

func TestRenderBoardWhenNothingIsDoing(t *testing.T) {
	lines := renderBoard(scheduler.Board{At: boardNow, Idle: 17, Next: "x", NextDue: boardNow.Add(90 * time.Second)}, time.UTC, 100, 14)
	if len(lines) != 1 || !strings.Contains(lines[0], "0 polling · 0 waiting · 17 idle · next x in 1:30") {
		t.Fatalf("lines = %q", lines)
	}
}

func TestLiveBoardKeepsLinesAboveAndRedrawsBelow(t *testing.T) {
	var out bytes.Buffer
	rows := 2
	b := &liveBoard{out: &out, size: func() (int, int) { return 100, 40 }, zone: time.UTC, snap: func() scheduler.Board {
		bd := scheduler.Board{At: boardNow}
		for i := range rows {
			bd.Rows = append(bd.Rows, scheduler.Activity{SourceID: string(rune('a' + i)), State: scheduler.Running, Since: boardNow})
		}
		return bd
	}}
	b.Redraw()
	if b.shown != 3 {
		t.Fatalf("shown = %d, want a summary and two rows", b.shown)
	}
	out.Reset()
	rows = 1
	b.Write([]byte("hello"))
	got := out.String()
	// Erase the 3 lines of the board, then the line (with the newline it lacked), then the new board.
	if !strings.HasPrefix(got, "\x1b[3A\r\x1b[J") || !strings.Contains(got, "hello\n") || b.shown != 2 {
		t.Fatalf("output %q, shown %d", got, b.shown)
	}
	if strings.Index(got, "hello") > strings.Index(got, "polling") {
		t.Fatalf("the line must come before the board: %q", got)
	}
	out.Reset()
	b.Close()
	if out.String() != "\x1b[2A\r\x1b[J" || b.shown != 0 {
		t.Fatalf("Close wrote %q", out.String())
	}
}

func TestTerminalBoardIsOffOutsideATerminal(t *testing.T) {
	var out, errOut bytes.Buffer
	if terminalBoard(&out, &errOut, nil, time.UTC) != nil {
		t.Fatal("a buffer is not a terminal")
	}
}

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0:00", 7 * time.Second: "0:07", 75 * time.Second: "1:15", -5 * time.Second: "0:00",
		3661 * time.Second: "1:01:01", 1499 * time.Millisecond: "0:01",
	} {
		if got := clock(d); got != want {
			t.Errorf("clock(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderBoardListsEverySourceIncludingIdleOnes(t *testing.T) {
	b := scheduler.Board{
		At: boardNow,
		Rows: []scheduler.Activity{
			{SourceID: "sec-edgar", State: scheduler.Running, Since: boardNow.Add(-3 * time.Second)},
			{SourceID: "fed-press", State: scheduler.Idle, Until: boardNow.Add(252 * time.Second), LastOK: boardNow.Add(-time.Minute), LastNew: 3},
			{SourceID: "ecb-press", State: scheduler.Idle, Until: boardNow.Add(150 * time.Second), Failures: 2, LastError: "httpx: throttled\nhttps://x (status 429)"},
			{SourceID: "bls-jolts", State: scheduler.Idle, Until: boardNow.Add(time.Hour)},
			{SourceID: "old-feed", State: scheduler.Idle, Parked: true, LastError: "httpx: permanent (status 404)"},
		},
		Idle: 4, Next: "ecb-press", NextDue: boardNow.Add(150 * time.Second),
	}
	got := strings.Join(renderBoard(b, time.UTC, 300, 14), "\n")
	for _, want := range []string{
		"1 polling · 0 waiting · 4 idle",
		"fed-press                idle      next in 4:12 · last 13:43:47, 3 new",
		"ecb-press                idle      retry in 2:30 · failed 2× · httpx: throttled https://x (status 429)",
		"bls-jolts                idle      next in 1:00:00 · not polled yet",
		"old-feed                 parked    until refreshed · httpx: permanent (status 404)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("board lacks %q:\n%s", want, got)
		}
	}
}
