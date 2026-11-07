package edgar

import (
	"context"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

func TestParseOwnershipReadsALiveSaleFromTheSubmissionText(t *testing.T) {
	got, err := ParseOwnership(fixture(t, "form4_sale.txt"))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %v", got, err)
	}
	want := model.InsiderTrade{
		CIK: "0000008818", Owner: "Lopez Andres Alberto", OwnerCIK: "0001568244", Role: "director",
		Date: "2026-11-05", Code: "S", Acquired: false, Shares: 4000, Price: 193.802, After: 1147, Direct: true,
	}
	if got[0] != want {
		t.Errorf("got  %+v\nwant %+v", got[0], want)
	}
	if got[0].Kind() != model.TradeSell || int(got[0].Value()) != 775208 {
		t.Errorf("kind %q, value %v", got[0].Kind(), got[0].Value())
	}
}

func TestParseOwnershipKeepsOnlyTransactionsAndReadsRolesAndHoldingNature(t *testing.T) {
	got, err := ParseOwnership(fixture(t, "form4_buy.xml"))
	if err != nil || len(got) != 3 {
		t.Fatalf("%v, %v", got, err)
	}
	a, b, c := got[0], got[1], got[2]
	if a.Owner != "Doe Jane / Doe Family Trust" || a.OwnerCIK != "0002222222" {
		t.Errorf("owners: %q %q", a.Owner, a.OwnerCIK)
	}
	if a.Role != "Chief Executive Officer, director, 10% owner" {
		t.Errorf("role %q", a.Role)
	}
	if a.Kind() != model.TradeBuy || a.Shares != 10000 || a.Price != 12.5 || !a.Acquired || !a.Direct || a.After != 60000 {
		t.Errorf("first: %+v", a)
	}
	if b.Direct || b.Shares != 2500 {
		t.Errorf("a trade through a trust is indirect: %+v", b)
	}
	if c.Kind() != "" || c.Price != 0 || c.Value() != 0 {
		t.Errorf("a grant is no signal and has no price: %+v", c)
	}
}

func TestParseOwnershipRefusesWhatIsNoOwnershipDocument(t *testing.T) {
	if _, err := ParseOwnership([]byte("<html>nope</html>")); err == nil {
		t.Error("no document, no trades")
	}
	if _, err := ParseOwnership([]byte("<ownershipDocument><issuer>")); err == nil {
		t.Error("a cut document is an error")
	}
	got, err := ParseOwnership([]byte("<ownershipDocument><documentType>4</documentType></ownershipDocument>"))
	if err != nil || len(got) != 0 {
		t.Errorf("a form with no transactions is not an error: %v, %v", got, err)
	}
}

func TestTradeReaderAsksForTheSubmissionTextOfTheFilingAndStampsTheTrades(t *testing.T) {
	f := httpx.NewFake()
	const url = "https://sec.test/Archives/edgar/data/8818/000000881826000172/0000008818-26-000172.txt"
	// Each call takes the next answer: the filing, a rate-limit page (which
	// comes with status 200), and text that is no filing.
	f.Reply(url, httpx.Response{Status: 200, Body: fixture(t, "form4_sale.txt")})
	f.Reply(url, httpx.Response{Status: 200, Body: []byte("<html><body>Request Rate Threshold Exceeded</body></html>")})
	f.Reply(url, httpx.Response{Status: 200, Body: []byte(strings.Repeat("x", 50))})
	r := NewTradeReader(f, Config{WebBase: "https://sec.test/"})
	got, err := r.Trades(context.Background(), "0000008818-26-000172", "0000008818", "2026-11-06")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %v", got, err)
	}
	if got[0].Accession != "0000008818-26-000172" || got[0].Seq != 1 || got[0].Filed != "2026-11-06" {
		t.Errorf("%+v", got[0])
	}

	if _, err := r.Trades(context.Background(), "0000008818-26-000172", "0000008818", "2026-11-06"); !Blocked(err) {
		t.Errorf("a rate-limit page is a block: %v", err)
	}
	if _, err := r.Trades(context.Background(), "0000008818-26-000172", "0000008818", "2026-11-06"); err == nil || Blocked(err) {
		t.Errorf("a filing that cannot be read is permanent, not a block: %v", err)
	}
	if _, err := r.Trades(context.Background(), "nonsense", "1", ""); err == nil {
		t.Error("an invalid accession number is an error")
	}
}
