package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/eval"
	"github.com/b070nd/stoxicity/app/src/labels"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/pages"
)

// progressText is the labelling in the status line: how many labels of the
// target there are, and how the lens does against them.
func progressText(p labels.Progress) string {
	s := fmt.Sprintf("labels %d of %d", p.Total, labels.Target)
	if pr, ok := p.All.PrecisionValue(); ok {
		s += fmt.Sprintf(" · precision %.2f", pr)
	}
	if rc, ok := p.All.RecallValue(); ok {
		s += fmt.Sprintf(" · recall %.2f", rc)
	}
	return s
}

// buildLabel is the screen of the articles to judge. The queue is the ones the
// lens is least sure of, one kind of event after the other, and a random fourth.
// Their scores are not on the rows, so that the verdict does not follow the
// lens; the article shows them.
func buildLabel(d Data) Screen {
	s := Screen{Title: "Label"}
	var b builder
	p := d.Progress
	b.heading("Teaching the lens", nil)
	b.styled(Strong, fmt.Sprintf("  %d labels of the %d it takes before the numbers mean anything · kept %d · dropped %d · %d from the random sample",
		p.Total, labels.Target, p.Kept, p.Dropped, p.Sampled))
	b.styled(Dim, fmt.Sprintf("  at relevance %.2f the lens calls an article relevant:", p.Threshold))
	b.add(Row{Text: "  " + measureLine("against all labels", p.All), Style: measureStyle(p.All)})
	b.add(Row{Text: "  " + measureLine("against the random sample (the honest number)", p.Honest), Style: measureStyle(p.Honest)})
	if p.Total < 20 {
		b.styled(Dim, "  with fewer than 20 labels these are not worth reading; `stoxicity eval` has the table by kind of event")
	} else {
		b.styled(Dim, "  `stoxicity eval` has the table by kind of event, version and domain")
	}
	b.blank()
	b.heading("Judge these", nil)
	b.styled(Dim, "  + keeps (relevant to a long-term investor), - drops (noise); enter opens the article with its score")
	if len(d.Queue) == 0 {
		b.styled(Dim, "  nothing left to judge in the last 30 days")
		s.Rows = b.rows
		return s
	}
	zone := d.Params.Zone
	if zone == nil {
		zone = time.UTC
	}
	for _, pk := range d.Queue {
		a := pk.Article
		mark := " "
		switch d.Verdicts[a.ID] {
		case labels.Keep:
			mark = "+"
		case labels.Drop:
			mark = "-"
		}
		title := a.Title
		if label := model.ItemLabel(a.Items, 3); label != "" {
			title += " · " + label
		}
		tick := ""
		if len(a.Tickers) > 0 {
			tick = " [" + strings.Join(pages.Head(a.Tickers, 3), " ") + "]"
		}
		kind := string(a.Score.Kind)
		if kind == "" {
			kind = "-"
		}
		sample := " "
		if pk.Sampled {
			sample = "°" // from the random sample
		}
		a2 := a
		b.add(Row{
			Text:    fmt.Sprintf("%s%s %s  %-18s %s  %s%s", mark, sample, a.Published.In(zone).Format("01-02 15:04"), kind, title, a.Domain, tick),
			Article: &a2,
		})
	}
	b.blank()
	b.styled(Dim, "  ° drawn at random; the others are the ones the lens is least sure of")
	s.Rows = b.rows
	return s
}

func measureLine(name string, c eval.Confusion) string {
	if c.Total() == 0 {
		return name + ": no labels"
	}
	return fmt.Sprintf("%s: precision %s · recall %s", name, c.Precision(), c.Recall())
}

// measureStyle is green when the lens is right about most of what it calls
// relevant, red when it is wrong about most, dim when there is nothing to say.
func measureStyle(c eval.Confusion) Style {
	pr, ok := c.PrecisionValue()
	switch {
	case !ok || c.Total() < 5:
		return Dim
	case pr >= 0.7:
		return Good
	case pr < 0.5:
		return Bad
	}
	return Warn
}
