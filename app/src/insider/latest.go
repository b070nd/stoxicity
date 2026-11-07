package insider

import (
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Latest returns the trades as the forms stand after their amendments. A Form
// 4/A restates the form it amends, and says which period and owner it is for;
// it does not name the form. So for each company, owner and period the trades
// of the newest amendment replace those of every form filed before it (and of
// the older amendments), while a form filed after it is a new one and stays.
// Trades whose period or owner is unknown are kept as they are. The order of
// the trades is kept.
func Latest(trades []model.InsiderTrade) []model.InsiderTrade {
	type key struct{ cik, owner, period string }
	keyOf := func(t model.InsiderTrade) (key, bool) {
		owner := t.OwnerCIK
		if owner == "" || t.Period == "" {
			return key{}, false
		}
		return key{t.CIK, owner, t.Period}, true
	}
	newest := map[key]model.InsiderTrade{} // the newest amendment's trade, for its accession and day
	for _, t := range trades {
		k, ok := keyOf(t)
		if !ok || strings.ToUpper(t.Form) != "4/A" {
			continue
		}
		if cur, has := newest[k]; !has || t.Filed > cur.Filed || t.Filed == cur.Filed && t.Accession > cur.Accession {
			newest[k] = t
		}
	}
	if len(newest) == 0 {
		return trades
	}
	out := make([]model.InsiderTrade, 0, len(trades))
	for _, t := range trades {
		k, ok := keyOf(t)
		if am, has := newest[k]; ok && has && t.Accession != am.Accession && !(t.Filed > am.Filed && strings.ToUpper(t.Form) != "4/A") {
			continue // replaced by the amendment
		}
		out = append(out, t)
	}
	return out
}
