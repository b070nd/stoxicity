// Package insider keeps what the Form 4 filings say about who in a company is
// buying or selling its shares, and turns it into figures a query can compare.
//
// The feed of filings (package edgar) only says that a Form 4 was filed. The
// Keeper reads the filings it has not read, newest first, a budget of them per
// cycle, one small request each, and stores their trades. Open market
// purchases (code P) and sales (code S) are the signal; awards, exercises, tax
// withholding and gifts are stored too, so a page can show them, but they are
// not counted.
package insider

import (
	"context"
	"errors"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds/edgar"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Window is how far back an unread filing is still worth reading: older ones
// no longer count towards the 90-day figures and are left alone.
const Window = 120 * 24 * time.Hour

// Filing is a Form 4 that was found in the feed.
type Filing struct {
	Accession string
	CIK       string // the issuer, ten digits
	Filed     string // YYYY-MM-DD
}

// Store keeps the trades.
type Store interface {
	// UnreadForm4 lists the Form 4 filings published since the time that have not
	// been read, newest first, up to limit, and says how many there are in all.
	UnreadForm4(ctx context.Context, since time.Time, limit int) (list []Filing, total int, err error)
	// PutInsider stores the trades of a filing and notes the filing as read;
	// problem is empty or why it could not be read.
	PutInsider(ctx context.Context, f Filing, trades []model.InsiderTrade, problem string, at time.Time) error
}

// Reader reads the trades of one filing.
type Reader interface {
	Trades(ctx context.Context, acc edgar.Accession, cik, filed string) ([]model.InsiderTrade, error)
}

// Report says what one refresh did.
type Report struct {
	// Asked is the number of filings read, Trades the trades they held, Empty the
	// filings that held none in ordinary shares, Bad the filings that could not be
	// read, Left the filings still waiting.
	Asked, Trades, Empty, Bad, Left int
}

// Keeper reads the filings that have not been read.
type Keeper struct {
	Store  Store
	Reader Reader
	Now    func() time.Time
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

// Refresh reads up to budget unread filings. A rate limit or block from the SEC
// stops it at once and is returned with what was done so far; a filing that
// cannot be read is noted as bad and not asked again.
func (k *Keeper) Refresh(ctx context.Context, budget int) (Report, error) {
	var rep Report
	if budget <= 0 {
		return rep, nil
	}
	list, total, err := k.Store.UnreadForm4(ctx, k.now().Add(-Window), budget)
	if err != nil {
		return rep, err
	}
	rep.Left = total
	for _, f := range list {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		acc, perr := edgar.ParseAccession(f.Accession)
		var trades []model.InsiderTrade
		if perr == nil {
			trades, perr = k.Reader.Trades(ctx, acc, f.CIK, f.Filed)
		}
		if perr != nil {
			if edgar.Blocked(perr) || errors.Is(perr, context.Canceled) || errors.Is(perr, context.DeadlineExceeded) {
				return rep, perr
			}
			// A failure of the connection is worth asking again; one of the filing is not.
			if !permanent(perr) {
				return rep, perr
			}
			if err := k.Store.PutInsider(ctx, f, nil, perr.Error(), k.now()); err != nil {
				return rep, err
			}
			rep.Asked++
			rep.Bad++
			rep.Left--
			continue
		}
		if err := k.Store.PutInsider(ctx, f, trades, "", k.now()); err != nil {
			return rep, err
		}
		rep.Asked++
		rep.Left--
		rep.Trades += len(trades)
		if len(trades) == 0 {
			rep.Empty++
		}
	}
	return rep, nil
}

// permanent says that asking again would not help: the SEC has no such filing
// (404) or the filing is not a readable ownership document.
func permanent(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Class == httpx.Permanent
}
