package edgar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/feeds"
	"github.com/b070nd/stoxicity/app/src/httpx"
	"github.com/b070nd/stoxicity/app/src/model"
)

// Submissions reads {DataBase}/submissions/CIK<10 digits>.json for each
// followed company: recent filings with their form, date and, where present,
// 8-K item numbers. Used to fill in what the Atom feed lacks and to back-fill a
// company.
//
// The companies of a source come from its URL when that is a submissions URL
// (https://data.sec.gov/submissions/CIK0000320193.json), and from tags of the
// form "cik:320193". Only DataBase and WebBase of Config decide which host is
// asked; the host in the source URL is ignored. The ETag and Last-Modified of
// each company are kept in State.Cursor as JSON, since State has one pair of
// fields; with a single company they are also in State.ETag and
// State.LastModified.
type Submissions struct {
	client httpx.Client
	cfg    Config
}

// NewSubmissions returns a Submissions reader that makes all requests through client.
func NewSubmissions(client httpx.Client, cfg Config) *Submissions {
	return &Submissions{client: client, cfg: cfg.withDefaults()}
}

// validators are the conditional-GET headers last seen for one company.
type validators struct {
	ETag         string `json:"e,omitempty"`
	LastModified string `json:"m,omitempty"`
}

type submissionsFile struct {
	Name    string   `json:"name"`
	Tickers []string `json:"tickers"`
	Filings struct {
		Recent struct {
			AccessionNumber    []string `json:"accessionNumber"`
			FilingDate         []string `json:"filingDate"`
			AcceptanceDateTime []string `json:"acceptanceDateTime"`
			Form               []string `json:"form"`
			Items              []string `json:"items"`
			PrimaryDocument    []string `json:"primaryDocument"`
		} `json:"recent"`
	} `json:"filings"`
}

var (
	cikInURL  = regexp.MustCompile(`(?i)/submissions/CIK(\d{1,10})\.json`)
	tickerSym = regexp.MustCompile(`^[A-Z0-9.\-]{1,12}$`)
)

// sourceCIKs lists the companies a source follows, as ten digits each.
func sourceCIKs(src model.Source) []string {
	var out []string
	add := func(raw string) {
		if cik := padCIK(raw); cik != "" && !slices.Contains(out, cik) {
			out = append(out, cik)
		}
	}
	if m := cikInURL.FindStringSubmatch(src.URL); m != nil {
		add(m[1])
	}
	for _, tag := range src.Tags {
		if v, ok := strings.CutPrefix(tag, "cik:"); ok {
			add(v)
		}
	}
	return out
}

// Fetch implements feeds.Fetcher. It is NotModified only if every company
// answered 304. A company that fails does not hide the others, unless the SEC
// blocked or throttled the request; its filings are read on the next poll.
func (s *Submissions) Fetch(ctx context.Context, src model.Source, prev feeds.State) (feeds.Result, error) {
	ciks := sourceCIKs(src)
	if len(ciks) == 0 {
		return feeds.Result{}, permanentError(src.URL, 0,
			fmt.Errorf("edgar: source %q names no company: put a /submissions/CIK##########.json URL or cik:<number> tags in it", src.ID))
	}
	var stored map[string]validators
	if prev.Cursor != "" {
		_ = json.Unmarshal([]byte(prev.Cursor), &stored) // a cursor from elsewhere only costs a full read
	}
	next := make(map[string]validators, len(ciks))
	known := make(map[string]bool, len(prev.Seen))
	for _, id := range prev.Seen {
		known[id] = true
	}
	now := s.cfg.Now().UTC()

	var (
		articles []model.Article
		firstErr error
		finished int
		changed  bool
	)
	for _, cik := range ciks {
		v := stored[cik]
		if v == (validators{}) && len(ciks) == 1 {
			v = validators{ETag: prev.ETag, LastModified: prev.LastModified}
		}
		next[cik] = v
		endpoint := s.cfg.DataBase + "/submissions/CIK" + cik + ".json"
		resp, err := s.client.Do(ctx, httpx.Request{
			URL:          endpoint,
			Header:       http.Header{"Accept": {"application/json"}},
			ETag:         v.ETag,
			LastModified: v.LastModified,
		})
		if err == nil && !resp.NotModified && resp.Status != http.StatusNotModified {
			if looksLikeHTML(resp.Body) {
				err = blockedError(endpoint, resp.Status)
			} else {
				var found []model.Article
				if found, err = s.articles(src, cik, resp.Body, known, now); err != nil {
					err = permanentError(endpoint, resp.Status, err)
				} else {
					articles = append(articles, found...)
					next[cik] = validators{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
					changed = true
				}
			}
		}
		if err != nil {
			if mustStop(err) {
				return feeds.Result{}, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		finished++
	}
	if finished == 0 {
		return feeds.Result{}, firstErr
	}
	if !changed && firstErr == nil {
		return feeds.Result{State: prev, NotModified: true}, nil
	}

	slices.SortStableFunc(articles, func(a, b model.Article) int { return b.Published.Compare(a.Published) })
	ids := make([]string, len(articles))
	for i, a := range articles {
		ids[i] = a.ID
	}
	state := feeds.State{Seen: mergeSeen(ids, prev.Seen, s.cfg.SeenCap)}
	cursor, err := json.Marshal(next)
	if err != nil {
		return feeds.Result{}, permanentError(src.URL, 0, fmt.Errorf("edgar: encode cursor: %w", err))
	}
	state.Cursor = string(cursor)
	if len(ciks) == 1 {
		state.ETag, state.LastModified = next[ciks[0]].ETag, next[ciks[0]].LastModified
	}
	return feeds.Result{Articles: articles, State: state}, nil
}

// articles turns the recent filings of one company into articles, newest first,
// leaving out accession numbers that are known, malformed or of an unwanted form.
func (s *Submissions) articles(src model.Source, cik string, body []byte, known map[string]bool, now time.Time) ([]model.Article, error) {
	var file submissionsFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("edgar: parse submissions for CIK %s: %w", cik, err)
	}
	r := file.Filings.Recent
	at := func(list []string, i int) string {
		if i < len(list) {
			return strings.TrimSpace(list[i])
		}
		return ""
	}
	company := cleanText(file.Name)
	tickers := cleanTickers(file.Tickers)
	if len(tickers) == 0 {
		tickers = slices.Clone(s.cfg.Tickers[cik])
	}

	var out []model.Article
	for i, raw := range r.AccessionNumber {
		if len(out) >= s.cfg.MaxFilings {
			break
		}
		acc, err := ParseAccession(strings.TrimSpace(raw))
		if err != nil || known[string(acc)] {
			continue
		}
		form := cleanText(at(r.Form, i))
		if form == "" || !keepForm(form, src.Forms) {
			continue
		}
		t, ok := parseTime(at(r.AcceptanceDateTime, i))
		if !ok {
			t, ok = parseTime(at(r.FilingDate, i))
		}
		pub, reasons := published(t, ok, now)
		items := ParseItems(at(r.Items, i))

		title := form
		if company != "" {
			title += " - " + company
		}
		summary := "Form " + form
		if d := at(r.FilingDate, i); d != "" {
			summary += " filed " + cleanText(d)
		}
		if len(items) > 0 {
			summary += ", items " + strings.Join(items, ", ")
		}
		a := model.Article{
			ID:        string(acc),
			SourceID:  src.ID,
			Domain:    domain,
			URL:       s.documentURL(cik, acc, at(r.PrimaryDocument, i)),
			Title:     title,
			Summary:   summary,
			Published: pub,
			Fetched:   now,
			Form:      form,
			CIK:       padCIK(cik),
			Items:     items,
			Tickers:   tickers,
		}
		a.Score.Reasons = reasons
		out = append(out, a)
	}
	return out, nil
}

// documentURL is the filing's primary document, or its index page when the
// submissions file names none. The documents live at
// /Archives/edgar/data/<CIK without zeros>/<accession without dashes>/<file>.
func (s *Submissions) documentURL(cik string, acc Accession, primary string) string {
	if primary == "" || strings.ContainsAny(primary, "/\\") {
		return FilingIndexURL(cik, acc)
	}
	return s.cfg.WebBase + "/Archives/edgar/data/" + strings.TrimLeft(cik, "0") + "/" +
		strings.ReplaceAll(string(acc), "-", "") + "/" + url.PathEscape(primary)
}

// cleanTickers keeps well-formed upper-case symbols only.
func cleanTickers(in []string) []string {
	var out []string
	for _, t := range in {
		t = strings.ToUpper(strings.TrimSpace(t))
		if tickerSym.MatchString(t) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
