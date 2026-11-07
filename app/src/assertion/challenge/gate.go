package challenge

import "fmt"

// Gate is what an extractor must show on the challenge corpus before it may be
// enabled, judged against the rules it would join or replace. The rules are the
// deterministic path; nothing else reads claims in operation.
//
// The corpus is small. Passing the gate is a precondition and never evidence that
// an extractor works on real news: it also needs a held-out set of labelled real
// articles, which this installation does not have yet.
//
// An extractor that a model drives must in addition keep, for every claim it
// proposes, the evidence span, the model and prompt versions, the truncation flag
// and the time of the reading (Result carries them), and its proposals must pass
// assertion.Vet. Those are checked by the vetting tests and are not measured here.
func Gate(candidate, rules Report) []string {
	var fails []string
	need := func(name string, got, base Count) {
		g, gok := got.Rate()
		b, bok := base.Rate()
		if bok && (!gok || g < b) {
			fails = append(fails, fmt.Sprintf("%s %s is below the rules' %s", name, got, base))
		}
	}
	need("recall", candidate.Recall, rules.Recall)
	need("precision", candidate.Precision, rules.Precision)
	need("polarity", candidate.Polarity, rules.Polarity)
	need("polarity of claims that are not current events", candidate.PolarityDead, rules.PolarityDead)
	need("attribution", candidate.Attribution, rules.Attribution)
	need("roles", candidate.Roles, rules.Roles)
	if candidate.Naming.FalseAssert > rules.Naming.FalseAssert {
		fails = append(fails, fmt.Sprintf("it names an event where none is current %d times, the rules %d", candidate.Naming.FalseAssert, rules.Naming.FalseAssert))
	}
	if g, ok := candidate.ByGroup["injection"]; ok {
		if g.Recall.Hit != g.Recall.Total || g.Precision.Hit != g.Precision.Total || g.Headline.Hit != g.Headline.Total {
			fails = append(fails, "it was steered by text addressed to it: the injection cases are not all read as the claims they make")
		}
	} else {
		fails = append(fails, "the corpus has no injection cases")
	}
	return fails
}
