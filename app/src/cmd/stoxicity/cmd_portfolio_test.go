package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const pfHeader = "id,date,kind,security,qty,amount,gross,commission,withheld,to,rate,ratio,ref,order,settle_day,price,memo\n"

// pfRow writes one CSV line in the order of the header from name, value pairs.
func pfRow(id, date, kind string, kv ...string) string {
	m := map[string]string{"id": id, "date": date, "kind": kind}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	cols := strings.Split(strings.TrimSpace(pfHeader), ",")
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = m[c]
	}
	return strings.Join(cells, ",") + "\n"
}

func statement() string {
	return pfHeader +
		pfRow("d1", "2026-11-02", "deposit", "amount", "10000.00 USD") +
		pfRow("b1", "2026-11-02", "buy", "security", "sec_apple", "qty", "10", "gross", "1500.00 USD", "commission", "1.00 USD", "settle_day", "2026-11-04", "price", "150.00") +
		pfRow("s1", "2026-11-04", "settle", "ref", "b1") +
		pfRow("x1", "2026-11-05", "sell", "security", "sec_apple", "qty", "4", "gross", "700.00 USD", "commission", "1.00 USD", "settle_day", "2026-11-07", "price", "175.00") +
		pfRow("s2", "2026-11-07", "settle", "ref", "x1") +
		pfRow("v1", "2026-11-10", "dividend", "gross", "20.00 USD", "withheld", "3.00 USD") +
		pfRow("f1", "2026-11-11", "fx", "amount", "1000.00 USD", "to", "920.00 EUR", "commission", "1.00 USD", "rate", "0.92") +
		pfRow("p1", "2026-11-12", "split", "security", "sec_apple", "ratio", "2:1") +
		pfRow("r1", "2026-11-13", "reinvest", "security", "sec_apple", "qty", "0.5", "gross", "10.00 USD", "withheld", "1.50 USD")
}

func TestPortfolioWorkflow(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "portfolio", "book", "list")
	r.wantCode(t, exitOK)
	r.wantOut(t, "no books yet")

	// a book needs its environment said out loud
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core").wantCode(t, exitUsage)
	r = e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "live")
	r.wantCode(t, exitError)
	r.wantErr(t, "unknown environment")
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real").wantCode(t, exitOK)
	r = e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "core", "-env", "real")
	r.wantCode(t, exitError)
	r.wantErr(t, "exists")
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "paper", "-env", "paper_local", "-method", "lifo").wantCode(t, exitOK)
	r = e.run(t, "portfolio", "book", "list")
	r.wantOut(t, "core", "paper", "real", "paper_local", "lifo", "bk_")

	// a security that is not in the register cannot be bought
	r = e.run(t, "portfolio", "add", "main/core", "deposit", "-date", "2026-11-02", "-amount", "10000.00 USD")
	r.wantCode(t, exitOK)
	r = e.run(t, "portfolio", "add", "main/core", "buy", "-date", "2026-11-02", "-security", "sec_apple", "-qty", "1", "-gross", "100.00 USD")
	r.wantCode(t, exitError)
	r.wantErr(t, "not in the instrument register")
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	// the deposit above is a different record from the statement's: reset by using a fresh book
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "stmt", "-env", "real").wantCode(t, exitOK)

	file := writeFile(t, e, "stmt.csv", statement())
	r = e.run(t, "portfolio", "import", "main/stmt", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "9 lines read, 9 recorded, 0 already recorded", "sha256:")
	r = e.run(t, "portfolio", "import", "main/stmt", "-file", file)
	r.wantCode(t, exitOK)
	r.wantOut(t, "9 lines read, 0 recorded, 9 already recorded")

	r = e.run(t, "portfolio", "show", "main/stmt", "-lots")
	r.wantCode(t, exitOK)
	r.wantOut(t, "main/stmt", "9 transactions",
		"8214.00 USD", "920.00 EUR", // cash after trades, commissions, dividend, conversion
		"sec_apple", "12.5", "908.50 USD", // 6 shares doubled, plus 0.5 reinvested at 10.00 - 1.50
		"Apple Inc. common stock",
		"realised gain on sales: 100.00 USD", "not tax reporting",
		"the books balance in every currency")
	r.wantNotOut(t, "open order:")

	var v struct {
		Txs      int  `json:"transactions"`
		Balanced bool `json:"balanced"`
		Cash     []struct {
			Currency  string                            `json:"currency"`
			Settled   struct{ Amount string }           `json:"settled"`
			Available struct{ Amount string }           `json:"available"`
			Unsettled struct{ Amount, Currency string } `json:"unsettled"`
		} `json:"cash"`
	}
	r = e.run(t, "portfolio", "show", "main/stmt", "-json")
	if err := json.Unmarshal([]byte(r.out), &v); err != nil || v.Txs != 9 || !v.Balanced || len(v.Cash) != 2 {
		t.Fatalf("%v %+v\n%s", err, v, r.out)
	}

	// the book as it stood after the first sale, by event time
	r = e.run(t, "portfolio", "show", "main/stmt", "-as-of", "2026-11-05")
	r.wantOut(t, "4 transactions", "as of", "by event time", "8498.00 USD", "700.00 USD")
	e.run(t, "portfolio", "show", "main/stmt", "-as-of", "2026-11-05", "-basis", "wall").wantCode(t, exitError)
	r = e.run(t, "portfolio", "show", "main/stmt", "-as-of", "2020-01-01", "-basis", "recorded")
	r.wantOut(t, "0 transactions", "no cash", "no positions")

	// a statement of the same day agrees; one that differs is reported in full and changes nothing
	ok := writeFile(t, e, "ok.csv", "kind,security,qty,currency,amount\ncash,,,USD,8214.00\ncash,,,EUR,920.00\nposition,sec_apple,12.5,,\n")
	r = e.run(t, "portfolio", "reconcile", "main/stmt", "-file", ok, "-as-of", "2026-11-13")
	r.wantCode(t, exitOK)
	r.wantOut(t, "reconciled", "not that the statement is right")
	off := writeFile(t, e, "off.csv", "kind,security,qty,currency,amount\ncash,,,USD,8200.00\ncash,,,EUR,920.00\nposition,sec_apple,12,,\nposition,sec_sap,1,,\n")
	r = e.run(t, "portfolio", "reconcile", "main/stmt", "-file", off, "-as-of", "2026-11-13")
	r.wantCode(t, exitError)
	r.wantOut(t, "cash USD", "8214.00 USD", "8200.00 USD", "sec_apple", "12.5", "sec_sap", "does not know", "Nothing was changed")
	r.wantErr(t, "3 differences")
	// as of the 12th the reinvestment had not happened yet
	r = e.run(t, "portfolio", "reconcile", "main/stmt", "-file", ok, "-as-of", "2026-11-12")
	r.wantCode(t, exitError)
	r.wantOut(t, "sec_apple", "12 ")
	e.run(t, "portfolio", "reconcile", "main/stmt", "-file", ok).wantCode(t, exitUsage)
	badStmt := writeFile(t, e, "badstmt.csv", "kind,security,qty,currency,amount\nholding,sec_apple,1,,\n")
	e.run(t, "portfolio", "reconcile", "main/stmt", "-file", badStmt, "-as-of", "2026-11-13").wantCode(t, exitError)

	// a changed line is a conflict, and a batch with one is not imported at all
	bad := writeFile(t, e, "bad.csv", pfHeader+
		pfRow("n1", "2026-11-14", "deposit", "amount", "5.00 USD")+
		pfRow("d1", "2026-11-02", "deposit", "amount", "10001.00 USD"))
	r = e.run(t, "portfolio", "import", "main/stmt", "-file", bad)
	r.wantCode(t, exitError)
	r.wantErr(t, "line 3", "d1", "other content", "nothing was imported")
	r = e.run(t, "portfolio", "show", "main/stmt")
	r.wantOut(t, "9 transactions")

	// every form error is reported at once, and nothing is imported
	worse := writeFile(t, e, "worse.csv", pfHeader+
		pfRow("w1", "2026-13-40", "deposit", "amount", "5.00 USD")+
		pfRow("w2", "2026-11-14", "gift", "amount", "5.00 USD")+
		pfRow("w3", "2026-11-14", "deposit", "amount", "5 dollars")+
		pfRow("w4", "2026-11-14", "deposit", "amount", "5.00 USD", "qty", "3")+
		pfRow("w5", "2026-11-14", "buy", "security", "sec_apple", "qty", "1")+
		pfRow("", "2026-11-14", "deposit", "amount", "5.00 USD")+
		pfRow("w3", "2026-11-14", "deposit", "amount", "5.00 USD")+
		pfRow("w7", "2026-11-14", "split", "security", "sec_apple", "ratio", "2-1"))
	r = e.run(t, "portfolio", "import", "main/stmt", "-file", worse)
	r.wantCode(t, exitError)
	r.wantErr(t, "line 2", "YYYY-MM-DD", "line 3", "unknown kind", "line 4", "currency", "line 5", "does not use \"qty\"", "line 6", `needs "gross"`, "line 7", "every line needs an id", "line 8", "also on line 4", "line 9", "NEW:OLD", "nothing was imported")
	unknownCol := writeFile(t, e, "col.csv", "id,date,kind,colour\nz,2026-11-14,deposit,red\n")
	r = e.run(t, "portfolio", "import", "main/stmt", "-file", unknownCol)
	r.wantCode(t, exitError)
	r.wantErr(t, "unknown column")

	// the rules of the ledger hold at the command line
	r = e.run(t, "portfolio", "add", "main/stmt", "sell", "-date", "2026-11-20", "-security", "sec_apple", "-qty", "99", "-gross", "1.00 USD")
	r.wantCode(t, exitError)
	r.wantErr(t, "not enough shares")
	r = e.run(t, "portfolio", "add", "main/stmt", "withdrawal", "-date", "2026-11-20", "-amount", "99999.00 USD")
	r.wantCode(t, exitError)
	r.wantErr(t, "not enough cash")
	r = e.run(t, "portfolio", "add", "main/stmt", "gift", "-date", "2026-11-20")
	r.wantCode(t, exitUsage)

	// a correction is a reversal; nothing is edited
	r = e.run(t, "portfolio", "add", "main/stmt", "reverse", "-date", "2026-11-14", "-ref", "r1", "-import-id", "rev-r1")
	r.wantCode(t, exitOK)
	r.wantOut(t, "reversal", "sec_apple -0.5")
	r = e.run(t, "portfolio", "add", "main/stmt", "reverse", "-date", "2026-11-14", "-ref", "r1", "-import-id", "rev-r1")
	r.wantCode(t, exitOK)
	r.wantOut(t, "already recorded")
	r = e.run(t, "portfolio", "add", "main/stmt", "reverse", "-date", "2026-11-15", "-ref", "b1")
	r.wantCode(t, exitError)
	r.wantErr(t, "settled by")
	r = e.run(t, "portfolio", "add", "main/stmt", "reverse", "-date", "2026-11-15", "-ref", "rev-r1")
	r.wantCode(t, exitError)
	r.wantErr(t, "cannot be reversed")
	r = e.run(t, "portfolio", "show", "main/stmt")
	r.wantOut(t, "10 transactions", "900.00 USD", "the books balance")

	r = e.run(t, "portfolio", "journal", "main/stmt")
	r.wantCode(t, exitOK)
	r.wantOut(t, "import:stmt.csv", "deposit", "d1", "reversed by", "reverses", "unsettled -1500.00 USD", "sec_apple +10")
	r = e.run(t, "portfolio", "journal", "main/stmt", "-limit", "2", "-json")
	var j []struct {
		Seq        int
		Kind       string
		ReversedBy string `json:"reversed_by"`
	}
	if err := json.Unmarshal([]byte(r.out), &j); err != nil || len(j) != 2 || j[1].Kind != "reversal" || j[0].Seq != 9 {
		t.Fatalf("%v %+v", err, j)
	}
	if r.code != exitOK || !strings.Contains(r.out, "reversed_by") {
		t.Fatalf("the reversed transaction should say so:\n%s", r.out)
	}

	// transfers stay inside one environment
	e.run(t, "portfolio", "book", "create", "-account", "main", "-name", "paper2", "-env", "paper_local").wantCode(t, exitOK)
	e.run(t, "portfolio", "add", "main/paper", "deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	tr := []string{"portfolio", "transfer", "-from", "main/paper", "-to", "main/paper2", "-date", "2026-11-03", "-amount", "400.00 EUR", "-import-id", "tr1"}
	r = e.run(t, tr...)
	r.wantCode(t, exitOK)
	r.wantOut(t, "moved", "cash -400.00 EUR", "cash +400.00 EUR")
	r = e.run(t, tr...)
	r.wantOut(t, "already recorded")
	r = e.run(t, "portfolio", "show", "main/paper2")
	r.wantOut(t, "400.00 EUR")
	for _, pair := range [][2]string{{"main/stmt", "main/paper"}, {"main/paper", "main/stmt"}} {
		r = e.run(t, "portfolio", "transfer", "-from", pair[0], "-to", pair[1], "-date", "2026-11-16", "-amount", "1.00 USD")
		r.wantCode(t, exitError)
		r.wantErr(t, "never exchange")
	}
	e.run(t, "portfolio", "transfer", "-from", "main/paper").wantCode(t, exitUsage)

	// a real book is never archived or reset; a paper book is
	for _, args := range [][]string{{"book", "archive", "main/stmt"}, {"book", "reset", "main/stmt", "-name", "fresh"}} {
		r = e.run(t, append([]string{"portfolio"}, args...)...)
		r.wantCode(t, exitError)
		r.wantErr(t, "cannot be archived or reset")
	}
	r = e.run(t, "portfolio", "book", "reset", "main/paper", "-name", "paper-3")
	r.wantCode(t, exitOK)
	r.wantOut(t, "archived with its journal intact", "new empty paper_local book main/paper-3")
	r = e.run(t, "portfolio", "add", "main/paper", "deposit", "-date", "2026-11-17", "-amount", "1.00 EUR")
	r.wantCode(t, exitError)
	r.wantErr(t, "archived")
	r = e.run(t, "portfolio", "show", "main/paper-3")
	r.wantOut(t, "0 transactions", "no cash", "no positions")

	r = e.run(t, "portfolio", "verify")
	r.wantCode(t, exitOK)
	r.wantOut(t, "intact", "main/stmt", "main/paper", "chain hashes")
	r.wantNotOut(t, "FAILED")
	e.run(t, "portfolio", "verify", "main/none").wantCode(t, exitError)

	// another tenant does not see any of it
	r = e.run(t, "-tenant", "other", "portfolio", "book", "list")
	if r.code == exitOK {
		r.wantOut(t, "no books yet")
	}
	e.run(t, "portfolio").wantCode(t, exitUsage)
	e.run(t, "portfolio", "frobnicate").wantCode(t, exitUsage)
}

func TestPortfolioReservationsAndPaperBooks(t *testing.T) {
	e := newTestEnv(t)
	inst := writeInstruments(t, e)
	e.run(t, "instruments", "import", "-file", inst, "-source", "fixture").wantCode(t, exitOK)
	e.run(t, "portfolio", "book", "create", "-account", "demo", "-name", "sim", "-env", "paper_broker").wantCode(t, exitOK)
	add := func(args ...string) result {
		return e.run(t, append([]string{"portfolio", "add", "demo/sim"}, args...)...)
	}
	add("deposit", "-date", "2026-11-02", "-amount", "1000.00 EUR").wantCode(t, exitOK)
	add("reserve_cash", "-date", "2026-11-02", "-order", "ord1", "-amount", "600.00 EUR", "-memo", "limit buy SAP").wantCode(t, exitOK)
	r := e.run(t, "portfolio", "show", "demo/sim")
	r.wantOut(t, "paper_broker", "400.00 EUR", "open order: ord1 holds 600.00 EUR limit buy SAP")

	// the held cash cannot be spent by anything else
	r = add("buy", "-date", "2026-11-03", "-security", "sec_sap", "-qty", "5", "-gross", "500.00 EUR")
	r.wantCode(t, exitError)
	r.wantErr(t, "not enough cash")
	r = add("withdrawal", "-date", "2026-11-03", "-amount", "500.00 EUR")
	r.wantCode(t, exitError)
	// the fill that names the order may
	r = add("buy", "-date", "2026-11-03", "-security", "sec_sap", "-qty", "5", "-gross", "600.00 EUR", "-order", "ord1")
	r.wantCode(t, exitOK)
	r = e.run(t, "portfolio", "show", "demo/sim")
	r.wantNotOut(t, "open order:")
	r.wantOut(t, "sec_sap", "unsettled", "-600.00 EUR")

	// shares held for a sell order cannot be sold by another one
	add("reserve_shares", "-date", "2026-11-04", "-order", "ord2", "-security", "sec_sap", "-qty", "5").wantCode(t, exitOK)
	r = add("sell", "-date", "2026-11-04", "-security", "sec_sap", "-qty", "1", "-gross", "130.00 EUR")
	r.wantCode(t, exitError)
	r.wantErr(t, "not enough shares")
	add("release", "-date", "2026-11-04", "-order", "ord2").wantCode(t, exitOK)
	add("release", "-date", "2026-11-04", "-order", "ord2").wantCode(t, exitError)
	add("sell", "-date", "2026-11-04", "-security", "sec_sap", "-qty", "1", "-gross", "130.00 EUR").wantCode(t, exitOK)
	// an order the sell does not own
	r = add("sell", "-date", "2026-11-04", "-security", "sec_sap", "-qty", "1", "-gross", "130.00 EUR", "-order", "ghost")
	r.wantCode(t, exitError)
	r.wantErr(t, "is not active")

	// a paper book is reset to a new one; the old one keeps its journal
	r = e.run(t, "portfolio", "book", "reset", "demo/sim", "-name", "sim-2")
	r.wantCode(t, exitOK)
	r = e.run(t, "portfolio", "journal", "demo/sim")
	r.wantOut(t, "reserve", "release", "hold 600.00 EUR for ord1")
	e.run(t, "portfolio", "verify", "demo/sim").wantCode(t, exitOK)
	r = e.run(t, "portfolio", "book", "list")
	r.wantOut(t, "archived", "sim-2")
}
