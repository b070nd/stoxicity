package laya

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	// maxSummaryRunes is how much of the summary is sent. The checkpoints read
	// 512 to 1024 tokens, so the start of the summary is all they would see.
	maxSummaryRunes = 1200
	// maxTitleRunes only guards against a title that is really a body of text.
	maxTitleRunes = 500
)

// state is the text the model reads for one article: the title, a dash, and the
// start of the summary, preceded by the filing form and items when there are
// any. It is empty when the article has neither title nor summary. The URL is
// never part of it.
func state(a model.Article) string {
	title := clip(strings.Join(strings.Fields(a.Title), " "), maxTitleRunes)
	summary := clip(strings.Join(strings.Fields(a.Summary), " "), maxSummaryRunes)
	if summary != "" {
		summary = filingPrefix(a) + summary
	}
	switch {
	case title == "":
		return summary
	case summary == "":
		return title
	}
	return title + " — " + summary
}

// filingPrefix names the SEC form and 8-K items, which tell the model more
// than the prose does. It is empty for articles that are not filings.
func filingPrefix(a model.Article) string {
	form := strings.TrimSpace(a.Form)
	var items []string
	for _, it := range a.Items {
		if it = strings.TrimSpace(it); it != "" {
			items = append(items, it)
		}
	}
	list := strings.Join(items, ", ")
	switch {
	case form != "" && list != "":
		return "Form " + form + " (items " + list + "): "
	case form != "":
		return "Form " + form + ": "
	case list != "":
		return "Items " + list + ": "
	}
	return ""
}

// clip cuts s to at most n runes, at a word boundary when there is one.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	if unicode.IsSpace(runes[n]) {
		return strings.TrimSpace(string(runes[:n]))
	}
	for i := n - 1; i > 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return strings.TrimSpace(string(runes[:i]))
		}
	}
	return string(runes[:n])
}
