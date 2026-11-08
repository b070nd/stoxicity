package policy

import (
	"fmt"
	"slices"

	"github.com/b070nd/stoxicity/app/src/money"
)

// Feasibility checks the necessary conditions for any allocation to exist under
// the policy. It cannot prove that one does; the allocator does that with real
// instruments. What it reports is what no instrument could fix, with the
// numbers, and it never relaxes a constraint to make the policy pass. The
// findings are warnings: a policy that can only hold cash is legal, but the
// user should be told before a plan says so.
func Feasibility(p Policy) []Problem {
	var out []Problem
	add := func(path, format string, args ...any) {
		out = append(out, Problem{Warning, path, "infeasible: " + fmt.Sprintf(format, args...)})
	}
	inv, known := p.Finances.Investable.Get()
	if !known {
		return nil // nothing to allocate yet; the unknown is reported on its own
	}
	if !inv.Valid() || inv.Currency() != p.BaseCurrency || inv.Sign() == 0 {
		return nil // the amount's own error, or nothing to place
	}
	k := p.Constraints
	funds := slices.Contains(p.Preferences.Instruments, "fund")
	stocks := slices.Contains(p.Preferences.Instruments, "stock")
	if !funds && !stocks {
		return nil
	}
	invD := inv.Decimal()
	cur := p.BaseCurrency
	money2 := func(d money.Decimal) string {
		a, err := money.NewAmount(d, cur, money.Down)
		if err != nil {
			return d.String()
		}
		return a.String()
	}
	frac := func(f money.Decimal) (money.Decimal, error) { return invD.Mul(f, 2, money.Down) }

	keep, err := frac(p.Finances.CashReserve)
	if err != nil {
		return nil
	}
	spendable, _ := invD.Sub(keep)
	event, err := frac(k.MaxEventSleeve)
	if err != nil {
		return nil
	}
	core, _ := spendable.Sub(event)
	if core.Sign() <= 0 {
		add("constraints.max_event_sleeve", "the cash reserve and the event sleeve take all %s; nothing is left for the core", money2(invD))
		return out
	}
	if k.MaxEventSleeve.Sign() > 0 && k.MinPosition.Sign() > 0 && event.Cmp(k.MinPosition.Decimal()) < 0 {
		add("constraints.max_event_sleeve", "the event sleeve holds %s, below the minimum position %s, so no event position can be opened", money2(event), k.MinPosition)
	}

	capacity := zero
	if funds && k.MaxFund.Sign() > 0 {
		capacity, _ = frac(k.MaxFund)
	}
	if stocks {
		perIssuer, err := frac(k.MaxIssuer)
		minPos := k.MinPosition.Decimal()
		switch {
		case err != nil:
		case k.MinPosition.Sign() == 0:
			add("constraints.min_position", "is not set, so the number of single-stock positions cannot be bounded")
		case minPos.Cmp(perIssuer) > 0:
			add("constraints.min_position", "the minimum position %s is above what the issuer cap %s allows in one issuer (%s), so no single stock can satisfy both", k.MinPosition, k.MaxIssuer, money2(perIssuer))
		default:
			n, err := core.Div(minPos, 0, money.Down)
			if err == nil {
				if n.IsZero() {
					add("constraints.min_position", "the minimum position %s is more than the %s there is for the core", k.MinPosition, money2(core))
				}
				held, err := n.Mul(perIssuer, 2, money.Down)
				if err == nil {
					if held.Cmp(core) > 0 {
						held = core
					}
					capacity, _ = capacity.Add(held)
				}
			}
		}
	}
	if capacity.Cmp(core) < 0 {
		add("constraints", "the instruments allowed, the issuer cap %s, the fund cap %s and the minimum position %s let %s of the %s meant for the core be placed; the rest could only stay cash",
			k.MaxIssuer, k.MaxFund, k.MinPosition, money2(capacity), money2(core))
	}
	if !stocks && funds && k.MaxFund.Sign() > 0 && k.MaxFund.Cmp(one) < 0 {
		q, err := one.Div(k.MaxFund, 0, money.Ceiling)
		if err == nil {
			add("constraints.max_fund", "only funds are allowed and one fund may hold %s, so at least %s funds are needed", k.MaxFund, q)
		}
	}
	return out
}
