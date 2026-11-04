package main

import (
	"github.com/b070nd/stoxicity/app/src/reader"
)

// reader opens the store and returns a reader for the selected tenant, bound
// to the sources the tenant may see and to the configured time zone. Commands
// that show articles read through it and never touch the store directly, so
// the licence rule is applied in one place.
func (a *app) reader() (*reader.Reader, error) {
	db, err := a.store()
	if err != nil {
		return nil, err
	}
	return reader.New(db, a.tenantID, a.visible(), a.location(),
		reader.WithNow(a.now), reader.WithSince(a.cfg.SinceTime())), nil
}
