package model

import (
	"strings"
	"testing"
)

func TestEventTaxonomy(t *testing.T) {
	kinds := EventKinds()
	if len(kinds) < 30 {
		t.Fatalf("only %d kinds; the taxonomy shrank", len(kinds))
	}
	groups := map[EventGroup]int{}
	for _, k := range kinds {
		if !k.Valid() || k.Group() == "" {
			t.Errorf("%q has no group", k)
		}
		if b := k.BaseMateriality(); b < 1 || b > 5 {
			t.Errorf("%q base materiality %d is outside 1..5", k, b)
		}
		groups[k.Group()]++
	}
	// A classifier with a small option budget must be able to pick a group, then a kind.
	if len(groups) > 15 {
		t.Errorf("%d groups is too many for one question", len(groups))
	}
	for g, n := range groups {
		if n > 15 {
			t.Errorf("group %q has %d kinds, too many for one question", g, n)
		}
		if len(KindsIn(g)) != n {
			t.Errorf("KindsIn(%q) disagrees with the table", g)
		}
	}
	if EventKind("m&a").Valid() || EventKind("").Valid() {
		t.Error("unknown kinds must not be valid")
	}
	if !GroupIntegrity.Valid() || EventGroup("nope").Valid() {
		t.Error("EventGroup.Valid is wrong")
	}
}

func TestIntegrityEventsAreHighMateriality(t *testing.T) {
	for _, k := range []EventKind{EventNonReliance, EventBankruptcy, EventCyber, EventDelisting, EventAuditorChange} {
		if k.Group() != GroupIntegrity || k.BaseMateriality() < 4 {
			t.Errorf("%q: group %q, base %d", k, k.Group(), k.BaseMateriality())
		}
	}
	if EventPriceChatter.BaseMateriality() != 1 {
		t.Error("price chatter is noise, materiality 1")
	}
}

func TestMaterialityLevelsComplete(t *testing.T) {
	for i := 1; i <= 5; i++ {
		if strings.TrimSpace(MaterialityLevels[i]) == "" {
			t.Errorf("level %d has no description", i)
		}
	}
}

func goodSource() Source {
	return Source{
		ID: "sec-edgar", URL: "https://www.sec.gov/x", Kind: KindFilings, Tier: TierPrimary,
		Licence: LicenceRedistributable, Terms: "https://www.sec.gov/about/privacy-information",
	}
}

func TestSourceValidate(t *testing.T) {
	if err := goodSource().Validate(); err != nil {
		t.Fatalf("good source rejected: %v", err)
	}
	s := goodSource()
	s.ID, s.URL, s.Kind, s.Tier, s.Licence, s.Interval = "Bad Id", "http://insecure", "pigeon", 9, "free", "5s"
	err := s.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"id must", "https", "kind", "tier", "licence", "interval"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
	noTerms := goodSource()
	noTerms.Terms = ""
	if noTerms.Validate() == nil {
		t.Error("a redistributable licence without terms must be rejected")
	}
	noOwner := goodSource()
	noOwner.Licence, noOwner.Terms = LicencePersonal, ""
	if noOwner.Validate() == nil {
		t.Error("a personal licence without an owner must be rejected")
	}
}

func TestVisibleTo(t *testing.T) {
	pub := goodSource()
	priv := Source{Licence: LicencePersonal, Owner: "alice"}
	none := Source{}
	if !pub.VisibleTo("anyone") {
		t.Error("redistributable sources are visible to every tenant")
	}
	if !priv.VisibleTo("alice") || priv.VisibleTo("bob") {
		t.Error("a personal source is visible only to its owner")
	}
	if none.VisibleTo("alice") {
		t.Error("a source with no licence must be hidden")
	}
	if (Source{Licence: LicenceInternal}).VisibleTo("") {
		t.Error("an internal source with no owner must be hidden, even from an empty tenant ID")
	}
}
