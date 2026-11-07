// Synthetic fixture, not verified against live data.

package summarize

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

var base = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

// article is a relevant article with one title, published day days after base.
func article(id, cluster string, day, materiality int, title string) model.Article {
	return model.Article{
		ID:        id,
		ClusterID: cluster,
		Title:     title,
		Published: base.AddDate(0, 0, day),
		Score:     model.Score{Relevance: 0.5, Materiality: materiality},
	}
}

func texts(lines []Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

func TestSentences(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"corporate suffix does not split", "Apple Inc. Shares rose.", []string{"Apple Inc. Shares rose."}},
		{"multi-part abbreviation does not split", "The U.S. Commerce Department acted. Next came the vote.",
			[]string{"The U.S. Commerce Department acted.", "Next came the vote."}},
		{"titles do not split", "Mr. Smith spoke. Dr. Jones agreed.",
			[]string{"Mr. Smith spoke.", "Dr. Jones agreed."}},
		{"single capital letter does not split", "Plan J. Smith signed it. Done today.",
			[]string{"Plan J. Smith signed it.", "Done today."}},
		{"No. does not split", "Case No. 5 is closed. Next.",
			[]string{"Case No. 5 is closed.", "Next."}},
		{"decimal point does not split", "Revenue rose 3.5 percent. Guidance held.",
			[]string{"Revenue rose 3.5 percent.", "Guidance held."}},
		{"question and exclamation split only before a capital", "Is it over? Yes! it is done",
			[]string{"Is it over?", "Yes! it is done"}},
		{"a run of abbreviations stays one sentence", "Acme Corp. Ltd. Co. Inc. Sales rose.",
			[]string{"Acme Corp. Ltd. Co. Inc. Sales rose."}},
		{"whitespace is collapsed", "First\n  line.  Second   one.",
			[]string{"First line.", "Second one."}},
		{"a digit after the break starts a sentence", "It rose 4. 2026 looks better.",
			[]string{"It rose 4.", "2026 looks better."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sentences(c.in); !reflect.DeepEqual(got, c.want) {
				t.Errorf("sentences(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSummarizeLengthBounds(t *testing.T) {
	cases := []struct {
		length int
		want   int
	}{
		{19, 0},
		{20, 1},
		{300, 1},
		{301, 0},
	}
	for _, c := range cases {
		a := article("a", "", 0, 3, strings.Repeat("a", c.length))
		if got := Summarize([]model.Article{a}, 5); len(got.Lines) != c.want {
			t.Errorf("length %d: got %d lines, want %d", c.length, len(got.Lines), c.want)
		}
	}
}

func TestSummarizeTitleAndSummaryBothContribute(t *testing.T) {
	a := article("a", "", 0, 3, "Lumen raised its buyback to two billion dollars.")
	a.Summary = "Analysts had expected a smaller increase this year."
	got := Summarize([]model.Article{a}, 5)
	want := []string{"Lumen raised its buyback to two billion dollars.", "Analysts had expected a smaller increase this year."}
	if !reflect.DeepEqual(texts(got.Lines), want) {
		t.Fatalf("lines = %q, want %q", texts(got.Lines), want)
	}
	if got.Lines[0].Article.ID != "a" {
		t.Errorf("source = %q, want a", got.Lines[0].Article.ID)
	}
}

func TestSummarizeNearDuplicateThreshold(t *testing.T) {
	const first = "alpha bravo charlie delta echo"
	cases := []struct {
		name, second string
		want         int
	}{
		{"identical words are dropped", "alpha bravo charlie delta echo", 1},
		// Shares 3 of 5 distinct words: Jaccard 0.6, which is dropped.
		{"exactly at the threshold is dropped", "alpha bravo charlie.", 1},
		// Shares 2 of 8 distinct words: Jaccard 0.25, which is kept.
		{"below the threshold is kept", "alpha bravo kilo lima mike.", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			arts := []model.Article{
				article("a1", "", 0, 4, first),
				article("a2", "", 1, 2, c.second),
			}
			got := Summarize(arts, 5)
			if len(got.Lines) != c.want {
				t.Fatalf("lines = %q, want %d", texts(got.Lines), c.want)
			}
			if got.Lines[0].Text != first {
				t.Errorf("kept %q, want the higher-scoring %q", got.Lines[0].Text, first)
			}
		})
	}
}

func TestSummarizeOneLinePerCluster(t *testing.T) {
	arts := []model.Article{
		article("a1", "c1", 0, 5, "Lumen raised its buyback authorisation to two billion dollars."),
		article("a2", "c1", 1, 3, "Analysts expected a smaller increase in the buyback this year."),
		article("a3", "", 0, 1, "Pioneer Bank agreed to acquire a regional lender for cash."),
	}
	got := Summarize(arts, 5)
	want := []string{
		"Lumen raised its buyback authorisation to two billion dollars.",
		"Pioneer Bank agreed to acquire a regional lender for cash.",
	}
	if !reflect.DeepEqual(texts(got.Lines), want) {
		t.Fatalf("lines = %q, want %q", texts(got.Lines), want)
	}
}

func TestSummarizeScoreOrderAndMaxLines(t *testing.T) {
	arts := []model.Article{
		article("low", "", 0, 1, "Pioneer Bank agreed to acquire a regional lender for cash."),
		article("mid", "", 1, 3, "Verdant Energy named a new chief financial officer effective January."),
		article("high", "", 2, 5, "Quartz Motors recalled forty thousand vehicles over brake software."),
	}
	got := Summarize(arts, 2)
	if len(got.Lines) != 2 || got.Lines[0].Article.ID != "high" || got.Lines[1].Article.ID != "mid" {
		t.Fatalf("lines = %q, want high then mid", texts(got.Lines))
	}
}

func TestSummarizeTiesGoToEarlierArticle(t *testing.T) {
	// X and Y score the same. Z is in the newest third, so it gets the recency
	// bonus, but its lower materiality keeps it below both.
	x := article("x", "", 0, 3, "Lumen Corp raised its buyback authorisation to two billion dollars.")
	y := article("y", "", 1, 3, "Pioneer Bank agreed to acquire a regional lender for cash.")
	z := article("z", "", 2, 1, "Solaris Labs delayed its phase three trial results into next year.")
	got := Summarize([]model.Article{y, z, x}, 2)
	if len(got.Lines) != 2 || got.Lines[0].Article.ID != "x" || got.Lines[1].Article.ID != "y" {
		t.Fatalf("lines = %q, want x then y", texts(got.Lines))
	}
}

func TestSummarizeMaxLinesBelowOneReturnsNothing(t *testing.T) {
	arts := []model.Article{article("a", "", 0, 3, "Lumen raised its buyback to two billion dollars.")}
	for _, n := range []int{0, -1} {
		if got := Summarize(arts, n); len(got.Lines) != 0 {
			t.Errorf("Summarize(maxLines %d) = %q, want none", n, texts(got.Lines))
		}
	}
}

func TestSummarizeEmpty(t *testing.T) {
	if got := Summarize(nil, 5); len(got.Lines) != 0 {
		t.Fatalf("Summarize(nil) = %q, want none", texts(got.Lines))
	}
}

func TestACorrectionIsNotDroppedAsARepeatOfTheClaimItCorrects(t *testing.T) {
	arts := []model.Article{
		article("a", "c1", 0, 5, "Acme will acquire Beta for cash this year."),
		article("b", "c1", 1, 3, "Acme will not acquire Beta for cash this year."),
		article("c", "c2", 0, 2, "Acme will pay 4 billion dollars for Beta this year."),
		article("d", "c3", 1, 1, "Acme will pay 5 billion dollars for Beta this year."),
	}
	got := texts(Summarize(arts, 10).Lines)
	for _, want := range []string{"Acme will acquire Beta for cash this year.", "Acme will not acquire Beta for cash this year."} {
		if !contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if !contains(got, "Acme will pay 4 billion dollars for Beta this year.") || !contains(got, "Acme will pay 5 billion dollars for Beta this year.") {
		t.Errorf("a changed number was treated as a repeat: %q", got)
	}
}

func TestARepeatThatClaimsTheSameStillGoes(t *testing.T) {
	arts := []model.Article{
		article("a", "c1", 0, 5, "Acme will acquire Beta for cash this year."),
		article("b", "c1", 1, 3, "Acme will acquire Beta for cash this year!"),
		article("c", "", 1, 3, "Acme to acquire Beta for cash this year."),
	}
	if got := texts(Summarize(arts, 10).Lines); len(got) != 1 {
		t.Errorf("repeats kept: %q", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
