package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// root is the module's app folder; the repository root is its parent.
func resolve(t *testing.T, p string) string {
	t.Helper()
	return filepath.Join("..", "..", p) // from app/src/readiness to app/
}

func TestInventoryIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	storyRe := regexp.MustCompile(`^US-\d{3}$`)
	for _, c := range Inventory() {
		if c.ID == "" || c.Name == "" {
			t.Errorf("capability without id or name: %+v", c)
		}
		if seen[c.ID] {
			t.Errorf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		if _, err := ParseScope(string(c.Scope)); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		if c.Story != "" && !storyRe.MatchString(c.Story) {
			t.Errorf("%s: story %q", c.ID, c.Story)
		}
		for _, d := range Dimensions {
			switch c.Level(d) {
			case Yes, No, Unknown, NotApplicable:
			default:
				t.Errorf("%s: %s has level %q", c.ID, d, c.Level(d))
			}
		}
		for d := range c.Levels {
			if !containsDim(d) {
				t.Errorf("%s: unknown dimension %q", c.ID, d)
			}
		}
		// A dimension answered "yes" must cite a path that exists.
		for _, d := range Dimensions {
			if c.Level(d) != Yes {
				if _, has := c.Evidence[d]; has {
					t.Errorf("%s: evidence given for %s, which is %s", c.ID, d, c.Level(d))
				}
				continue
			}
			ev := c.Evidence[d]
			if ev == "" {
				t.Errorf("%s: %s is yes without evidence", c.ID, d)
				continue
			}
			if d == SourceVerified || d == AccountVerified || d == InvestmentEvaluated {
				// These need a dated record, which no path in the tree provides yet.
				t.Errorf("%s: %s is yes, but no verification record format exists", c.ID, d)
				continue
			}
			if _, err := os.Stat(resolve(t, ev)); err != nil {
				t.Errorf("%s: evidence for %s: %v", c.ID, d, err)
			}
		}
		// Later dimensions need the earlier ones: nothing is fixture-verified
		// without being implemented.
		if c.Level(FixtureVerified) == Yes && c.Level(Implemented) != Yes {
			t.Errorf("%s: fixture verified but not implemented", c.ID)
		}
		if c.Level(Implemented) == No {
			for _, d := range []Dimension{FixtureVerified, SourceVerified, AccountVerified, InvestmentEvaluated} {
				if l := c.Level(d); l == Yes {
					t.Errorf("%s: %s is yes while implemented is no", c.ID, d)
				}
			}
			if c.Blocker == "" {
				t.Errorf("%s: a planned capability must say what blocks it", c.ID)
			}
		}
	}
}

func containsDim(d Dimension) bool {
	for _, k := range Dimensions {
		if k == d {
			return true
		}
	}
	return false
}

func TestNothingClaimsMoreThanIsRecorded(t *testing.T) {
	for _, c := range Inventory() {
		for _, d := range []Dimension{SourceVerified, AccountVerified, InvestmentEvaluated} {
			if c.Level(d) == Yes {
				t.Errorf("%s claims %s", c.ID, d)
			}
		}
	}
}

func TestEveryStoryOfThePlanHasACapability(t *testing.T) {
	have := map[string]bool{}
	for _, c := range Inventory() {
		have[c.Story] = true
	}
	for i := 1; i <= 28; i++ {
		id := fmt.Sprintf("US-%03d", i)
		if !have[id] {
			t.Errorf("no capability for %s", id)
		}
	}
}

func TestGapsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range Gaps() {
		if g.ID == "" || g.Summary == "" || g.Source == "" {
			t.Errorf("incomplete gap %+v", g)
		}
		if seen[g.ID] {
			t.Errorf("duplicate gap %s", g.ID)
		}
		seen[g.ID] = true
	}
}

func TestSummaryKeepsDimensionsApart(t *testing.T) {
	r := Build("test", Portfolio, nil, Schema{})
	if r.Summary.Total != len(r.Capabilities) || r.Summary.Total == 0 {
		t.Fatalf("summary %+v", r.Summary)
	}
	for _, d := range Dimensions {
		n := 0
		for _, c := range r.Summary.Counts[d] {
			n += c
		}
		if n != r.Summary.Total {
			t.Errorf("%s counts add up to %d of %d", d, n, r.Summary.Total)
		}
	}
	for _, c := range r.Capabilities {
		if c.Scope != Portfolio {
			t.Errorf("%s is out of scope", c.ID)
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema":"stoxicity.doctor/1"`, `"fixture_verified"`, `"investment_evaluated"`, `"known_base"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON lacks %s", key)
		}
	}
}

func TestParseScope(t *testing.T) {
	for _, s := range []string{"research", "portfolio", "broker"} {
		if _, err := ParseScope(s); err != nil {
			t.Error(err)
		}
	}
	if _, err := ParseScope("all"); err == nil {
		t.Error("all is not a scope")
	}
}

func TestReferenceCatalogueIsDeterministic(t *testing.T) {
	a, b := ReferenceCatalogue(7, 12000), ReferenceCatalogue(7, 12000)
	if len(a) != 12000 {
		t.Fatal(len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("row %d differs", i)
		}
	}
	if c := ReferenceCatalogue(8, 10); c[0] == a[0] && c[1] == a[1] {
		t.Error("another seed gave the same rows")
	}
	tick := map[string]bool{}
	for _, s := range a {
		if tick[s.Ticker] {
			t.Fatalf("duplicate ticker %s", s.Ticker)
		}
		tick[s.Ticker] = true
	}
}

func TestSummarizeSamples(t *testing.T) {
	var s []time.Duration
	for i := 100; i >= 1; i-- {
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	m, err := SummarizeSamples("x", 100*time.Millisecond, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.P50 != 50*time.Millisecond || m.P95 != 95*time.Millisecond || m.Max != 100*time.Millisecond || !m.Met() {
		t.Errorf("%+v", m)
	}
	m.Target = 90 * time.Millisecond
	if m.Met() {
		t.Error("p95 above the target must not be met")
	}
	if _, err := SummarizeSamples("x", 0, nil); err == nil {
		t.Error("no samples")
	}
	if CurrentMachine().CPUs < 1 {
		t.Error("machine")
	}
}
