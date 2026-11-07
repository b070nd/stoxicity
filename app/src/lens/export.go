package lens

import "github.com/b070nd/stoxicity/app/src/model"

// KindMatch is one keyword rule that matches a piece of text.
type KindMatch struct {
	Kind model.EventKind
	// Text is the matched words and At their position in Normalized(text).
	Text string
	At   int
	// Denied says a denial word stands just before the match in its sentence
	// ("no going concern doubt"); it is reported for every kind, but the lens
	// itself only lets it cancel the kinds listed as deniable.
	Denied bool
	// Deniable says the lens treats a denied match of this kind as no event.
	Deniable bool
}

// Normalized is the text as the rules read it: lower case, dashes and quotes
// unified, white space collapsed.
func Normalized(s string) string { return normalize(s) }

// KindMatches returns every keyword rule that matches the text, in the order of
// the rule table, with one match per rule (the first occurrence of its first
// pattern). Unlike Score it does not stop at the first rule: a text that is
// about two things matches both, and the caller decides what that means.
func KindMatches(text string) []KindMatch {
	text = normalize(text)
	var out []KindMatch
	for _, r := range kindRules {
		first := r.all[0]
		for _, loc := range first.FindAllStringIndex(text, -1) {
			ok := true
			for _, re := range r.all[1:] {
				if !re.MatchString(text) {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			out = append(out, KindMatch{Kind: r.kind, Text: text[loc[0]:loc[1]], At: loc[0], Denied: negated(text, loc[0]), Deniable: deniable[r.kind]})
			break
		}
	}
	return out
}

// Magnitude returns the first phrase in the text that makes an event bigger than
// its kind alone (record, plunge, surge, going concern, material weakness,
// significantly) and is not itself denied, or "".
func Magnitude(text string) string { return firstMagnitude(normalize(text)) }

// Routine returns the first phrase that tags the text as expected and
// unremarkable, or "".
func Routine(text string) string { return firstMatch(routineRules, normalize(text)) }

// Noise reports whether the kind is one a long-term investor ignores.
func Noise(k model.EventKind) bool { return noiseKinds[k] }
