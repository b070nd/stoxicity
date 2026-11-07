// Package pipeline is the ingest path that builds the shared information base.
// It runs once per article, whatever the number of tenants, and knows nothing
// about tenants or watches while ingesting:
//
//	fetch (feeds) → sanitize → entity resolution → classification (topics,
//	sector) → base scoring (lens) → second opinion (optional) → cluster (with
//	the stored articles of the same days) → store
//
// Scores are written with the lens version, never overwritten. Tenant-specific
// meaning (watches, labels) is applied afterwards, on read, by package ledger.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/cluster"
	"github.com/b070nd/stoxicity/app/src/entity"
	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/sanitize"
	"github.com/b070nd/stoxicity/app/src/scheduler"
	"github.com/b070nd/stoxicity/app/src/store"
)

const (
	// clusterWindow is how far around a batch the stored articles are
	// searched for copies of the same story. It equals the largest gap between
	// the publication times of near-duplicate titles that package cluster accepts.
	clusterWindow = 48 * time.Hour
)

// Classifier decides the topics and the sector of an article from its text and
// tickers. Package classify implements it.
type Classifier interface {
	// Classify returns the topics of the article, sorted, and its sector, or
	// "" when there is none.
	Classify(model.Article) (topics []string, sector string)
	// Version identifies the rules in use; it is empty when there are none.
	Version() string
}

// Enricher is a second scorer, such as laya, that gives an opinion on a batch of
// articles. The pipeline merges each opinion into the rule-based score with
// lens.Apply, under Config.Use and Config.MinConfidence.
type Enricher interface {
	// Opinions returns one opinion per article, in order. It returns an error
	// and no opinions when it cannot answer.
	Opinions(ctx context.Context, articles []model.Article) ([]lens.Opinion, error)
	// Version identifies the model and questions in use. It becomes part of the
	// stored lens version.
	Version() string
}

// Config carries what a Pipeline works with.
type Config struct {
	// Store is the database. Required.
	Store store.Store
	// Lens scores every article. Required.
	Lens *lens.Lens
	// Entities finds company mentions in the text. Nil means tickers are not
	// resolved and Article.Tickers keeps what the fetcher set.
	Entities entity.Resolver
	// Classifier files articles under topics and a sector. Nil means Topics
	// keep what the fetcher set, Score.Sector keeps what the lens set, and the
	// stored LensVersion is the lens version alone.
	Classifier Classifier
	// Enricher gives a second opinion on each batch of articles. Nil means the
	// rule-based score stands alone. When it fails, the batch keeps its
	// rule-based scores, under a version without the enricher's, so that a later
	// Rescore adds the scores with the opinion.
	Enricher Enricher
	// Use says which of the opinion's answers change the score, and
	// MinConfidence how confident an answer must be to do so. See lens.Apply.
	// They are ignored without an Enricher.
	Use           lens.Use
	MinConfidence float64
	// Primary says whether a source is a primary one (a filing, a regulator),
	// which decides the reporting origin of its articles. Nil means none is.
	Primary func(sourceID string) bool
	// ItemRating reads the 8-K items of a filing into a kind and a materiality;
	// the claims of a filing come from it. Nil leaves items without claims.
	ItemRating func(items []string) (model.ItemRating, bool)
	// Logger receives debug lines. Nil discards them.
	Logger *slog.Logger
	// Now is the clock; time.Now if nil.
	Now func() time.Time
}

// Pipeline ingests articles into the shared base and rescores them. It
// implements scheduler.Sink and is safe for concurrent use as far as the Store
// is.
type Pipeline struct {
	cfg Config
}

var _ scheduler.Sink = (*Pipeline)(nil)

// New returns a Pipeline using cfg.
func New(cfg Config) *Pipeline {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Pipeline{cfg: cfg}
}

// Ingest stores the articles one poll of src found and returns how many of
// them were not stored before. It cleans the title and summary with package
// sanitize, sets SourceID from src and normalises Domain, drops articles
// without an ID or a title and repeated IDs, adds the tickers found in the text
// to those already set, adds the topics the Classifier finds to those already
// set, scores each article, fills Score.Sector from the Classifier when the lens
// left it empty, asks the Enricher, if any, for one opinion on the whole batch
// and merges it, assigns ClusterIDs within the
// batch and against the stored articles published around it, and saves
// everything in one call. When it returns an error nothing was stored.
//
// A story that arrives again in a later poll joins the cluster of the copy
// already stored: stored articles keep their ClusterID.
func (p *Pipeline) Ingest(ctx context.Context, src model.Source, articles []model.Article) (int, error) {
	batch := prepare(src, articles)
	if len(batch) == 0 {
		return 0, nil
	}
	// Fetchers stamp the start of their poll, which can be older than an article
	// another worker has already saved, and the ledger's cursor on the fetch time
	// would skip it. The fetch time is therefore left for the store to set when
	// the batch is committed (see store.Save); a stored article keeps the time it
	// was first saved with.
	for i := range batch {
		batch[i].Fetched = time.Time{}
	}

	ids := make([]string, len(batch))
	for i, a := range batch {
		ids[i] = a.ID
	}
	known, err := p.cfg.Store.Has(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("pipeline: check stored articles: %w", err)
	}
	added := 0
	for _, id := range ids {
		if !known[id] {
			added++
		}
	}

	for i := range batch {
		p.resolveTickers(&batch[i])
		sector := p.classify(&batch[i])
		batch[i].Score = p.cfg.Lens.Score(batch[i])
		p.present(&batch[i])
		p.applyClassification(&batch[i], sector)
	}
	p.enrich(ctx, batch)
	if err := p.assignClusters(ctx, batch); err != nil {
		return 0, err
	}
	if err := p.cfg.Store.Save(ctx, batch); err != nil {
		return 0, fmt.Errorf("pipeline: save: %w", err)
	}
	p.recordEvents(ctx, batch)
	p.cfg.Logger.Debug("pipeline: ingested", "source", src.ID, "articles", len(batch), "new", added)
	return added, nil
}

// prepare cleans the articles of one poll and drops those that cannot be
// stored. It copies the articles and shares no slices with the input.
func prepare(src model.Source, in []model.Article) []model.Article {
	seen := make(map[string]bool, len(in))
	out := make([]model.Article, 0, len(in))
	for _, a := range in {
		a.Title = sanitize.Text(a.Title)
		a.Summary = sanitize.Text(a.Summary)
		if a.ID == "" || a.Title == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		a.SourceID = src.ID
		a.Domain = normaliseDomain(a.Domain)
		if a.Domain == "" {
			a.Domain = normaliseDomain(src.Domain)
		}
		if a.Domain == "" {
			if u, err := url.Parse(a.URL); err == nil {
				a.Domain = normaliseDomain(u.Hostname())
			}
		}
		a.Items = slices.Clone(a.Items)
		a.Topics = slices.Clone(a.Topics)
		a.Tickers = normaliseTickers(a.Tickers)
		out = append(out, a)
	}
	return out
}

// normaliseDomain lower-cases d and removes surrounding space, a leading
// "www." and a trailing dot.
func normaliseDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "www.")
	return strings.TrimSuffix(d, ".")
}

// normaliseTickers returns the symbols in upper case, sorted, without blanks
// and repeats.
func normaliseTickers(in []string) []string {
	var out []string
	for _, t := range in {
		if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
			out = append(out, t)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// resolveTickers sets the tickers of a. A SEC filing belongs to the company
// whose CIK it carries, and that is all it is about: its tickers are the ones
// the SEC list gives that CIK (none if it gives none), whatever the title or
// the summary happen to contain. Anything else adds the tickers its title and
// summary mention to a.Tickers.
func (p *Pipeline) resolveTickers(a *model.Article) {
	if p.cfg.Entities == nil {
		return
	}
	if cr, ok := p.cfg.Entities.(entity.CIKResolver); ok && a.CIK != "" {
		a.Tickers = nil
		for _, e := range cr.ByCIK(a.CIK) {
			a.Tickers = append(a.Tickers, e.Ticker)
		}
		a.Tickers = normaliseTickers(a.Tickers)
		return
	}
	found := p.cfg.Entities.Find(a.Title + "\n" + a.Summary)
	if len(found) == 0 {
		return
	}
	all := slices.Clone(a.Tickers)
	for _, e := range found {
		all = append(all, e.Ticker)
	}
	a.Tickers = normaliseTickers(all)
}

// classify unions the topics the Classifier finds into a.Topics, keeping those
// already set, sorted and without repeats, and returns the sector it found.
func (p *Pipeline) classify(a *model.Article) (sector string) {
	if p.cfg.Classifier == nil {
		return ""
	}
	topics, sector := p.cfg.Classifier.Classify(*a)
	if len(topics) > 0 {
		all := append(slices.Clone(a.Topics), topics...)
		slices.Sort(all)
		a.Topics = slices.Compact(all)
	}
	return sector
}

// applyClassification completes the score of a that the lens just made. The
// sector is the Classifier's when the lens left it empty. A score row is
// immutable per version in the store (the first write wins), so when rules
// change the version must change too, or the new topics and sectors would
// never replace the old row: a non-empty Classifier version is therefore
// appended to the lens version as "<lens>+<classifier>".
func (p *Pipeline) applyClassification(a *model.Article, sector string) {
	if p.cfg.Classifier == nil {
		return
	}
	if a.Score.Sector == "" {
		a.Score.Sector = sector
	}
	if v := p.cfg.Classifier.Version(); v != "" {
		a.Score.LensVersion += "+" + v
	}
}

// enrich asks the Enricher for its opinion on the whole batch in one call and
// merges each opinion into the score of its article with lens.Apply: the
// topic an opinion adds joins Article.Topics, sorted and without repeats, and
// "+<enricher version>" ends the lens version, also when the opinion had
// nothing to say, because the scorer was consulted. The sector is only ever
// filled where the lens and the Classifier left it empty. When the Enricher
// fails the batch keeps its rule-based scores and versions untouched, so that a
// later Rescore can add the scores with the opinion, and a warning is logged
// without the cause's address. It reports whether the opinions were merged.
func (p *Pipeline) enrich(ctx context.Context, batch []model.Article) bool {
	if p.cfg.Enricher == nil || len(batch) == 0 {
		return false
	}
	// A SEC filing has a title such as "4 - Kenvue Inc." and a summary of
	// boilerplate: no prose for a model to read. Its form and items are
	// exact data, which the lens reads better than a model could guess, so
	// only articles without a form are sent. The others keep the version
	// without the enricher's part.
	var ask []int
	var texts []model.Article
	for i, a := range batch {
		if a.Form == "" {
			ask = append(ask, i)
			texts = append(texts, a)
		}
	}
	if len(ask) == 0 {
		return true
	}
	opinions, err := p.cfg.Enricher.Opinions(ctx, texts)
	if err == nil && len(opinions) != len(texts) {
		err = fmt.Errorf("%d opinions for %d articles", len(opinions), len(texts))
	}
	if err != nil {
		p.cfg.Logger.Warn("pipeline: second opinion unavailable, keeping the rule-based scores",
			"articles", len(texts), "cause", withoutAddress(err))
		return false
	}
	suffix := "+" + p.cfg.Enricher.Version()
	for j, i := range ask {
		a := &batch[i]
		score, topic := lens.Apply(a.Score, opinions[j], p.cfg.Use, p.cfg.MinConfidence)
		score.LensVersion += suffix
		a.Score = score
		if topic != "" && !slices.Contains(a.Topics, topic) {
			a.Topics = append(a.Topics, topic)
			slices.Sort(a.Topics)
		}
	}
	return true
}

// withoutAddress returns err's text with the request address left out: a
// failed HTTP request names its URL, which may carry a credential.
func withoutAddress(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// Version is the version a score written now carries: the lens version, then
// "+" and the Classifier's version when there is one, then "+" and the
// Enricher's. A batch whose Enricher failed carries the shorter version
// without the last part.
func (p *Pipeline) Version() string {
	v := p.cfg.Lens.Version()
	if p.cfg.Classifier != nil {
		if cv := p.cfg.Classifier.Version(); cv != "" {
			v += "+" + cv
		}
	}
	if p.cfg.Enricher != nil {
		v += "+" + p.cfg.Enricher.Version()
	}
	return v
}

// assignClusters sets ClusterID on every article of batch. The batch is
// grouped together with the stored articles published within clusterWindow of
// it; a group that holds a stored article reuses that article's ClusterID, so
// IDs already in the base never change.
func (p *Pipeline) assignClusters(ctx context.Context, batch []model.Article) error {
	from, to := p.window(batch)
	stored, err := p.cfg.Store.Range(ctx, from, to)
	if err != nil {
		return fmt.Errorf("pipeline: load stored articles: %w", err)
	}
	existing := make(map[string]string, len(stored))
	for _, a := range stored {
		if a.ClusterID != "" {
			existing[a.ID] = a.ClusterID
		}
	}

	all := make([]model.Article, 0, len(stored)+len(batch))
	all = append(all, stored...)
	all = append(all, batch...)
	final := make(map[string]string, len(all))
	for _, g := range cluster.Build(all) {
		id := g.ID
		for _, m := range g.Members {
			if c := existing[m.ID]; c != "" {
				id = c
				break
			}
		}
		for _, m := range g.Members {
			final[m.ID] = id
		}
	}
	for i := range batch {
		batch[i].ClusterID = final[batch[i].ID]
	}
	return nil
}

// window returns the publication times [from, to) of the stored articles that
// may belong to a story of the batch.
func (p *Pipeline) window(batch []model.Article) (from, to time.Time) {
	for _, a := range batch {
		if a.Published.IsZero() {
			continue
		}
		if from.IsZero() || a.Published.Before(from) {
			from = a.Published
		}
		if a.Published.After(to) {
			to = a.Published
		}
	}
	if from.IsZero() {
		from, to = p.cfg.Now(), p.cfg.Now()
	}
	return from.Add(-clusterWindow), to.Add(clusterWindow)
}

// rescoreBatch is how many articles Rescore saves in one transaction.
const rescoreBatch = 500

// Rescore recomputes tickers, topics, sector and score of the stored articles
// published in [from, to), whatever source they come from, with the current
// lens, entities, classifier and Enricher, exactly as Ingest does for a new
// article, and saves them. A batch of 500 is one call to the Enricher; after a
// failure the remaining batches are not offered to it. A score row for the
// version in use is added; the rows of older versions stay. Clusters do not change: every article keeps its ClusterID. It
// returns the number of articles it went through, including those whose new
// score equals a stored one. The articles of earlier batches stay rescored when
// a later one fails.
func (p *Pipeline) Rescore(ctx context.Context, from, to time.Time) (int, error) {
	stored, err := p.cfg.Store.Range(ctx, from, to)
	if err != nil {
		return 0, fmt.Errorf("pipeline: load articles to rescore: %w", err)
	}
	done := 0
	asking := p.cfg.Enricher != nil
	for start := 0; start < len(stored); start += rescoreBatch {
		batch := stored[start:min(start+rescoreBatch, len(stored))]
		for i := range batch {
			p.resolveTickers(&batch[i])
			sector := p.classify(&batch[i])
			batch[i].Score = p.cfg.Lens.Score(batch[i])
			p.present(&batch[i])
			p.applyClassification(&batch[i], sector)
		}
		if asking {
			// One failed batch is reported; the others do not wait for a sidecar
			// that is not there.
			asking = p.enrich(ctx, batch)
		}
		if err := p.cfg.Store.Save(ctx, batch); err != nil {
			return done, fmt.Errorf("pipeline: save rescored articles: %w", err)
		}
		done += len(batch)
	}
	p.cfg.Logger.Debug("pipeline: rescored", "articles", done)
	return done, nil
}

// Relinked is what Relink did.
type Relinked struct {
	Filings int // SEC filings looked at
	Changed int // filings whose tickers were different and are saved again
	Cleared int // of those, filings left with no ticker: their CIK is not in the SEC list
}

// Relink gives the stored SEC filings published in [from, to) the tickers of
// their CIK, as ingestion now does, and saves those that change. Nothing else
// about an article changes, and no score is made again. Articles that are not
// filings are left as they are.
func (p *Pipeline) Relink(ctx context.Context, from, to time.Time) (Relinked, error) {
	var res Relinked
	if p.cfg.Entities == nil {
		return res, errors.New("pipeline: no company list to link filings with")
	}
	stored, err := p.cfg.Store.Range(ctx, from, to)
	if err != nil {
		return res, fmt.Errorf("pipeline: load filings to relink: %w", err)
	}
	var changed []model.Article
	for _, a := range stored {
		if a.CIK == "" {
			continue
		}
		res.Filings++
		before := slices.Clone(a.Tickers)
		p.resolveTickers(&a)
		if slices.Equal(before, a.Tickers) {
			continue
		}
		res.Changed++
		if len(a.Tickers) == 0 {
			res.Cleared++
		}
		changed = append(changed, a)
	}
	for start := 0; start < len(changed); start += rescoreBatch {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := p.cfg.Store.Save(ctx, changed[start:min(start+rescoreBatch, len(changed))]); err != nil {
			return res, fmt.Errorf("pipeline: save relinked filings: %w", err)
		}
	}
	return res, nil
}
