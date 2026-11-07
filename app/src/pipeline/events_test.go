package pipeline

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/assertion"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/store"
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

func claimed(t *testing.T, arts ...model.Article) (map[string]assertion.Result, []store.EventInput) {
	t.Helper()
	names := func(tk string) []string {
		return map[string][]string{"ACME": {"Acme Corp"}, "BOLT": {"Bolt Industries"}, "ZETA": {"Zeta Ltd"}}[tk]
	}
	claims := map[string]assertion.Result{}
	for _, a := range arts {
		claims[a.ID] = Claims(a, names, nil)
	}
	return claims, EventsFrom(arts, claims)
}

func text(id, title, summary string, tickers ...string) model.Article {
	a := scored(id, "", title, 3, tickers...)
	a.Summary = summary
	a.Score = model.Score{Kind: model.EventDividend, Materiality: 3, Relevance: 0.8, LensVersion: "l1"} // the first keyword of the lens
	return a
}

func TestAStoryThatSaysTwoThingsIsNamedAfterTheMoreImportantClaimWithRoles(t *testing.T) {
	a := text("a1", "Acme Corp raises dividend and agrees to acquire Bolt Industries", "", "ACME", "BOLT", "ZETA")
	_, got := claimed(t, a)
	if len(got) != 1 || got[0].Kind != model.EventAcquisition {
		t.Fatalf("the lens's first keyword said dividend; the claims say acquisition: %+v", got)
	}
	roles := map[string]model.Role{}
	for _, s := range got[0].Securities {
		roles[s.Ticker] = s.Role
	}
	if roles["ACME"] != model.RoleSubject || roles["BOLT"] != model.RoleCounterparty || roles["ZETA"] != model.RoleMentioned {
		t.Errorf("roles %v", roles)
	}
	if !strings.HasSuffix(got[0].LensVersion, "+"+assertion.Version) {
		t.Errorf("the revision records which reading made it: %s", got[0].LensVersion)
	}
}

func TestDenialsPreviewsAndHistoryAreNoEvents(t *testing.T) {
	for _, title := range []string{
		"Acme Corp denies bankruptcy rumours",
		"Acme Corp says there is no restatement of its accounts",
		"Acme Corp settled a lawsuit in 2019",
	} {
		a := text("a1", title, "", "ACME")
		a.Score.Kind = model.EventBankruptcy
		if _, got := claimed(t, a); len(got) != 0 {
			t.Errorf("%q gave events %+v", title, got)
		}
	}
	// ... but the same words as a current claim are one.
	a := text("a2", "Acme Corp files for chapter 11 bankruptcy", "", "ACME")
	if _, got := claimed(t, a); len(got) != 1 || got[0].Kind != model.EventBankruptcy {
		t.Errorf("%+v", got)
	}
}

func TestAnIncidentalTickerGetsNoEvent(t *testing.T) {
	a := text("a1", "Acme Corp agrees to acquire Bolt Industries", "Stocks to watch also include Zeta Ltd.", "ACME", "BOLT", "ZETA")
	_, got := claimed(t, a)
	for _, s := range got[0].Securities {
		if s.Ticker == "ZETA" && s.Role != model.RoleMentioned {
			t.Errorf("Zeta is only mentioned: %+v", s)
		}
	}
}

func TestAssessmentsAreSeparateReadingsAndKnowWhatCameBefore(t *testing.T) {
	a := text("a1", "Acme Corp completed its acquisition of Bolt Industries", "", "ACME", "BOLT")
	b := text("a2", "Acme Corp completed its acquisition of Bolt Industries", "", "ACME", "BOLT")
	b.Published = a.Published.Add(time.Hour)
	a.ClusterID, b.ClusterID = "c1", "c1"
	claims, _ := claimed(t, a, b)
	results, as := Assessments([]model.Article{a, b}, claims, nil, b.Published.Add(time.Hour))
	if len(results) != 2 {
		t.Fatalf("%d", len(results))
	}
	nov := map[string]string{}
	for _, x := range as {
		if x.Ticker == "ACME" && x.Dimension == assertion.DimNovelty {
			nov[x.ArticleID] = x.Label
		}
		if x.Dimension == assertion.DimPriceImpact && !x.Abstained {
			t.Errorf("price impact is never read from this evidence: %+v", x)
		}
	}
	if nov["a1"] != "new" || nov["a2"] != "repeat" {
		t.Errorf("the first report is new and the second a repeat: %v", nov)
	}
}

func TestTextWithoutClaimsKeepsTheLensReading(t *testing.T) {
	a := scored("a1", "", "8-K - Acme Corp", 4, "ACME")
	if got := EventsFrom([]model.Article{a}, map[string]assertion.Result{}); len(got) != 1 {
		t.Errorf("an article the claims were not read for is read as before: %+v", got)
	}
}
