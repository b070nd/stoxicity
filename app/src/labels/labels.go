// Package labels records the user's keep/drop verdicts on articles. They are
// the gold set: the data that calibrates the lens, trains laya
// (laya-train) and feeds package eval. The TUI writes one label per keypress.
package labels

import (
	"context"
	"time"
)

// Verdict is what the user decided about an article.
type Verdict string

const (
	Keep Verdict = "keep" // relevant to a long-term investor
	Drop Verdict = "drop" // noise
)

// Label is one verdict. Labels belong to a tenant: what is noise to one desk
// may matter to another, so they are never pooled. LensVersion is the version
// that scored the article when the user judged it, so disagreement can be
// measured per version.
type Label struct {
	TenantID    string
	ArticleID   string
	Verdict     Verdict
	At          time.Time
	LensVersion string
	// Sampled is true when the article was shown by random sampling instead of
	// by rank. Labels from the top of the ranking alone make the lens look
	// better than it is; a sampled slice keeps precision estimates honest.
	Sampled bool
}

// Store persists one tenant's labels; get it from store.TenantStore.Labels.
type Store interface {
	Put(ctx context.Context, l Label) error
	All(ctx context.Context) ([]Label, error)
}
