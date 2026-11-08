package instrument

// Counts says how many records of one kind an import added, changed or found
// already stored.
type Counts struct {
	New       int `json:"new"`
	Revised   int `json:"revised"`
	Unchanged int `json:"unchanged"`
}

// ImportReport is the outcome of an import.
type ImportReport struct {
	Issuers    Counts    `json:"issuers"`
	Securities Counts    `json:"securities"`
	Listings   Counts    `json:"listings"`
	Linked     []string  `json:"linked,omitempty"` // handles attached to existing securities
	Problems   []Problem `json:"problems"`         // warnings; errors stop the import
}

// Merge returns existing with every record of incoming replacing the record of
// the same ID, and new ones added. Nothing in existing is removed: an import
// that leaves a record out does not delete it.
func Merge(existing, incoming Set) Set {
	out := Set{}
	issuers := map[string]int{}
	for _, i := range existing.Issuers {
		issuers[i.ID] = len(out.Issuers)
		out.Issuers = append(out.Issuers, i)
	}
	for _, i := range incoming.Issuers {
		if at, ok := issuers[i.ID]; ok {
			out.Issuers[at] = i
		} else {
			out.Issuers = append(out.Issuers, i)
		}
	}
	secs := map[string]int{}
	for _, s := range existing.Securities {
		secs[s.ID] = len(out.Securities)
		out.Securities = append(out.Securities, s)
	}
	for _, s := range incoming.Securities {
		if at, ok := secs[s.ID]; ok {
			out.Securities[at] = s
		} else {
			out.Securities = append(out.Securities, s)
		}
	}
	existing, incoming = Normalize(existing), Normalize(incoming)
	lst := map[string]int{}
	for _, l := range existing.Listings {
		lst[l.ID] = len(out.Listings)
		out.Listings = append(out.Listings, l)
	}
	for _, l := range incoming.Listings {
		if at, ok := lst[l.ID]; ok {
			out.Listings[at] = l
		} else {
			out.Listings = append(out.Listings, l)
		}
	}
	return out
}
