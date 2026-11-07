// Package entity resolves company mentions in text to tickers. Stock pages,
// discovery and the watchlist all depend on it. It holds a master list of
// tickers, names, aliases and SEC CIKs and finds them in article text.
package entity

import "github.com/b070nd/stoxicity/app/src/model"

// Resolver finds companies in text.
type Resolver interface {
	// Find returns the entities mentioned in text. It must not match common
	// words that happen to equal a ticker or a name.
	Find(text string) []model.Entity
	// Lookup returns one entity by ticker.
	Lookup(ticker string) (model.Entity, bool)
}

// CIKResolver is a Resolver that also knows the tickers of a SEC number. A
// filing names its filer by CIK, so its company is looked up here and never
// searched for in its text.
type CIKResolver interface {
	Resolver
	// ByCIK returns every ticker the company with the CIK (ten digits) trades
	// under, sorted by ticker; none when the SEC list does not know the CIK.
	ByCIK(cik string) []model.Entity
}
