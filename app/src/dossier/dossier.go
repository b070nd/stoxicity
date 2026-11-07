// Package dossier turns the event history into things a person can read and
// check: Markdown files of events and securities that can live in Git, and the
// small knowledge graph around a security that has a strong signal.
//
// Everything here is a pure function of data handed in: no clock, no store, no
// network. The same history always gives the same text, so a file in Git only
// changes when the history did.
package dossier

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/origin"
	"github.com/b070nd/stoxicity/app/src/store"
)

// stamp writes a time the same way everywhere.
func stamp(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// line makes text safe for one line of a Markdown table cell or heading.
func line(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", "\\|")
}

// EventFile is the file name of an event's dossier.
func EventFile(eventID string) string { return eventID + ".md" }

// SecurityFile is the file name of a security's dossier.
func SecurityFile(securityID string) string { return securityID + ".md" }

// EventMarkdown writes the dossier of one event: what is understood now, the
// evidence, the observed roles of the securities, the assessments (which are
// interpretations, kept apart) and every revision that led here. history is
// oldest first and must not be empty; articles gives the evidence by ID, and an
// article that is missing is listed by its ID alone. origins gives the stored
// reporting origin of each article; an article without one is shown as not yet
// assessed rather than guessed.
func EventMarkdown(history []model.EventRevision, articles map[string]model.Article, origins map[string]origin.Origin) string {
	cur := history[len(history)-1]
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", line(cur.Title))
	fmt.Fprintf(&b, "- event: `%s`\n- kind: %s, status: %s, revision %d of %d\n", cur.EventID, cur.Kind, cur.Status, cur.Revision, len(history))
	if cur.EffectiveDay != "" {
		fmt.Fprintf(&b, "- effective: %s\n", cur.EffectiveDay)
	}
	fmt.Fprintf(&b, "- public: %s (the earliest evidence)\n- recorded: %s (when this system committed revision %d)\n", stamp(cur.PublicAt), stamp(cur.RecordedAt), cur.Revision)
	fmt.Fprintf(&b, "- materiality %d, relevance %.2f, by `%s`\n", cur.Materiality, cur.Relevance, cur.LensVersion)
	if len(cur.Topics) > 0 {
		fmt.Fprintf(&b, "- topics: %s\n", strings.Join(cur.Topics, ", "))
	}

	b.WriteString("\n## Securities (observed relationship)\n\n| role | ticker | security |\n|---|---|---|\n")
	secs := slices.Clone(cur.Securities)
	slices.SortFunc(secs, func(x, y model.EventSecurity) int {
		return cmp.Or(cmp.Compare(y.Role.Strength(), x.Role.Strength()), cmp.Compare(x.SecurityID, y.SecurityID))
	})
	for _, s := range secs {
		fmt.Fprintf(&b, "| %s | %s | [%s](../securities/%s) |\n", s.Role, s.Ticker, s.SecurityID, SecurityFile(s.SecurityID))
	}

	b.WriteString("\n## Evidence\n\n")
	domains, keys := map[string]bool{}, map[string]bool{}
	for _, ev := range cur.Evidence {
		a, ok := articles[ev.ArticleID]
		if !ok {
			fmt.Fprintf(&b, "- `%s` (%s), article not stored\n", ev.ArticleID, ev.SourceID)
			continue
		}
		domains[a.Domain] = true
		title := line(a.Title)
		if a.URL != "" {
			title = fmt.Sprintf("[%s](%s)", title, a.URL)
		}
		fmt.Fprintf(&b, "- %s, %s (%s), published %s", title, a.Domain, ev.SourceID, stamp(a.Published))
		if o, has := origins[ev.ArticleID]; has {
			keys[o.Key] = true
			fmt.Fprintf(&b, "; origin %s (%s)", o.Kind, o.Key)
			if o.DerivedFrom != "" {
				fmt.Fprintf(&b, " resting on %s", o.DerivedFrom)
			}
			if o.CopyOf != "" {
				fmt.Fprintf(&b, ", copy of `%s`", o.CopyOf)
			}
			fmt.Fprintf(&b, ", found by %s", o.Method)
		} else {
			b.WriteString("; origin not assessed")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%d observations from %d publishers and %d reporting origins (copies counted once; articles without an assessed origin are not counted).\n", len(cur.Evidence), len(domains), len(keys))

	b.WriteString("\n## Assessments (interpretation, not observation)\n\n")
	if len(cur.Assessments) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| security | effect | direction | horizon | confidence | rule | rationale |\n|---|---|---|---|---|---|---|\n")
		for _, a := range cur.Assessments {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.2f | %s | %s |\n", a.SecurityID, a.Effect, a.Direction, a.Horizon, a.Confidence, a.Rule, line(a.Rationale))
		}
	}

	b.WriteString("\n## Revisions\n\n| rev | status | recorded | public | materiality | evidence | title |\n|---|---|---|---|---|---|---|\n")
	for _, r := range history {
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %d | %d | %s |\n", r.Revision, r.Status, stamp(r.RecordedAt), stamp(r.PublicAt), r.Materiality, len(r.Evidence), line(r.Title))
	}
	return b.String()
}

// SecurityMarkdown writes the dossier of one security: who it is, facts about it
// that the caller gathered (each already a sentence), and its events newest
// first, each linked to its own file.
func SecurityMarkdown(sec store.SecurityInfo, facts []string, timeline []store.TimelineEntry) string {
	var b strings.Builder
	title := sec.Ticker
	if sec.Name != "" {
		title += " - " + sec.Name
	}
	fmt.Fprintf(&b, "# %s\n\n- security: `%s`\n", line(title), sec.ID)
	if sec.CIK != "" {
		fmt.Fprintf(&b, "- SEC CIK: %s\n", sec.CIK)
	}
	if sec.Class != "" {
		fmt.Fprintf(&b, "- class: %s\n", sec.Class)
	}
	if len(facts) > 0 {
		b.WriteString("\n## Facts\n\n")
		for _, f := range facts {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	b.WriteString("\n## Events\n\n| public | materiality | role | kind | status | rev | evidence | event |\n|---|---|---|---|---|---|---|---|\n")
	for _, e := range timeline {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %d | %d | [%s](../events/%s) |\n", stamp(e.PublicAt), e.Materiality, e.Role, e.Kind,
			e.Status, e.Revision, e.Evidence, line(e.Title), EventFile(e.EventID))
	}
	if len(timeline) == 0 {
		b.WriteString("| none | | | | | | | |\n")
	}
	return b.String()
}
