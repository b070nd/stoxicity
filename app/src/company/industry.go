package company

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/httpx"
)

// SubmissionsURL is the SEC's per-company record; %s is the ten-digit CIK. The
// record holds the industry (SIC code and its description), the state of
// incorporation, the fiscal year end, the filer category and former names, next
// to the filing history, which is not kept here. The SEC lets a program make
// ten requests a second and asks it to declare who it is; the HTTP client of
// this program sends one tenth of that and identifies itself.
const SubmissionsURL = "https://data.sec.gov/submissions/CIK%s.json"

// DefaultIndustryAge is how old an industry record may be before Enrich asks
// again. Companies change SIC code or name rarely.
const DefaultIndustryAge = 30 * 24 * time.Hour

const industryFile = "industry.json"

// Industry is what the SEC says about one company, besides its name and
// ticker.
type Industry struct {
	CIK         string   `json:"cik"`
	Name        string   `json:"name,omitempty"`
	SIC         string   `json:"sic,omitempty"`
	Description string   `json:"description,omitempty"`     // the SIC code in words, as the SEC writes it
	EntityType  string   `json:"entity_type,omitempty"`     // "operating", ...
	Category    string   `json:"category,omitempty"`        // "Large accelerated filer", ...
	State       string   `json:"state,omitempty"`           // of incorporation
	FiscalYear  string   `json:"fiscal_year_end,omitempty"` // "0926" is 26 September
	Website     string   `json:"website,omitempty"`
	Former      []string `json:"former_names,omitempty"`
	// Missing is true when the SEC had no record (404), so the question is not
	// asked again until the record is old.
	Missing   bool      `json:"missing,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
}

// IndustryCache holds the records by ten-digit CIK.
type IndustryCache map[string]Industry

// LoadIndustry reads the cache in dir. A missing file is an empty cache.
func LoadIndustry(dir string) (IndustryCache, error) {
	raw, err := os.ReadFile(filepath.Join(dir, industryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return IndustryCache{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("company: %w", err)
	}
	var c IndustryCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("company: %s is damaged (delete it and run \"stoxicity universe enrich\"): %w", industryFile, err)
	}
	return c, nil
}

func saveIndustry(dir string, c IndustryCache) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("company: %w", err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("company: %w", err)
	}
	return writeFile(filepath.Join(dir, industryFile), raw)
}

// submission is the part of the SEC record that is kept.
type submission struct {
	CIK         string `json:"cik"`
	EntityType  string `json:"entityType"`
	SIC         string `json:"sic"`
	SICDesc     string `json:"sicDescription"`
	Name        string `json:"name"`
	Website     string `json:"website"`
	Category    string `json:"category"`
	FiscalYear  string `json:"fiscalYearEnd"`
	State       string `json:"stateOfIncorporation"`
	FormerNames []struct {
		Name string `json:"name"`
	} `json:"formerNames"`
}

func parseSubmission(raw []byte, cik string) (Industry, error) {
	var s submission
	if err := json.Unmarshal(raw, &s); err != nil {
		return Industry{}, fmt.Errorf("company: read the record of CIK %s: %w", cik, err)
	}
	if s.CIK != "" && strings.TrimLeft(s.CIK, "0") != strings.TrimLeft(cik, "0") {
		return Industry{}, fmt.Errorf("company: the record for CIK %s is for CIK %s", cik, s.CIK)
	}
	in := Industry{
		CIK: cik, Name: s.Name, SIC: s.SIC, Description: s.SICDesc, EntityType: s.EntityType,
		Category: s.Category, State: s.State, FiscalYear: s.FiscalYear, Website: s.Website,
	}
	for _, f := range s.FormerNames {
		if f.Name != "" {
			in.Former = append(in.Former, f.Name)
		}
	}
	return in, nil
}

// EnrichOptions are the settings of Enrich.
type EnrichOptions struct {
	// URL replaces SubmissionsURL; tests point it at a server.
	URL string
	// MaxAge replaces DefaultIndustryAge.
	MaxAge time.Duration
	// Force asks even for records that are young.
	Force bool
	// Max is the most requests to make; 0 means no limit.
	Max int
	// Now is the clock; time.Now if nil.
	Now func() time.Time
	// Progress, if set, is told after every request how many are done of how
	// many will be made.
	Progress func(done, total int)
}

// EnrichResult says what Enrich did.
type EnrichResult struct {
	Fetched int      // records stored
	Skipped int      // records that were young enough
	Missing int      // CIKs the SEC has no record for
	Left    int      // CIKs not asked because of Max
	Failed  []string // one line per request that failed
}

// Enrich fetches the SEC record of each CIK that has none or an old one and
// stores the result in dir. The cache is written every 25 requests and at the
// end, so an interrupted run keeps its work. A request that fails is reported
// in the result and does not stop the others, except that a cancelled context
// stops everything; a rate limit or block from the SEC (httpx.Throttled and
// httpx.Blocked) also stops the run, because going on would make it worse.
func Enrich(ctx context.Context, client httpx.Client, dir string, ciks []string, opts EnrichOptions) (EnrichResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultIndustryAge
	}
	url := opts.URL
	if url == "" {
		url = SubmissionsURL
	}
	cache, err := LoadIndustry(dir)
	if err != nil {
		cache = IndustryCache{} // a damaged cache is replaced, not repaired
	}

	var todo []string
	var res EnrichResult
	seen := map[string]bool{}
	for _, c := range ciks {
		c = padCIK(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if old, ok := cache[c]; ok && !opts.Force && now().Sub(old.FetchedAt) < maxAge {
			res.Skipped++
			continue
		}
		todo = append(todo, c)
	}
	slices.Sort(todo)
	if opts.Max > 0 && len(todo) > opts.Max {
		res.Left = len(todo) - opts.Max
		todo = todo[:opts.Max]
	}

	dirty := 0
	flush := func() error {
		if dirty == 0 {
			return nil
		}
		dirty = 0
		return saveIndustry(dir, cache)
	}
	for i, c := range todo {
		if err := ctx.Err(); err != nil {
			return res, errors.Join(err, flush())
		}
		resp, err := client.Do(ctx, httpx.Request{URL: fmt.Sprintf(url, c)})
		var he *httpx.Error
		switch {
		case errors.As(err, &he) && he.Status == 404:
			cache[c] = Industry{CIK: c, Missing: true, FetchedAt: now().UTC()}
			res.Missing++
			dirty++
		case errors.As(err, &he) && (he.Class == httpx.Throttled || he.Class == httpx.Blocked):
			res.Left += len(todo) - i
			return res, errors.Join(fmt.Errorf("company: the SEC is pushing back, stopping: %w", err), flush())
		case err != nil:
			res.Failed = append(res.Failed, fmt.Sprintf("CIK %s: %v", c, err))
		default:
			in, perr := parseSubmission(resp.Body, c)
			if perr != nil {
				res.Failed = append(res.Failed, perr.Error())
				break
			}
			in.FetchedAt = now().UTC()
			cache[c] = in
			res.Fetched++
			dirty++
		}
		if opts.Progress != nil {
			opts.Progress(i+1, len(todo))
		}
		if dirty >= 25 {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	return res, flush()
}

// padCIK writes a CIK as ten digits; it returns "" for anything else.
func padCIK(c string) string {
	c = strings.TrimSpace(c)
	if c == "" || len(c) > 10 || strings.Trim(c, "0123456789") != "" {
		return ""
	}
	return strings.Repeat("0", 10-len(c)) + c
}

// IndustryCIKs lists the CIKs in the cache, sorted.
func (c IndustryCache) CIKs() []string { return slices.Sorted(maps.Keys(c)) }
