package tui

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/marketdata"
)

var priceLevels = []rune("▁▂▃▄▅▆▇█")

// priceCalendarCell keeps one position per month, including missing months.
// Heights compare this stock's monthly closes, never its trading volume.
func priceCalendarCell(calendar marketdata.PriceCalendar) Cell {
	var periods [12]marketdata.PricePeriod
	for i, month := range calendar.Months {
		periods[i] = month.PricePeriod
	}
	return pricePeriodCell(periods[:])
}

func priceYearsCell(calendar marketdata.PriceCalendar) Cell {
	var periods [10]marketdata.PricePeriod
	for i := range periods {
		if i < len(calendar.Years) {
			periods[i] = calendar.Years[i].PricePeriod
		}
	}
	return pricePeriodCell(periods[:])
}

func pricePeriodCell(periods []marketdata.PricePeriod) Cell {
	low, high := math.Inf(1), math.Inf(-1)
	for _, month := range periods {
		if calendarPriceKnown(month) {
			low, high = min(low, month.Close), max(high, month.Close)
		}
	}
	var text strings.Builder
	spans := make([]Span, 0, len(periods))
	for i, month := range periods {
		glyph, style := '·', Dim
		if month.Day != "" && !calendarPriceKnown(month) {
			glyph = '?'
		}
		if calendarPriceKnown(month) {
			level := 3 // a constant series has a visible, level middle bar
			if high > low {
				level = int(math.Round(7 * (month.Close - low) / (high - low)))
			}
			glyph = priceLevels[min(7, max(0, level))]
			if month.HasChange {
				style = gainStyle(month.Change)
			}
		}
		text.WriteRune(glyph)
		spans = append(spans, Span{From: i, To: i + 1, Style: style})
	}
	return Cell{Text: text.String(), Spans: spans}
}

func calendarPriceKnown(month marketdata.PricePeriod) bool {
	return month.Close > 0 && !math.IsNaN(month.Close) && !math.IsInf(month.Close, 0) && !month.Unresolved
}

func (d Data) priceCalendar(ticker string) marketdata.PriceCalendar {
	if calendar, ok := d.PriceCalendars[marketdata.NormTicker(ticker)]; ok {
		return calendar
	}
	return marketdata.BuildPriceCalendar(d.priceCalendarAt(), nil, nil, marketdata.SplitCoverage{})
}

func (d Data) priceCalendarCells(ticker string) (Cell, Cell) {
	if d.PriceCalendarError != "" {
		return Str("????????????").Styled(Dim), Str("??????????").Styled(Dim)
	}
	if calendar, ok := d.PriceCalendars[marketdata.NormTicker(ticker)]; ok {
		return priceCalendarCell(calendar), priceYearsCell(calendar)
	}
	return Str("············").Styled(Dim), Str("··········").Styled(Dim)
}

func (d Data) priceCalendarAt() time.Time {
	if d.Params.Zone != nil {
		return d.Now.In(d.Params.Zone)
	}
	return d.Now
}

func priceCalendarRows(b *builder, ticker string, d Data) {
	if d.PriceCalendars == nil && d.PriceCalendarError == "" {
		return
	}
	b.blank()
	b.heading("12-month price calendar", nil)
	if d.PriceCalendarError != "" {
		b.styled(Warn, "  Price history unavailable: "+d.PriceCalendarError)
		return
	}
	calendar := d.priceCalendar(ticker)
	strip := priceCalendarCell(calendar)
	latest, count := "", 0
	for _, m := range calendar.Months {
		if m.Day != "" {
			count++
			latest = max(latest, m.Day)
		}
	}
	if count == 0 {
		b.styled(Dim, "  Public history fills in the background; fetch now: stoxicity universe history "+ticker)
	} else {
		currency := calendar.Currency
		if currency == "" {
			currency = "currency unspecified"
		}
		b.styled(Dim, "  Last stored monthly closes · "+currency+" · through "+latest)
	}
	columns := 4
	if d.Width > 0 {
		columns = min(4, max(1, (d.Width-2)/29))
	}
	for start := 0; start < len(calendar.Months); start += columns {
		row := Row{Text: "  "}
		for i := start; i < min(start+columns, len(calendar.Months)); i++ {
			m := calendar.Months[i]
			date, _ := time.Parse("2006-01", m.Month)
			label := date.Format("Jan06")
			if m.Month == d.priceCalendarAt().Format("2006-01") {
				label += "*"
			}
			close, change := "—", "—"
			if calendarPriceKnown(m.PricePeriod) {
				close = priceText(m.Close)
			}
			if m.HasChange {
				change = signedPercent(m.Change)
			}
			glyph := string([]rune(strip.Text)[i])
			prefix := fmt.Sprintf("%-6s ", label)
			from := utf8.RuneCountInString(row.Text + prefix)
			if d.Width > 0 && d.Width < 29 {
				row.Text += fmt.Sprintf("%s%s %8s", prefix, glyph, change)
			} else {
				row.Text += fmt.Sprintf("%s%s %8s %8s", prefix, glyph, close, change)
			}
			row.Spans = append(row.Spans, Span{From: from, To: utf8.RuneCountInString(row.Text), Style: strip.Spans[i].Style})
			if i+1 < min(start+columns, len(calendar.Months)) {
				row.Text += "  "
			}
		}
		b.add(row)
	}
	b.styled(Dim, "  Height = relative close · green up / yellow flat / red down")
	b.styled(Dim, "  * month to date · · missing · ? unresolved · dim = no comparison")
	b.styled(Dim, "  Split-adjusted prices; excludes dividends and FX conversion")
}
