package sources

import (
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func reg() Registry {
	return Registry{
		"sec": {ID: "sec", URL: "https://www.sec.gov/x", Kind: model.KindFilings, Tier: model.TierPrimary,
			Licence: model.LicenceRedistributable, Terms: "https://www.sec.gov/about/privacy-information", Enabled: true},
		"fed": {ID: "fed", URL: "https://www.federalreserve.gov/f.xml", Kind: model.KindRSS, Tier: model.TierPrimary,
			Licence: model.LicenceInternal, Owner: "default", Enabled: false},
		"api": {ID: "api", URL: "https://api.example.com/news", Kind: model.KindAPI, Tier: model.TierReporting,
			Licence: model.LicencePersonal, Owner: "alice", Enabled: true},
	}
}

func TestCheckAcceptsGoodRegistry(t *testing.T) {
	if err := reg().Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckFindsBadSourceAndWrongKey(t *testing.T) {
	r := reg()
	bad := r["sec"]
	bad.Licence = ""
	r["sec"] = bad
	r["misfiled"] = model.Source{ID: "other", URL: "https://x.example.com", Kind: model.KindRSS, Tier: 1,
		Licence: model.LicenceInternal, Owner: "default"}
	err := r.Check()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"licence", "listed under key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func TestEnabledIsSorted(t *testing.T) {
	got := reg().Enabled()
	if len(got) != 2 || got[0].ID != "api" || got[1].ID != "sec" {
		t.Fatalf("Enabled() = %+v", got)
	}
}

func TestVisibleTo(t *testing.T) {
	alice := reg().VisibleTo("alice")
	if !alice["sec"] || !alice["api"] || alice["fed"] {
		t.Errorf("alice sees %v; want sec and her own api, not default's fed", alice)
	}
	bob := reg().VisibleTo("bob")
	if !bob["sec"] || bob["api"] || bob["fed"] {
		t.Errorf("bob sees %v; want only the redistributable source", bob)
	}
}
