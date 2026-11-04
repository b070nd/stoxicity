// Package sources holds the curated registry of news sources. Only sources a
// long-term investor would read belong here: filings, company reports,
// central banks and serious reporting. Every source states what its licence
// allows, and the registry hides a source from tenants that may not see it.
package sources

import (
	"errors"
	"fmt"
	"sort"

	"github.com/b070nd/stoxicity/app/src/model"
)

// Registry is the loaded set of sources, keyed by source ID.
type Registry map[string]model.Source

// Enabled returns the sources that should be polled, sorted by ID.
func (r Registry) Enabled() []model.Source {
	var out []model.Source
	for _, s := range r {
		if s.Enabled {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VisibleTo returns the IDs of the sources a tenant may see. Readers of the
// shared base use it to drop articles from sources whose licence forbids
// showing them to that tenant.
func (r Registry) VisibleTo(tenantID string) map[string]bool {
	ids := map[string]bool{}
	for id, s := range r {
		if s.VisibleTo(tenantID) {
			ids[id] = true
		}
	}
	return ids
}

// Check validates every source, enabled or not, and reports all problems. A
// source listed under the wrong key is a problem too.
func (r Registry) Check() error {
	var errs []error
	for key, s := range r {
		if key != s.ID {
			errs = append(errs, fmt.Errorf("source %q is listed under key %q", s.ID, key))
		}
		if err := s.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
