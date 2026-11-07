package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/insider"
	"github.com/b070nd/stoxicity/app/src/model"
)

// insiderShown is how many trades the stock page lists; the command line
// (`stoxicity universe insiders TICKER`) lists them all.
const insiderShown = 10

// insiderRows adds the insiders' block of a stock page: what the officers,
// directors and large holders of the company did with its shares, newest first,
// and what they bought and sold in the last quarter. Purchases are
// green and sales red; awards, exercises, tax withholding and gifts are dim,
// because they are pay and bookkeeping, not a view of the company. Nothing is
// added when no trade is stored.
func insiderRows(b *builder, ticker string, d Data) {
	if d.Insiders == nil {
		return
	}
	trades := d.Insiders(ticker)
	if len(trades) == 0 {
		return
	}
	b.blank()
	b.heading("Insiders", nil)
	// The summary is made from the very trades listed below, not from the cached
	// figures of the list, so the two cannot disagree.
	var sum insider.Summary
	if len(trades) > 0 {
		sum = insider.Summarize(trades, d.Now)[trades[0].CIK]
	}
	var parts []string
	if sum.Buys > 0 {
		parts = append(parts, fmt.Sprintf("bought %s (%s)", moneyText(sum.Buys), people(float64(sum.Buyers))))
	}
	if sum.Sells > 0 {
		parts = append(parts, fmt.Sprintf("sold %s (%s)", moneyText(sum.Sells), people(float64(sum.Sellers))))
	}
	switch {
	case len(parts) > 0:
		b.styled(Strong, "  last 90 days, common stock: "+strings.Join(parts, ", "))
	case sum.UnpricedBuys+sum.UnpricedSells == 0:
		b.styled(Dim, "  no purchase or sale of common stock in the last 90 days")
	}
	if n := sum.UnpricedBuys + sum.UnpricedSells; n > 0 {
		b.styled(Warn, fmt.Sprintf("  %d purchases and sales of common stock give no price, so their value is unknown and not in the sums", n))
	}
	for i, t := range trades {
		if i == insiderShown {
			b.styled(Dim, fmt.Sprintf("  %d older trades: stoxicity universe insiders %s", len(trades)-i, ticker))
			break
		}
		url := edgar.FilingIndexURL(t.CIK, edgar.Accession(t.Accession))
		for _, line := range insiderLines(t) {
			b.add(Row{Text: line, Style: tradeStyle(t), URL: url})
		}
	}
	b.styled(Dim, "  from the Form 4 filings (b opens the filing); purchases and sales may be open market or private; a purchase says more than a sale, which is often scheduled in advance")
}

// insiderLines is one trade in words, on two lines so neither is cut at the
// width of a terminal: the day, what was done, how much at what price; and
// below it who did it and what else the form says.
func insiderLines(t model.InsiderTrade) [2]string {
	amount := thousands(int(math.Round(t.Shares))) + " shares"
	if t.Price > 0 {
		amount += " at " + priceText(t.Price) + " = " + moneyText(t.Value())
	}
	who := t.Owner
	if t.Role != "" {
		who += ", " + t.Role
	}
	notes := []string{who}
	if t.After > 0 {
		notes = append(notes, "holds "+thousands(int(math.Round(t.After)))+" after")
	}
	if t.Plan {
		notes = append(notes, "10b5-1 plan")
	}
	if !t.Direct {
		notes = append(notes, "held indirectly")
	}
	return [2]string{
		fmt.Sprintf("  %s  %-26s %s", t.Date, model.InsiderCodeLabel(t.Code), amount),
		"              " + strings.Join(notes, " · "),
	}
}

func tradeStyle(t model.InsiderTrade) Style {
	switch t.Kind() {
	case model.TradeBuy:
		return Good
	case model.TradeSell:
		return Bad
	}
	return Dim
}

func people(n float64) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", int(n))
}

// insiderCell is the BUYS or SELLS cell of the company list: the money the
// insiders bought or sold in the last quarter, empty when unknown.
func insiderCell(d Data, key, ticker string, style Style) Cell {
	v, ok := d.Numbers[key][ticker]
	if !ok || v <= 0 {
		return Cell{}
	}
	return Num(moneyText(v), v).Styled(style)
}
