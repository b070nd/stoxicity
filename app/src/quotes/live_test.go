package quotes

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// TestECBLiveContract fetches the ECB's real daily file once and checks that it
// has the shape this package reads. It runs only when STOXICITY_LIVE_CONTRACT=1
// and STOXICITY_CONTACT is set (the contact address goes in the User-Agent, as
// for every fetch the program makes). It is the check behind a "verified" entry
// in the capability catalogue, and the one place the tests leave the machine.
func TestECBLiveContract(t *testing.T) {
	if os.Getenv("STOXICITY_LIVE_CONTRACT") != "1" {
		t.Skip("set STOXICITY_LIVE_CONTRACT=1 to fetch the ECB's real file")
	}
	ua, err := httpx.UserAgent("stoxicity-contract-test", "0", os.Getenv("STOXICITY_CONTACT"))
	if err != nil {
		t.Skipf("STOXICITY_CONTACT is not set to an email address or https URL: %v", err)
	}
	client, err := httpx.New(httpx.Config{UserAgent: ua, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	obs, rep, err := ECB{Client: client}.Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var usd *FXObs
	for i := range obs {
		if obs[i].Quote == "USD" {
			usd = &obs[i]
		}
	}
	if rep.Days < 1 || usd == nil {
		t.Fatalf("days %d, usd %v", rep.Days, usd)
	}
	t.Logf("ECB daily: %d day(s), %d rates, EUR/USD %s on %s, skipped %v", rep.Days, len(obs), usd.Value, usd.Day, rep.Skipped)
	if usd.Value.Sign() <= 0 || usd.Basis.Executable() {
		t.Errorf("%+v", usd)
	}
}
