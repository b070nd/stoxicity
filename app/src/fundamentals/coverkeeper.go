package fundamentals

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/b070nd/stoxicity/app/src/company"
	"github.com/b070nd/stoxicity/app/src/httpx"
)

// The cover page of an inline XBRL report starts the document body: the hidden
// header with the contexts comes first, the cover facts right after. The start
// of a document is therefore asked for first, and the whole document only when
// the start cannot be trusted to hold the whole cover.
const (
	coverPrefix = 1 << 20 // bytes asked for first
	// coverMargin: a prefix is trusted when the last cover fact ends before this
	// fraction of it, so that a cover cut off by the end of the prefix is not
	// taken for a complete one.
	coverMargin = 0.5
	// DefaultCoverAge is how long a successfully read issuer is left before its
	// record of filings is looked at again for a newer report; DefaultCoverRetry
	// is how long one that failed is left.
	DefaultCoverAge   = 30 * 24 * time.Hour
	DefaultCoverRetry = 3 * 24 * time.Hour
	// MaxCoverBackoff caps the wait after repeated failures.
	MaxCoverBackoff = 14 * 24 * time.Hour
)

// Kinds of a cover check.
const (
	// CheckFailed: the request or the parsing failed; Problem says how.
	CheckFailed = "failed"
	// CheckNoCover: the recent reports have no machine-readable cover page. That is
	// not an error: this method does not support the issuer.
	CheckNoCover = "nocover"
)

// Backoff is how long an issuer is left after its n-th consecutive failure: the
// retry wait doubled for each failure after the first, up to MaxCoverBackoff.
func Backoff(retry time.Duration, failures int) time.Duration {
	if retry <= 0 {
		retry = DefaultCoverRetry
	}
	d := retry
	for i := 1; i < failures && d < MaxCoverBackoff; i++ {
		d *= 2
	}
	return min(d, MaxCoverBackoff)
}

// CoverRead is the cover page of one report, with the public availability of the
// report as the SEC's record of the company gives it.
type CoverRead struct {
	CIK       string
	Accession string
	Form      string
	Filed     string    // YYYY-MM-DD
	Accepted  time.Time // zero when the record gives no acceptance time
	Classes   []ClassShares
	At        time.Time
}

// CoverCheck is where the reading of one issuer stands.
type CoverCheck struct {
	At        time.Time
	Accession string // the report the last good reading took, empty if it found none
	Problem   string
	// Kind is "" when the last look worked, otherwise CheckFailed or CheckNoCover.
	Kind string
	// Failures counts the failed looks in a row; Next is the earliest time the
	// issuer is looked at again (zero: by the ordinary age rule).
	Failures int
	Next     time.Time
}

// CoverCandidate is an issuer whose cover page is wanted.
type CoverCandidate struct {
	CIK    string
	Ticker string
	// Listings is how many common stock listings the issuer has; issuers with
	// several classes listed are read first, because only the cover page can tell
	// their classes apart.
	Listings int
	// Priority marks issuers the user follows, lists or sees in the news.
	Priority bool
}

// CoverStore keeps what the keeper reads.
type CoverStore interface {
	// CoverChecks returns where the reading of each issuer stands, by CIK.
	CoverChecks(ctx context.Context) (map[string]CoverCheck, error)
	// PutCover stores the share counts of a report and notes the issuer as read.
	PutCover(ctx context.Context, r CoverRead) error
	// PutCoverCheck notes an issuer as looked at. c.Problem says why nothing was
	// stored and is empty when the issuer simply has no newer report; an empty
	// c.Accession leaves the stored one.
	PutCoverCheck(ctx context.Context, cik string, c CoverCheck) error
}

// CoverReport says what one refresh did.
type CoverReport struct {
	// Asked is the issuers looked at, Stored the reports whose counts were stored,
	// Unchanged the issuers whose newest report was already stored, Empty those
	// whose reports have no machine-readable cover, Bad those that failed, and
	// Left the issuers still due.
	Asked, Stored, Unchanged, Empty, Bad, Left int
	// Requests counts the requests made; Full counts the documents that had to be
	// read whole after the start did not do.
	Requests, Full int
}

// CoverKeeper keeps the class-level share counts up to date in the background.
// It never runs while a list is drawn.
type CoverKeeper struct {
	Store  CoverStore
	Client httpx.Client
	// SubmissionsURL is the address of a company's record of filings with %s for
	// the ten-digit CIK; empty means the SEC's.
	SubmissionsURL string
	// Archives is the base of the filing documents; empty means the SEC's.
	Archives string
	// Candidates lists the issuers wanted.
	Candidates func(ctx context.Context) ([]CoverCandidate, error)
	// Map maps the classes of the issuer's newest stored count to securities; it
	// may be nil.
	Map func(ctx context.Context, cik string, at time.Time) error
	// MaxAge and Retry default to DefaultCoverAge and DefaultCoverRetry.
	MaxAge, Retry time.Duration
	Now           func() time.Time
}

func (k *CoverKeeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Due returns the candidates that are due for a reading, the most wanted first:
// the issuers the user follows, then those never read, then those with several
// listings (only the cover page tells their classes apart), then the longest
// unread. An issuer that failed is left until its persisted retry time, which
// grows with each failure in a row; the whole universe is walked in this order,
// a budget at a time, and a restart continues from the stored state.
func Due(cands []CoverCandidate, checks map[string]CoverCheck, now time.Time, maxAge, retry time.Duration) []CoverCandidate {
	if maxAge <= 0 {
		maxAge = DefaultCoverAge
	}
	if retry <= 0 {
		retry = DefaultCoverRetry
	}
	var due []CoverCandidate
	seen := map[string]bool{}
	for _, c := range cands {
		if c.CIK == "" || seen[c.CIK] {
			continue
		}
		seen[c.CIK] = true
		ck, ok := checks[c.CIK]
		switch {
		case !ok:
		case !ck.Next.IsZero():
			if now.Before(ck.Next) {
				continue
			}
		case ck.Problem != "" && now.Sub(ck.At) >= retry:
		case ck.Problem == "" && now.Sub(ck.At) >= maxAge:
		default:
			continue
		}
		due = append(due, c)
	}
	slices.SortStableFunc(due, func(a, b CoverCandidate) int {
		_, aread := checks[a.CIK]
		_, bread := checks[b.CIK]
		switch {
		case a.Priority != b.Priority:
			if a.Priority {
				return -1
			}
			return 1
		case aread != bread:
			if !aread {
				return -1
			}
			return 1
		case (a.Listings > 1) != (b.Listings > 1):
			if a.Listings > 1 {
				return -1
			}
			return 1
		}
		return checks[a.CIK].At.Compare(checks[b.CIK].At)
	})
	return due
}

// Refresh reads the cover pages of up to budget due issuers. A block or rate
// limit from the SEC stops it at once and is returned with what was done.
func (k *CoverKeeper) Refresh(ctx context.Context, budget int) (CoverReport, error) {
	var rep CoverReport
	if budget <= 0 || k.Candidates == nil {
		return rep, nil
	}
	cands, err := k.Candidates(ctx)
	if err != nil {
		return rep, err
	}
	checks, err := k.Store.CoverChecks(ctx)
	if err != nil {
		return rep, err
	}
	due := Due(cands, checks, k.now(), k.MaxAge, k.Retry)
	rep.Left = len(due)
	for _, c := range due {
		if rep.Asked >= budget {
			break
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		rep.Asked++
		rep.Left--
		stored, err := k.readOne(ctx, c, checks[c.CIK], &rep)
		if err != nil {
			if stopsEverything(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return rep, err
			}
			rep.Bad++
			if perr := k.Store.PutCoverCheck(ctx, c.CIK, k.failed(checks[c.CIK], err)); perr != nil {
				return rep, perr
			}
			continue
		}
		if stored && k.Map != nil {
			if err := k.Map(ctx, c.CIK, k.now()); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

// Read reads the cover page of one issuer now, whether or not it is due, and
// maps its classes. stored says whether a report's counts were stored (false
// when the newest report was already stored).
func (k *CoverKeeper) Read(ctx context.Context, cik string) (rep CoverReport, stored bool, err error) {
	checks, err := k.Store.CoverChecks(ctx)
	if err != nil {
		return rep, false, err
	}
	rep.Asked = 1
	stored, err = k.readOne(ctx, CoverCandidate{CIK: cik}, checks[cik], &rep)
	if err != nil {
		if !stopsEverything(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			_ = k.Store.PutCoverCheck(ctx, cik, k.failed(checks[cik], err))
		}
		return rep, false, err
	}
	if stored && k.Map != nil {
		err = k.Map(ctx, cik, k.now())
	}
	return rep, stored, err
}

// failed is the check that records a failed look: one more failure in a row, and
// the time before which the issuer is not asked again.
func (k *CoverKeeper) failed(last CoverCheck, err error) CoverCheck {
	n := last.Failures + 1
	if last.Kind != CheckFailed {
		n = 1
	}
	now := k.now()
	return CoverCheck{At: now, Accession: last.Accession, Problem: err.Error(), Kind: CheckFailed, Failures: n, Next: now.Add(Backoff(k.Retry, n))}
}

func stopsEverything(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && (he.Class == httpx.Blocked || he.Class == httpx.Throttled)
}

// readOne looks at one issuer: its record of filings, then the cover page of
// its newest periodic report with a machine-readable one, unless that report is
// already stored.
func (k *CoverKeeper) readOne(ctx context.Context, c CoverCandidate, last CoverCheck, rep *CoverReport) (bool, error) {
	sub := k.SubmissionsURL
	if sub == "" {
		sub = company.SubmissionsURL
	}
	resp, err := k.Client.Do(ctx, httpx.Request{URL: fmt.Sprintf(sub, c.CIK)})
	rep.Requests++
	if err != nil {
		return false, err
	}
	filings, err := RecentFilings(resp.Body)
	if err != nil {
		return false, err
	}
	for _, f := range filings {
		if !f.Periodic() || f.Document == "" {
			continue
		}
		if f.Accession == last.Accession && last.Problem == "" {
			rep.Unchanged++
			return false, k.Store.PutCoverCheck(ctx, c.CIK, CoverCheck{At: k.now(), Accession: f.Accession})
		}
		classes, err := k.cover(ctx, f.DocumentURL(k.Archives, c.CIK), rep)
		if err != nil {
			return false, fmt.Errorf("%s %s: %w", f.Form, f.Accession, err)
		}
		if len(classes) == 0 {
			continue // no machine-readable cover page; the report before may have one
		}
		r := CoverRead{CIK: c.CIK, Accession: f.Accession, Form: f.Form, Filed: f.Filed, Classes: classes, At: k.now()}
		if t, ok := f.AcceptedTime(); ok {
			r.Accepted = t
		}
		rep.Stored++
		return true, k.Store.PutCover(ctx, r)
	}
	rep.Empty++
	now := k.now()
	maxAge := k.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultCoverAge
	}
	return false, k.Store.PutCoverCheck(ctx, c.CIK, CoverCheck{At: now, Kind: CheckNoCover, Next: now.Add(maxAge),
		Problem: "no annual or quarterly report with a machine-readable cover page among the recent filings"})
}

// cover reads the cover page of the document at url: from its start when that is
// enough, from the whole document when it is not.
func (k *CoverKeeper) cover(ctx context.Context, url string, rep *CoverReport) ([]ClassShares, error) {
	resp, err := k.Client.Do(ctx, httpx.Request{URL: url, Header: http.Header{"Range": {fmt.Sprintf("bytes=0-%d", coverPrefix-1)}}})
	rep.Requests++
	if err != nil {
		return nil, err
	}
	partial := resp.Status == http.StatusPartialContent
	classes, last, perr := ParseCoverAt(resp.Body)
	if !partial {
		// The server sent the whole document, as it does when it ignores ranges.
		return classes, perr
	}
	if perr == nil && len(classes) > 0 && float64(last) < coverMargin*float64(len(resp.Body)) {
		return classes, nil
	}
	rep.Full++
	rep.Requests++
	full, err := k.Client.Do(ctx, httpx.Request{URL: url})
	if err != nil {
		return nil, err
	}
	return ParseCover(full.Body)
}
