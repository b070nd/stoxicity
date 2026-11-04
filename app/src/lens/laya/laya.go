// Package laya is the client for a laya-serve sidecar (POST /v1/systemone/batch),
// the second scoring stage after the keyword rules. laya answers typed
// questions in one pass, so a reply carries probabilities, not text. The
// package turns the answers into lens.Opinion values, which the lens combines
// with the rule-based score under its own policy.
//
// It is meant to be switched on only after calibration against the user's own
// labels (package eval): zero-shot checkpoints are not trusted, and the
// confidences returned here are the model's own numbers, passed on as they are.
// An answer the model did not give, or gave in a shape this package does not
// know, becomes a judgement with confidence zero, never a guess.
//
// Facts to keep in mind (docs/research/bleeding-edge-design.md, 3.2):
//   - laya is one candidate in a comparison with other scorers, not the default.
//   - laya-serve listens on 0.0.0.0 unless LAYA_HOST=127.0.0.1 is set; set
//     LAYA_API_KEY as well.
//   - The environment's installed laya (0.4.0) is behind upstream (0.4.22 on
//     2026-11-03). Pin a version and re-check its API before relying on it.
//   - A choice question should have about 20 options at most, so the event
//     group is asked first (model.EventGroup) and the kind within that group
//     second, in a second round of requests.
//
// The input is the article's title plus the start of its summary; checkpoints
// read 512 to 1024 tokens. The article's URL is never sent.
package laya

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

const (
	defaultTimeout   = 60 * time.Second
	defaultBatchSize = 16
	maxBatchSize     = 64
)

// Config says where the server is and how to use it. Only URL is required.
type Config struct {
	// URL is the base address of laya-serve, such as http://127.0.0.1:8000.
	URL string
	// Model names the checkpoint. Empty lets the server choose.
	Model string
	// APIKey is sent as a Bearer token when the server needs one. It is never
	// put in a URL, an error or a log.
	APIKey string
	// Timeout limits each HTTP request. Zero means 60 seconds.
	Timeout time.Duration
	// BatchSize is how many articles go into one request. Zero means 16; the
	// server accepts at most 64.
	BatchSize int
	// Topics are the themes the topic question chooses from, in addition to
	// "none". Empty leaves the question out.
	Topics []string
	// Sectors are the sectors the sector question chooses from, in addition to
	// "macro" and "none". Empty leaves the question out.
	Sectors []string
	// HTTPClient is used for requests. The default ignores proxy settings in
	// the environment. Redirects are never followed, whatever the client says.
	HTTPClient *http.Client
}

// Scorer asks a laya-serve for opinions about articles. It is safe for
// concurrent use.
type Scorer struct {
	batchURL  string
	healthURL string
	model     string
	apiKey    string
	timeout   time.Duration
	batchSize int
	client    *http.Client
	questions questionSet
	sectorSet map[string]bool
	topicSet  map[string]bool
	version   string

	// sleep waits between attempts; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// New checks the configuration and returns a Scorer. It does not contact the
// server; use Ping for that. Errors never contain the URL or the key.
func New(cfg Config) (*Scorer, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil {
		return nil, errors.New("laya: the url is not valid")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("laya: the url must use http or https")
	case u.Host == "" || u.Hostname() == "":
		return nil, errors.New("laya: the url must have a host")
	case u.User != nil:
		return nil, errors.New("laya: the url must not contain a user name or password")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return nil, errors.New("laya: the url must not contain a query or a fragment")
	}
	if strings.IndexFunc(cfg.APIKey, unicode.IsControl) >= 0 {
		return nil, errors.New("laya: the api key contains control characters")
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("laya: the timeout must not be negative")
	}
	if cfg.BatchSize < 0 || cfg.BatchSize > maxBatchSize {
		return nil, errors.New("laya: the batch size must be between 1 and 64")
	}

	s := &Scorer{
		batchURL:  u.JoinPath(batchPath).String(),
		healthURL: u.JoinPath(healthPath).String(),
		model:     strings.TrimSpace(cfg.Model),
		apiKey:    cfg.APIKey,
		timeout:   cfg.Timeout,
		batchSize: cfg.BatchSize,
		client:    newHTTPClient(cfg.HTTPClient),
		questions: buildQuestions(cfg.Sectors, cfg.Topics),
		sleep:     sleepContext,
	}
	if s.timeout == 0 {
		s.timeout = defaultTimeout
	}
	if s.batchSize == 0 {
		s.batchSize = defaultBatchSize
	}
	s.sectorSet = nameSet(s.questions.sectors)
	s.topicSet = nameSet(s.questions.topics)

	name := s.model
	if name == "" {
		name = "auto"
	}
	s.version = "laya-" + name + "-q" + s.questions.fingerprint()
	return s, nil
}

// newHTTPClient returns a copy of c, or a default client, that does not follow
// redirects: a redirect could carry the API key to another host.
func newHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		c = &http.Client{Transport: tr}
	}
	cp := *c
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cp
}

func nameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Version identifies the checkpoint and the question set, as
// "laya-<model or auto>-q<8 hex digits>". The digits change when any question,
// its instructions or criteria, or the topic or sector lists change; the order
// of the lists does not matter.
func (s *Scorer) Version() string { return s.version }

// Opinions returns one opinion per article, in the order of articles. Articles
// go to the server in batches of BatchSize. The first round asks about
// relevance, event group, materiality, tone and, when configured, sector and
// topic; the second round asks for the event kind within the predicted group.
//
// An article with no title and no summary gets the zero Opinion without a
// call. A failing batch returns its error and no opinions, except that a batch
// the server refuses as invalid or too large (422, 413) is retried state by
// state, and an article refused on its own gets the zero Opinion.
func (s *Scorer) Opinions(ctx context.Context, articles []model.Article) ([]lens.Opinion, error) {
	out := make([]lens.Opinion, len(articles))
	states := make([]string, len(articles))
	var todo []int
	for i, a := range articles {
		if states[i] = state(a); states[i] != "" {
			todo = append(todo, i)
		}
	}

	byGroup := make(map[model.EventGroup][]int)
	for _, chunk := range chunks(todo, s.batchSize) {
		results, ok, err := s.ask(ctx, pick(states, chunk), s.questions.round1)
		if err != nil {
			return nil, err
		}
		for j, i := range chunk {
			if !ok[j] {
				continue
			}
			out[i] = s.firstRound(results[j])
			if g := out[i].Group; g != "" {
				byGroup[g] = append(byGroup[g], i)
			}
		}
	}

	for _, g := range groupOrder {
		for _, chunk := range chunks(byGroup[g], s.batchSize) {
			results, ok, err := s.ask(ctx, pick(states, chunk), s.questions.kinds[g])
			if err != nil {
				return nil, err
			}
			for j, i := range chunk {
				if ok[j] {
					s.secondRound(&out[i], g, results[j])
				}
			}
		}
	}
	return out, nil
}

// ask sends the states in one request. When the server refuses the request as
// invalid or too large and there is more than one state, it asks again for each
// state alone; a state refused alone is reported as not ok instead of failing.
func (s *Scorer) ask(ctx context.Context, states []string, qs map[string]question) ([]result, []bool, error) {
	results := make([]result, len(states))
	ok := make([]bool, len(states))
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	res, err := s.batch(ctx, states, qs)
	if err == nil {
		copy(results, res)
		for i := range ok {
			ok[i] = true
		}
		return results, ok, nil
	}
	if !refusedInput(err) {
		return nil, nil, err
	}
	if len(states) == 1 {
		return results, ok, nil
	}
	for i := range states {
		one, err := s.batch(ctx, states[i:i+1], qs)
		switch {
		case err == nil:
			results[i], ok[i] = one[0], true
		case !refusedInput(err):
			return nil, nil, err
		}
	}
	return results, ok, nil
}

// refusedInput reports whether err is the server saying the request, not the
// server, is the problem: 422 or 413.
func refusedInput(err error) bool {
	var se *statusError
	return errors.As(err, &se) && (se.code == http.StatusUnprocessableEntity || se.code == http.StatusRequestEntityTooLarge)
}

// chunks splits idx into slices of at most n.
func chunks(idx []int, n int) [][]int {
	var out [][]int
	for len(idx) > 0 {
		k := min(n, len(idx))
		out = append(out, idx[:k])
		idx = idx[k:]
	}
	return out
}

// pick returns the states at the given positions.
func pick(states []string, idx []int) []string {
	out := make([]string, len(idx))
	for j, i := range idx {
		out[j] = states[i]
	}
	return out
}
