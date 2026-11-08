package main

import (
	"strings"
	"testing"
	"time"
)

func paperEnv(t *testing.T) (*testEnv, func(args ...string) result, string) {
	t.Helper()
	e := newTestEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "paper", "-name", "sim", "-env", "paper_local", "-base", "EUR").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "paper/sim", "deposit", "-date", "2026-10-01", "-amount", "10000.00 EUR").wantCode(t, exitOK)
	later := func(args ...string) result {
		return runWith(t, runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow.AddDate(0, 0, 10) }}, append([]string{"-config-dir", e.dir}, args...)...)
	}
	r := e.run(t, "order", "draft", "paper/sim", "-security", "sec_apple", "-side", "buy", "-qty", "10", "-limit", "50", "-fee", "2.00 EUR", "-by", "me")
	r.wantCode(t, exitOK)
	id := orderIDRE.FindString(r.out)
	e.run(t, "order", "confirm", id, "-by", "me").wantCode(t, exitOK)
	e.run(t, "order", "submit", id, "-by", "me").wantCode(t, exitOK)
	return e, later, id
}

const paperBars = "security,day,open,high,low,close,volume\n" +
	"sec_apple,2026-11-03,49,51,40,50,5000000\n" + // the day of submission: never used
	"sec_apple,2026-11-04,49,51,45,50,1200000\n"

func TestPaperEngineFillsConservativelyAndReproducibly(t *testing.T) {
	e, later, id := paperEnv(t)
	bars := writeFile(t, e, "bars.csv", paperBars)
	e.run(t, "paper").wantCode(t, exitUsage)
	e.run(t, "paper", "example").wantOut(t, "security,day,open,high,low,close,volume")

	// bars must be finished days, and the book must be a local paper book
	r := e.run(t, "paper", "run", "paper/sim", "-bars", bars) // the clock is 2026-11-03: no finished bar after submission
	r.wantCode(t, exitOK)
	r.wantOut(t, "not decided", "no finished bar after 2026-11-03")
	r.wantNotOut(t, "configuration stored")
	later("paper", "run", "paper/sim").wantCode(t, exitUsage)
	later("paper", "run", "paper/sim", "-bars", bars, "-through", "2026-11-20").wantCode(t, exitUsage)

	r = later("paper", "run", "paper/sim", "-bars", bars, "-seed", "7")
	r.wantCode(t, exitOK)
	r.wantOut(t, "configuration stored with the book", "seed 7", id, "2026-11-04", "fill 10 at 50", "A fill here is a simulation")
	r.wantNotOut(t, "2026-11-03")
	e.run(t, "order", "show", id).wantOut(t, "filled", "fill paper:"+id+":2026-11-04: 10 at 50", "in the ledger")
	f := e.run(t, "paper", "fills", "paper/sim")
	f.wantOut(t, id, "sec_apple", "buy", "10", "50 EUR", "paper:"+id+":2026-11-04", "yes")
	e.run(t, "portfolio", "show", "paper/sim").wantOut(t, "sec_apple")
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)

	// running it again changes nothing, and the book's configuration cannot be changed
	again := later("paper", "run", "paper/sim", "-bars", bars)
	again.wantCode(t, exitOK)
	again.wantOut(t, "no acknowledged order is waiting")
	r = later("paper", "run", "paper/sim", "-bars", bars, "-seed", "8")
	r.wantCode(t, exitError)
	r.wantErr(t, "fixed so that its fills stay reproducible", "seed 7 is stored")
	later("paper", "run", "paper/sim", "-bars", bars, "-seed", "7").wantCode(t, exitOK)

	// the same steps in another place give the same fills
	e2, later2, id2 := paperEnv(t)
	if id2 != id {
		t.Fatalf("order IDs differ: %s %s", id, id2)
	}
	later2("paper", "run", "paper/sim", "-bars", writeFile(t, e2, "bars.csv", paperBars), "-seed", "7").wantCode(t, exitOK)
	if a, b := f.out, e2.run(t, "paper", "fills", "paper/sim").out; a != b {
		t.Fatalf("not reproducible:\n%s\n%s", a, b)
	}
}

func TestPaperEngineExpiresWhatTheDayDidNotReach(t *testing.T) {
	e, later, id := paperEnv(t)
	// the low only touches the limit
	bars := writeFile(t, e, "bars.csv", "security,day,open,high,low,close,volume\nsec_apple,2026-11-04,52,53,50,51,9000000\n")
	r := later("paper", "run", "paper/sim", "-bars", bars)
	r.wantCode(t, exitOK)
	r.wantOut(t, "expired", "only touched the limit")
	e.run(t, "order", "show", id).wantOut(t, "expired")
	e.run(t, "portfolio", "show", "paper/sim").wantNotOut(t, "sec_apple")
	e.run(t, "paper", "fills", "paper/sim").wantOut(t, "no fills")
	// what it held came back
	e.run(t, "portfolio", "show", "paper/sim").wantOut(t, "10000.00")
}

func TestPaperEngineRefusesBadInput(t *testing.T) {
	e, later, _ := paperEnv(t)
	bad := writeFile(t, e, "bad.csv", "security,day,open,high,low,close,volume\nsec_apple,2026-11-04,52,50,53,51,9000000\n")
	later("paper", "run", "paper/sim", "-bars", bad).wantCode(t, exitError)
	later("paper", "run", "paper/sim", "-bars", writeFile(t, e, "w.csv", "security,day\nsec_apple,2026-11-04\n")).wantCode(t, exitError)
	good := writeFile(t, e, "g.csv", paperBars)
	later("paper", "run", "paper/sim", "-bars", good, "-participation", "2").wantCode(t, exitError)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	r := later("paper", "run", "main/core", "-bars", good)
	r.wantCode(t, exitError)
	r.wantErr(t, "only works on paper_local")
}

func TestPaperResetArchivesOnlyTheNamedPaperBook(t *testing.T) {
	e, later, id := paperEnv(t)
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real", "-base", "EUR").wantCode(t, exitOK)
	list := e.run(t, "portfolio", "book", "list")
	var paperID, realID string
	for _, line := range strings.Split(list.out, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "bk_") {
				if strings.Contains(line, "paper_local") {
					paperID = f
				} else if strings.Contains(line, "real") {
					realID = f
				}
			}
		}
	}
	if paperID == "" || realID == "" {
		t.Fatal(list.out)
	}
	e.run(t, "paper", "reset", "paper/sim", "-name", "sim2").wantCode(t, exitUsage) // an exact ID only
	r := e.run(t, "paper", "reset", realID, "-name", "x")
	r.wantCode(t, exitError)
	r.wantErr(t, "only works on a paper_local book")
	r = e.run(t, "paper", "reset", paperID, "-name", "sim2")
	r.wantCode(t, exitOK)
	r.wantOut(t, "archived with its journal, orders and fills intact", "1 order(s) of the archived book were still open")
	e.run(t, "order", "show", id).wantCode(t, exitOK)
	e.run(t, "portfolio", "verify").wantCode(t, exitOK)
	_ = later
}
