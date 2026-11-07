package store

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/model"
)

func claimResult(id string) assertion.Result {
	return assertion.Result{ArticleID: id, Version: assertion.Version, Extractor: assertion.ExtractorRules, Assertions: []assertion.Assertion{{
		Seq: 1, Kind: model.EventAcquisition, Method: assertion.MethodText, Span: "Acme agreed to acquire Bolt", Start: 0, End: 27, Match: "acquire",
		Parties: []assertion.Party{{Ticker: "ACME", Role: assertion.Subject, Basis: "named"}, {Ticker: "BOLT", Role: assertion.Counterparty, Basis: "named"}},
		Status:  assertion.Announced, Attribution: assertion.OwnStatement, Magnitude: "record", Denied: true,
	}}}
}

func TestAssertionsRoundTripAreAppendedOnceAndReadAsOfATime(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if n, err := db.PutAssertions(ctx, []assertion.Result{claimResult("a1")}, t0); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := db.PutAssertions(ctx, []assertion.Result{claimResult("a1")}, t0.Add(time.Hour)); n != 0 {
		t.Errorf("the same claim read again is not written again: %d", n)
	}
	got, err := db.Assertions(ctx, []string{"a1", "zz"}, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	a := got["a1"].Assertions[0]
	if a.Kind != model.EventAcquisition || len(a.Parties) != 2 || a.Parties[1].Role != assertion.Counterparty || !a.Denied || a.Status != assertion.Announced ||
		a.Attribution != assertion.OwnStatement || a.Magnitude != "record" || a.Span != "Acme agreed to acquire Bolt" || got["a1"].Extractor != assertion.ExtractorRules {
		t.Errorf("%+v", got["a1"])
	}
	if early, _ := db.Assertions(ctx, []string{"a1"}, t0.Add(-time.Minute)); len(early) != 0 {
		t.Errorf("a claim recorded later is not known earlier: %v", early)
	}
	// A model's claims are kept beside the rules', with the model, prompt and truncation.
	m := claimResult("a1").ProposedBy("model-x", "prompt-2", true)
	if n, _ := db.PutAssertions(ctx, []assertion.Result{m}, t0.Add(2*time.Hour)); n != 1 {
		t.Fatalf("a different extractor is another set of claims: %d", n)
	}
}

func TestAssessmentsAreReadingsAppendedOnlyWhenTheyChange(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	mat := func(v int, label string, abst bool) assertion.Assessment {
		return assertion.Assessment{ArticleID: "a1", Ticker: "ACME", Dimension: assertion.DimMateriality, Version: assertion.AssessVersion, Value: v, Label: label, Abstained: abst, Basis: "why", Seqs: []int{1}}
	}
	pi := assertion.Assessment{ArticleID: "a1", Ticker: "ACME", Dimension: assertion.DimPriceImpact, Version: assertion.AssessVersion, Abstained: true, Basis: "no model"}
	if n, err := db.PutAssessments(ctx, []assertion.Assessment{mat(3, "acquisition", false), pi}, t0); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := db.PutAssessments(ctx, []assertion.Assessment{mat(3, "acquisition", false), pi}, t0.Add(time.Hour)); n != 0 {
		t.Errorf("the same reading is not stored again: %d", n)
	}
	if n, _ := db.PutAssessments(ctx, []assertion.Assessment{mat(4, "acquisition", false)}, t0.Add(2*time.Hour)); n != 1 {
		t.Fatal("a changed reading is appended")
	}
	now, _ := db.Assessments(ctx, "a1", time.Time{})
	was, _ := db.Assessments(ctx, "a1", t0.Add(time.Hour))
	none, _ := db.Assessments(ctx, "a1", t0.Add(-time.Hour))
	value := func(as []assertion.Assessment) int {
		for _, a := range as {
			if a.Dimension == assertion.DimMateriality {
				return a.Value
			}
		}
		return -1
	}
	if value(now) != 4 || value(was) != 3 || len(none) != 0 {
		t.Errorf("a reading made after a cutoff is not in force before it: now %d was %d none %d", value(now), value(was), len(none))
	}
	var sawPI bool
	for _, a := range now {
		if a.Dimension == assertion.DimPriceImpact {
			sawPI = a.Abstained && a.Basis == "no model"
		}
	}
	if !sawPI {
		t.Error("an abstention is stored with its reason")
	}
}
