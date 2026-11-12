package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/b070nd/stoxicity/app/src/eventstudy"
	"github.com/b070nd/stoxicity/app/src/playbook"
	"github.com/b070nd/stoxicity/app/src/store"
)

// The kinds of record the lab keeps. Studies and playbooks are versioned: a
// changed body under the same key is a new revision, and no revision is
// overwritten.
const (
	studyKind    = "study.spec"
	studyRunKind = "study.run"
	playbookKind = "playbook.def"
	signalKind   = "playbook.signal"
)

// Records is what the lab needs of the record store; store.Records satisfies it.
type Records interface {
	Append(ctx context.Context, in ...store.RecordInput) ([]store.Record, int, error)
	Latest(ctx context.Context, kind, key string) (store.Record, error)
	History(ctx context.Context, kind, key string) ([]store.Record, error)
	Current(ctx context.Context, kind, prefix string) ([]store.Record, error)
	All(ctx context.Context, kind, prefix string) ([]store.Record, error)
}

var _ Records = store.Records{}

// Lab keeps the registered studies and playbooks, the runs of the studies and
// the signals that were saved. It holds no state of its own: everything is in
// the records, and the callers say what time it is.
type Lab struct{ Records Records }

// Run is what is kept of one run of a study. The outcomes are not kept: they
// can be large, and a run prints them on request.
type Run struct {
	At       time.Time           `json:"at"`
	Run      int                 `json:"run"`
	SpecHash string              `json:"spec_hash"`
	DataHash string              `json:"data_hash"`
	Manifest eventstudy.Manifest `json:"manifest"`
	Prices   PriceResult         `json:"prices"`
	Report   eventstudy.Report   `json:"report"`
}

// SavedSignal is a signal as kept, with the answer of the risk layer.
type SavedSignal struct {
	Signal   playbook.Signal   `json:"signal"`
	Decision playbook.Decision `json:"decision"`
}

// Registration says what registering a study or a playbook did.
type Registration struct {
	// Revision is the number of the newest revision after the call.
	Revision int
	// Existed is true when the id had been registered before.
	Existed bool
	// Unchanged is true when the newest revision already held exactly this
	// content, so nothing was written.
	Unchanged bool
}

// StudySummary is one registered study as the list shows it.
type StudySummary struct {
	Spec      eventstudy.Spec
	Revisions int
	Runs      int
	// LastStatus is the status of the newest run, or empty when it never ran.
	LastStatus string
}

// PlaybookSummary is one registered playbook as the list shows it.
type PlaybookSummary struct {
	Playbook  playbook.Playbook
	Revisions int
}

// register stores body as the newest revision of a key.
func (l Lab) register(ctx context.Context, kind, key string, body []byte) (Registration, error) {
	hist, err := l.Records.History(ctx, kind, key)
	if err != nil {
		return Registration{}, err
	}
	if n := len(hist); n > 0 && hist[n-1].Hash == store.RecordHash(body) {
		return Registration{Revision: n, Existed: true, Unchanged: true}, nil
	}
	_, fresh, err := l.Records.Append(ctx, store.RecordInput{Kind: kind, Key: key, Body: body})
	if err != nil {
		return Registration{}, err
	}
	if fresh == 0 {
		return Registration{}, fmt.Errorf("research: %s was registered with exactly this content before and cannot be made the newest revision again: change something, or register it under a new id", key)
	}
	return Registration{Revision: len(hist) + 1, Existed: len(hist) > 0}, nil
}

// RegisterStudy checks the study and stores it as the newest revision of its
// id.
func (l Lab) RegisterStudy(ctx context.Context, spec eventstudy.Spec) (Registration, error) {
	if err := spec.Validate(); err != nil {
		return Registration{}, err
	}
	body, err := json.Marshal(spec)
	if err != nil {
		return Registration{}, fmt.Errorf("research: study %s: %w", spec.ID, err)
	}
	return l.register(ctx, studyKind, spec.ID, body)
}

// RegisterPlaybook checks the playbook and stores it as the newest revision of
// its id.
func (l Lab) RegisterPlaybook(ctx context.Context, p playbook.Playbook) (Registration, error) {
	if err := p.Validate(); err != nil {
		return Registration{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return Registration{}, fmt.Errorf("research: playbook %s: %w", p.ID, err)
	}
	return l.register(ctx, playbookKind, p.ID, body)
}

// StudyCounts says how many studies and how many study revisions are
// registered.
func (l Lab) StudyCounts(ctx context.Context) (studies, revisions int, err error) {
	cur, err := l.Records.Current(ctx, studyKind, "")
	if err != nil {
		return 0, 0, err
	}
	all, err := l.Records.All(ctx, studyKind, "")
	if err != nil {
		return 0, 0, err
	}
	return len(cur), len(all), nil
}

// HasStudy says whether a study is registered under the id.
func (l Lab) HasStudy(ctx context.Context, id string) (bool, error) {
	_, err := l.Records.Latest(ctx, studyKind, id)
	if errors.Is(err, store.ErrNoRecord) {
		return false, nil
	}
	return err == nil, err
}

// LatestStudy is the newest revision of a registered study and its number.
func (l Lab) LatestStudy(ctx context.Context, id string) (eventstudy.Spec, int, error) {
	rec, err := l.Records.Latest(ctx, studyKind, id)
	if errors.Is(err, store.ErrNoRecord) {
		return eventstudy.Spec{}, 0, fmt.Errorf("research: no study %s: register it first (study register -file F)", id)
	}
	if err != nil {
		return eventstudy.Spec{}, 0, err
	}
	var spec eventstudy.Spec
	if err := json.Unmarshal(rec.Body, &spec); err != nil {
		return eventstudy.Spec{}, 0, fmt.Errorf("research: study %s does not decode: %w", id, err)
	}
	hist, err := l.Records.History(ctx, studyKind, id)
	if err != nil {
		return eventstudy.Spec{}, 0, err
	}
	return spec, len(hist), nil
}

// LatestPlaybook is the newest revision of a registered playbook and its
// number.
func (l Lab) LatestPlaybook(ctx context.Context, id string) (playbook.Playbook, int, error) {
	rec, err := l.Records.Latest(ctx, playbookKind, id)
	if errors.Is(err, store.ErrNoRecord) {
		return playbook.Playbook{}, 0, fmt.Errorf("research: no playbook %s: register it first (playbook register -file F)", id)
	}
	if err != nil {
		return playbook.Playbook{}, 0, err
	}
	var p playbook.Playbook
	if err := json.Unmarshal(rec.Body, &p); err != nil {
		return playbook.Playbook{}, 0, fmt.Errorf("research: playbook %s does not decode: %w", id, err)
	}
	hist, err := l.Records.History(ctx, playbookKind, id)
	if err != nil {
		return playbook.Playbook{}, 0, err
	}
	return p, len(hist), nil
}

// Studies lists the newest revision of every registered study, ordered by id,
// with how many revisions and runs it has.
func (l Lab) Studies(ctx context.Context) ([]StudySummary, error) {
	cur, err := l.Records.Current(ctx, studyKind, "")
	if err != nil {
		return nil, err
	}
	out := make([]StudySummary, 0, len(cur))
	for _, r := range cur {
		var spec eventstudy.Spec
		if err := json.Unmarshal(r.Body, &spec); err != nil {
			return nil, fmt.Errorf("research: study %s does not decode: %w", r.Key, err)
		}
		hist, err := l.Records.History(ctx, studyKind, r.Key)
		if err != nil {
			return nil, err
		}
		runs, err := l.Runs(ctx, r.Key)
		if err != nil {
			return nil, err
		}
		s := StudySummary{Spec: spec, Revisions: len(hist), Runs: len(runs)}
		if n := len(runs); n > 0 {
			s.LastStatus = runs[n-1].Report.Status
		}
		out = append(out, s)
	}
	return out, nil
}

// Playbooks lists the newest revision of every registered playbook, ordered by
// id, with how many revisions it has.
func (l Lab) Playbooks(ctx context.Context) ([]PlaybookSummary, error) {
	cur, err := l.Records.Current(ctx, playbookKind, "")
	if err != nil {
		return nil, err
	}
	out := make([]PlaybookSummary, 0, len(cur))
	for _, r := range cur {
		var p playbook.Playbook
		if err := json.Unmarshal(r.Body, &p); err != nil {
			return nil, fmt.Errorf("research: playbook %s does not decode: %w", r.Key, err)
		}
		hist, err := l.Records.History(ctx, playbookKind, r.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, PlaybookSummary{Playbook: p, Revisions: len(hist)})
	}
	return out, nil
}

// Runs lists the stored runs of a study, oldest first.
func (l Lab) Runs(ctx context.Context, studyID string) ([]Run, error) {
	recs, err := l.Records.History(ctx, studyRunKind, studyID)
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(recs))
	for _, r := range recs {
		var x Run
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, fmt.Errorf("research: a run of study %s does not decode: %w", studyID, err)
		}
		out = append(out, x)
	}
	return out, nil
}

// RecordRun keeps one run of a study.
func (l Lab) RecordRun(ctx context.Context, studyID string, r Run) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("research: run of study %s: %w", studyID, err)
	}
	_, _, err = l.Records.Append(ctx, store.RecordInput{Kind: studyRunKind, Key: studyID, Body: body})
	return err
}

// ValidationReads counts the earlier runs that read the validation window and
// says when the first of them ran. A pattern chosen after looking at
// validation is not confirmed by it, so the count is shown before a run.
func ValidationReads(runs []Run) (reads int, first time.Time) {
	for _, r := range runs {
		if r.Report.Spec.Validation.From == "" && r.Report.Spec.Validation.To == "" {
			continue
		}
		if reads == 0 {
			first = r.At
		}
		reads++
	}
	return reads, first
}

// SaveSignals keeps signals with the answer of the risk layer. A signal that
// was kept before, with the same content, is left alone; fresh counts the new
// ones.
func (l Lab) SaveSignals(ctx context.Context, signals []SavedSignal) (fresh int, err error) {
	if len(signals) == 0 {
		return 0, nil
	}
	in := make([]store.RecordInput, 0, len(signals))
	for _, s := range signals {
		body, err := json.Marshal(s)
		if err != nil {
			return 0, fmt.Errorf("research: signal %s: %w", s.Signal.ID, err)
		}
		in = append(in, store.RecordInput{Kind: signalKind, Key: s.Signal.ID, Body: body})
	}
	_, fresh, err = l.Records.Append(ctx, in...)
	return fresh, err
}

// SavedSignals lists the saved signals, newest first. A playbook id limits it
// to the signals of that playbook; an empty one lists all of them.
func (l Lab) SavedSignals(ctx context.Context, playbookID string) ([]SavedSignal, error) {
	recs, err := l.Records.Current(ctx, signalKind, "")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Seq > recs[j].Seq })
	var out []SavedSignal
	for _, r := range recs {
		var x SavedSignal
		if err := json.Unmarshal(r.Body, &x); err != nil {
			return nil, fmt.Errorf("research: saved signal %s does not decode: %w", r.Key, err)
		}
		if playbookID != "" && x.Signal.Playbook != playbookID {
			continue
		}
		out = append(out, x)
	}
	return out, nil
}
