package universe

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/b070nd/stoxicity/app/src/model"
)

// FileName is the user's file in a tenant's folder.
const FileName = "stocks.yaml"

// Layer is what a person adds to the reference data: lists of stocks, tags on
// single stocks, and companies the SEC lists do not carry. It is one YAML file
// per tenant, tenants/<id>/stocks.yaml:
//
//	lists:
//	  - id: compounders
//	    name: Long-term compounders
//	    tickers: [KO, BRK-B]
//	tags:
//	  KO: [moat, dividend]
//	extra:
//	  - ticker: NESN
//	    name: Nestlé
//	    exchange: SIX
//	    isin: CH0038863350
//	    aliases: [Nestle]
//
// The program rewrites the file when a tag or a list changes (from the TUI or
// "stoxicity universe tag"), so comments in it are not kept.
type Layer struct {
	Lists []ListDef           `yaml:"lists,omitempty"`
	Tags  map[string][]string `yaml:"tags,omitempty"`
	Extra []Extra             `yaml:"extra,omitempty"`
}

// ListDef is a named set of stocks.
type ListDef struct {
	ID      string   `yaml:"id"`
	Name    string   `yaml:"name,omitempty"`
	Tickers []string `yaml:"tickers"`
}

// Extra is a company that the SEC lists lack, such as a non-US listing.
type Extra struct {
	Ticker   string   `yaml:"ticker"`
	Name     string   `yaml:"name"`
	Exchange string   `yaml:"exchange,omitempty"`
	ISIN     string   `yaml:"isin,omitempty"`
	Sector   string   `yaml:"sector,omitempty"`
	Aliases  []string `yaml:"aliases,omitempty"`
}

// LoadLayer reads the file. A missing file is an empty Layer. Unknown keys and
// invalid values are errors, all reported together.
func LoadLayer(path string) (Layer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Layer{}, nil
	}
	if err != nil {
		return Layer{}, fmt.Errorf("universe: %w", err)
	}
	var l Layer
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil && !errors.Is(err, io.EOF) {
		return Layer{}, fmt.Errorf("universe: %s: %w", path, err)
	}
	l.normalize()
	if err := l.Validate(); err != nil {
		return Layer{}, fmt.Errorf("universe: %s: %w", path, err)
	}
	return l, nil
}

// Save writes the file so that a reader sees the old one or the new one.
func (l Layer) Save(path string) error {
	l.normalize()
	if err := l.Validate(); err != nil {
		return fmt.Errorf("universe: %w", err)
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("universe: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("universe: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("universe: %w", err)
	}
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	merr := os.Chmod(tmp.Name(), 0o644)
	if err := errors.Join(werr, serr, cerr, merr); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("universe: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("universe: %w", err)
	}
	return nil
}

// Ticker writes a ticker the way the SEC list does.
func Ticker(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// normalize upper-cases tickers, lower-cases tags, sorts and removes
// duplicates, and drops tags and lists that have nothing left.
func (l *Layer) normalize() {
	for i := range l.Lists {
		l.Lists[i].ID = strings.ToLower(strings.TrimSpace(l.Lists[i].ID))
		l.Lists[i].Tickers = cleanTickers(l.Lists[i].Tickers)
	}
	if len(l.Tags) > 0 {
		out := make(map[string][]string, len(l.Tags))
		for t, tags := range l.Tags {
			var clean []string
			for _, g := range tags {
				clean = append(clean, strings.ToLower(strings.TrimSpace(g)))
			}
			slices.Sort(clean)
			clean = slices.Compact(clean)
			if t = Ticker(t); t != "" && len(clean) > 0 {
				out[t] = clean
			}
		}
		l.Tags = out
	}
	for i := range l.Extra {
		l.Extra[i].Ticker = Ticker(l.Extra[i].Ticker)
	}
}

func cleanTickers(in []string) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		if t = Ticker(t); t != "" {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Validate reports every problem in the layer.
func (l Layer) Validate() error {
	var errs []error
	seen := map[string]bool{}
	for _, d := range l.Lists {
		switch {
		case !model.ValidID(d.ID):
			errs = append(errs, fmt.Errorf("list %q: the ID must be lower-case letters, digits and hyphens", d.ID))
		case seen[d.ID]:
			errs = append(errs, fmt.Errorf("list %q is defined twice", d.ID))
		}
		seen[d.ID] = true
	}
	for t, tags := range l.Tags {
		for _, g := range tags {
			if !model.ValidID(g) {
				errs = append(errs, fmt.Errorf("tag %q on %s: a tag is lower-case letters, digits and hyphens", g, t))
			}
		}
	}
	extras := map[string]bool{}
	for _, e := range l.Extra {
		switch {
		case e.Ticker == "":
			errs = append(errs, errors.New("extra: an entry has no ticker"))
		case strings.TrimSpace(e.Name) == "":
			errs = append(errs, fmt.Errorf("extra %s: no name", e.Ticker))
		case extras[e.Ticker]:
			errs = append(errs, fmt.Errorf("extra %s is defined twice", e.Ticker))
		}
		extras[e.Ticker] = true
	}
	return errors.Join(errs...)
}

// AddTag puts a tag on a stock. It reports whether anything changed.
func (l *Layer) AddTag(ticker, tag string) (bool, error) {
	ticker, tag = Ticker(ticker), strings.ToLower(strings.TrimSpace(tag))
	if ticker == "" {
		return false, errors.New("no ticker")
	}
	if !model.ValidID(tag) {
		return false, fmt.Errorf("tag %q: use lower-case letters, digits and hyphens", tag)
	}
	if slices.Contains(l.Tags[ticker], tag) {
		return false, nil
	}
	if l.Tags == nil {
		l.Tags = map[string][]string{}
	}
	l.Tags[ticker] = append(l.Tags[ticker], tag)
	slices.Sort(l.Tags[ticker])
	return true, nil
}

// RemoveTag takes a tag off a stock. It reports whether anything changed.
func (l *Layer) RemoveTag(ticker, tag string) bool {
	ticker, tag = Ticker(ticker), strings.ToLower(strings.TrimSpace(tag))
	tags := l.Tags[ticker]
	i := slices.Index(tags, tag)
	if i < 0 {
		return false
	}
	l.Tags[ticker] = slices.Delete(slices.Clone(tags), i, i+1)
	if len(l.Tags[ticker]) == 0 {
		delete(l.Tags, ticker)
	}
	return true
}

// SetMember puts a stock into a list or takes it out. A list that does not
// exist is created, with its ID as its name, when a stock is added. It reports
// whether anything changed.
func (l *Layer) SetMember(listID, ticker string, add bool) (bool, error) {
	listID, ticker = strings.ToLower(strings.TrimSpace(listID)), Ticker(ticker)
	if ticker == "" {
		return false, errors.New("no ticker")
	}
	if !model.ValidID(listID) {
		return false, fmt.Errorf("list %q: use lower-case letters, digits and hyphens", listID)
	}
	i := slices.IndexFunc(l.Lists, func(d ListDef) bool { return d.ID == listID })
	if i < 0 {
		if !add {
			return false, nil
		}
		l.Lists = append(l.Lists, ListDef{ID: listID, Name: listID})
		i = len(l.Lists) - 1
	}
	has := slices.Contains(l.Lists[i].Tickers, ticker)
	switch {
	case add && !has:
		l.Lists[i].Tickers = append(slices.Clone(l.Lists[i].Tickers), ticker)
		slices.Sort(l.Lists[i].Tickers)
		return true, nil
	case !add && has:
		l.Lists[i].Tickers = slices.DeleteFunc(slices.Clone(l.Lists[i].Tickers), func(t string) bool { return t == ticker })
		return true, nil
	}
	return false, nil
}

// Clone returns a copy that shares nothing with l.
func (l Layer) Clone() Layer {
	c := Layer{Lists: make([]ListDef, len(l.Lists)), Extra: slices.Clone(l.Extra)}
	for i, d := range l.Lists {
		d.Tickers = slices.Clone(d.Tickers)
		c.Lists[i] = d
	}
	if l.Tags != nil {
		c.Tags = make(map[string][]string, len(l.Tags))
		for k, v := range l.Tags {
			c.Tags[k] = slices.Clone(v)
		}
	}
	return c
}
