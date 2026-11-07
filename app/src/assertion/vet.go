package assertion

import (
	"fmt"
	"strings"

	"github.com/b070nd/stoxicity/app/src/lens"
)

// Rejection is a proposed assertion that was not kept, with why.
type Rejection struct {
	Assertion Assertion
	Reason    string
}

// Vet checks claims that something other than the rules proposed (a language
// model, say) against the text they claim to be read from. A claim is kept only
// when its evidence span occurs verbatim in the text at the offsets it names,
// when its kind is in the taxonomy, and when the span itself contains words that
// give that kind; whatever the proposer says about materiality or ratings is not
// taken at all (Rated is cleared): those are readings, made by Assess. A text
// that tells its reader what to conclude therefore changes nothing: the model
// may be fooled into proposing, but a proposal the words do not support is not
// kept.
func Vet(text string, proposed []Assertion) (kept []Assertion, rejected []Rejection) {
	for _, a := range proposed {
		reject := func(format string, args ...any) {
			rejected = append(rejected, Rejection{a, fmt.Sprintf(format, args...)})
		}
		switch {
		case !a.Kind.Valid():
			reject("kind %q is not in the taxonomy", a.Kind)
		case a.Span == "" || a.Start < 0 || a.End > len(text) || a.Start >= a.End || text[a.Start:a.End] != a.Span:
			reject("the evidence span is not in the text at offsets %d to %d", a.Start, a.End)
		case !spanGives(a):
			reject("the evidence span has no words that give the kind %s", a.Kind)
		default:
			a.Rated = 0
			a.Method = "model"
			kept = append(kept, a)
		}
	}
	for i := range kept {
		kept[i].Seq = i + 1
	}
	return kept, rejected
}

func spanGives(a Assertion) bool {
	for _, m := range lens.KindMatches(a.Span) {
		if m.Kind == a.Kind {
			return true
		}
	}
	return false
}

// ProposedBy returns the result marked as proposed by the model and prompt, after Vet
// has kept what the text supports. truncated says the text was cut before the
// model saw it, which a reader of the assertions must know: a claim made on half
// a document is not a claim about the document.
func (r Result) ProposedBy(model, promptVersion string, truncated bool) Result {
	r.Extractor = strings.TrimSpace(model)
	r.Model, r.PromptVersion, r.Truncated = model, promptVersion, truncated
	return r
}
