package edgar

import (
	"context"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Form4 follows insider transactions. docs/fetchers.md (section 7.4) plans to
// parse the Form 4 XML into insider purchases and sales but does not say how
// the XML or a structured data set is reached, and marks it unverified, so for
// now Form4 is the current-filings reader narrowed to Form 4 (and 4/A): it
// reports that a Form 4 was filed, by whom and for which company, and nothing
// about the transaction. Open market purchases by officers matter most;
// option exercises and tax withholding are noise, and telling them apart needs
// the XML.
type Form4 struct {
	current *Current
}

// NewForm4 returns a Form4 reader that makes all requests through client.
func NewForm4(client httpx.Client, cfg Config) *Form4 {
	return &Form4{current: NewCurrent(client, cfg)}
}

// Fetch reads the current-filings feed for Form 4 whatever src.Forms says.
func (f *Form4) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	src.Forms = []string{"4"}
	return f.current.Fetch(ctx, src, prev)
}
