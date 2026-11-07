package pipeline

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

func scored(id, cluster, title string, mat int, tickers ...string) model.Article {
	return model.Article{
		ID: id, SourceID: "wire", ClusterID: cluster, Title: title, Tickers: tickers,
		Published: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
		Score:     model.Score{Kind: model.EventAcquisition, Materiality: mat, Relevance: 0.8, LensVersion: "l1"},
	}
}

func TestSyndicatedCopiesAreOneEventWithAllTheEvidence(t *testing.T) {
	a := scored("a1", "c1", "Acme to buy Beta", 4, "ACME", "BETA")
	b := scored("a2", "c1", "Acme agrees deal for Beta", 4, "ACME", "BETA")
	b.Published = a.Published.Add(-time.Hour)
	got := Events([]model.Article{a, b})
	if len(got) != 1 || len(got[0].Evidence) != 2 || got[0].EventID != "evt_c1" {
		t.Fatalf("events = %+v, want one with both articles as evidence", got)
	}
	if !got[0].PublicAt.Equal(b.Published) {
		t.Errorf("public at %v, want the earliest evidence %v", got[0].PublicAt, b.Published)
	}
}

func TestRolesComeFromWhatTheEvidenceShows(t *testing.T) {
	footer := scored("a1", "", "Quarterly roundup of earnings", 4, "ACME", "XYZ", "AAA")
	if RoleOf(footer, "ACME") != model.RoleMentioned {
		t.Error("a ticker not in the title of a roundup is only mentioned")
	}
	titled := scored("a2", "", "XYZ wins contract", 4, "ACME", "XYZ")
	if RoleOf(titled, "XYZ") != model.RoleSubject || RoleOf(titled, "ACME") != model.RoleMentioned {
		t.Error("the company in the title is the subject, the other only mentioned")
	}
	filing := scored("a3", "", "8-K - Acme Corp", 4, "ACME", "ACMEB")
	filing.CIK = "0000000001"
	if RoleOf(filing, "ACMEB") != model.RoleSubject {
		t.Error("every security of the filer is the subject of its filing")
	}
	if RoleOf(scored("a4", "", "Acme to buy", 4, "ACME"), "ACME") != model.RoleSubject {
		t.Error("the only company named is the subject")
	}
}

func TestNoiseIsNotAnEvent(t *testing.T) {
	quiet := scored("a1", "", "Some chatter", 1, "ACME")
	quiet.Score.Kind = model.EventOther
	if got := Events([]model.Article{quiet}); len(got) != 0 {
		t.Errorf("events = %+v, want none for an article of no kind and little weight", got)
	}
	unscored := scored("a2", "", "x", 4, "ACME")
	unscored.Score = model.Score{}
	if got := Events([]model.Article{unscored}); len(got) != 0 {
		t.Errorf("events = %+v, want none for an unscored article", got)
	}
}
