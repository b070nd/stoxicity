package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/store"
)

// The sidecar below speaks the wire format of laya-serve (POST
// /v1/systemone/batch and GET /health); the answer values are made up.

func choice(key string, conf float64) map[string]any {
	return map[string]any{"type": "choice", "choice": key, "answer_confidence": conf, "probabilities": map[string]float64{key: conf}}
}

func noul(p, conf float64) map[string]any {
	return map[string]any{"type": "noul", "noul": p, "answer_confidence": conf}
}

func score(v, conf float64) map[string]any {
	return map[string]any{
		"type": "score", "score": v, "answer_confidence": conf,
		"probabilities": map[string]float64{"0": 0.1, "1": 0.2, "2": 0.3, "3": 0.2, "4": 0.2},
	}
}

// sidecar is a fake laya-serve that gives the same answers for every article.
type sidecar struct {
	*httptest.Server
	mu      sync.Mutex
	batches []int // number of articles of each batch request
	auth    []string
}

// confidentAnswers is a sidecar's answer to every question: a non-reliance
// restatement in the technology sector with a topic, all with high confidence,
// except the relevance question.
func confidentAnswers(group, kind string) map[string]map[string]any {
	return map[string]map[string]any{
		"relevant":    noul(0.12, 0.12),
		"group":       choice(group, 0.9),
		"kind":        choice(kind, 0.9),
		"materiality": score(3, 0.8),
		"tone":        score(0.9, 0.8),
		"sector":      choice("technology", 0.8),
		"topic":       choice("capital", 0.85),
	}
}

func newSidecar(t *testing.T, answers map[string]map[string]any) *sidecar {
	t.Helper()
	s := &sidecar{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		fmt.Fprint(w, `{"status":"ok","loaded":["english"]}`)
	})
	mux.HandleFunc("/v1/systemone/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			States    []string                   `json:"states"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"detail":"bad"}`, http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.batches = append(s.batches, len(req.States))
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		results := make([]map[string]any, len(req.States))
		for i := range results {
			got := map[string]any{}
			for q := range req.Questions {
				if a, ok := answers[q]; ok {
					got[q] = a
				}
			}
			results[i] = map[string]any{"model": "laya-rl-agent", "answers": got, "usage": map[string]any{"truncated": false}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *sidecar) requests() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.batches...)
}

// deadURL is the address of a sidecar that is not running any more.
func deadURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}

// writeLayaConfig switches laya on in the test environment, pointing at
// layaURL, with one topic and two sectors, and applies kind, topics and tone.
func writeLayaConfig(t *testing.T, e *testEnv, layaURL string) {
	t.Helper()
	t.Setenv("STOXICITY_LAYA_API_KEY", "")
	body := fmt.Sprintf(`since: 2026-11-01
data_dir: ./data
watchlist: [ACME]
laya:
  enabled: true
  url: %s
  min_confidence: 0.7
  timeout: 5s
  apply: [kind, topics, tone]
  batch_size: 16
topics:
  - name: capital
    words: [zero-matching-word]
sectors:
  - name: technology
    words: [software]
  - name: energy
    words: [crude oil]
`, layaURL)
	if err := os.WriteFile(filepath.Join(e.dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyWithoutLaya(t *testing.T) {
	e := newTestEnv(t)
	r := e.run(t, "classify", "Acme", "Corp", "restates", "its", "results")
	r.wantCode(t, exitOK)
	r.wantOut(t, "text          Acme Corp restates its results", "rule lens", "kind        non_reliance",
		"materiality 4", "relevance", "reasons", `keyword "restates"`, "switched off")
	r.wantNotOut(t, "merged")
	if _, err := os.Stat(filepath.Join(e.dir, "data")); err == nil {
		t.Error("classify created the data folder: it must store nothing")
	}
}

func TestClassifyShowsLayaAndTheMergedScore(t *testing.T) {
	e := newTestEnv(t)
	sc := newSidecar(t, confidentAnswers("integrity", "non_reliance"))
	writeLayaConfig(t, e, sc.URL)

	// The rules find no kind; laya's confident kind, tone and topic are applied
	// and its sector, materiality and relevance are only recorded.
	r := e.run(t, "classify", "Acme Corp updates shareholders on the year")
	r.wantCode(t, exitOK)
	r.wantOut(t, "laya          laya-auto-q",
		"relevant     0.12", "confidence 0.12",
		"kind         non_reliance", "confidence 0.90",
		"topic        capital",
		"merged        what ingest would store (applied: kind, topics, tone; minimum confidence 0.70)",
		"laya: kind non_reliance (0.90)",
		"laya: topic capital (0.85)",
		"laya (advisory): sector technology (0.80)",
		"laya (unsure): relevance 0.12 (0.12)", // below the minimum confidence
	)
	merged := r.out[strings.Index(r.out, "merged"):]
	if !strings.Contains(merged, "kind        non_reliance") || !strings.Contains(merged, "topics      capital") {
		t.Errorf("merged part lacks the applied kind and topic:\n%s", merged)
	}
	if strings.Contains(merged, "sector      technology") {
		t.Errorf("sector was applied although it is not in apply:\n%s", merged)
	}
	if strings.Contains(r.out, sc.URL) || strings.Contains(r.errOut, sc.URL) {
		t.Error("the address of the sidecar was printed")
	}
	// One request asks the group question, a second the kinds of that group.
	if got := sc.requests(); len(got) != 2 || got[0] != 1 || got[1] != 1 {
		t.Errorf("batch requests = %v, want two rounds of one article", got)
	}
}

func TestClassifyDisagreementKeepsTheRuleKind(t *testing.T) {
	e := newTestEnv(t)
	sc := newSidecar(t, confidentAnswers("capital", "buyback"))
	writeLayaConfig(t, e, sc.URL)
	r := e.run(t, "classify", "Acme Corp restates its results")
	r.wantCode(t, exitOK)
	r.wantOut(t, "laya (disagrees): kind buyback (0.90) against non_reliance")
	merged := r.out[strings.Index(r.out, "merged"):]
	if !strings.Contains(merged, "kind        non_reliance") {
		t.Errorf("the lens kind was replaced:\n%s", merged)
	}
}

func TestClassifyNoLayaDoesNotAsk(t *testing.T) {
	e := newTestEnv(t)
	sc := newSidecar(t, confidentAnswers("integrity", "non_reliance"))
	writeLayaConfig(t, e, sc.URL)
	r := e.run(t, "classify", "-no-laya", "Acme Corp restates its results")
	r.wantCode(t, exitOK)
	r.wantOut(t, "skipped (-no-laya)", "classifier")
	r.wantNotOut(t, "merged")
	if got := sc.requests(); len(got) != 0 {
		t.Errorf("sidecar was asked: %v", got)
	}
}

func TestClassifyWithLayaUnreachable(t *testing.T) {
	e := newTestEnv(t)
	dead := deadURL(t)
	writeLayaConfig(t, e, dead)
	r := e.run(t, "classify", "Acme Corp restates its results")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rule lens", "non_reliance", "no answer", "LAYA_HOST=127.0.0.1 laya-serve")
	r.wantNotOut(t, "merged")
	if strings.Contains(r.out, dead) || strings.Contains(r.errOut, dead) {
		t.Error("the address of the sidecar was printed")
	}
}

func TestClassifyUnusableLayaSettingsWarnOnce(t *testing.T) {
	e := newTestEnv(t)
	writeLayaConfig(t, e, "http://someone:hunter2@127.0.0.1:9") // user info is refused by the client
	r := e.run(t, "classify", "Acme Corp restates its results")
	r.wantCode(t, exitOK)
	r.wantOut(t, "rule lens", "not available")
	if n := strings.Count(r.errOut, "laya is switched on but unusable"); n != 1 {
		t.Errorf("stderr has %d warnings, want 1:\n%s", n, r.errOut)
	}
	for _, secret := range []string{"hunter2", "someone", "127.0.0.1"} {
		if strings.Contains(r.out+r.errOut, secret) {
			t.Errorf("output leaks %q:\n%s%s", secret, r.out, r.errOut)
		}
	}
}

func TestClassifyReadsStandardInput(t *testing.T) {
	e := newTestEnv(t)
	opts := runOptions{cwd: e.dir, client: e.client, now: func() time.Time { return testNow },
		stdin: strings.NewReader("Acme Corp restates its results\n\nSecond line of detail.\n")}
	r := runWith(t, opts, "-config-dir", e.dir, "classify", "-")
	r.wantCode(t, exitOK)
	r.wantOut(t, "text          Acme Corp restates its results", "summary       Second line of detail.", "non_reliance")
}

func TestClassifyUsageErrors(t *testing.T) {
	e := newTestEnv(t)
	for name, args := range map[string][]string{
		"no text":      {"classify"},
		"only a flag":  {"classify", "-no-laya"},
		"unknown flag": {"classify", "-nope", "x"},
		"blank text":   {"classify", "  \x1b[0m "},
	} {
		t.Run(name, func(t *testing.T) {
			e.run(t, args...).wantCode(t, exitUsage)
		})
	}
}

func TestDoctorAsksTheSidecar(t *testing.T) {
	const key = "sk-doctor-0123"
	e := newTestEnv(t)
	sc := newSidecar(t, nil)
	writeLayaConfig(t, e, sc.URL)
	t.Setenv("STOXICITY_LAYA_API_KEY", key)

	r := e.run(t, "doctor")
	r.wantCode(t, exitOK)
	if l := r.line("laya"); !strings.HasPrefix(l, "OK") || !strings.Contains(l, "answers") {
		t.Errorf("laya line is %q, want OK and answers", l)
	}
	if strings.Contains(r.out, sc.URL) || strings.Contains(r.out, key) {
		t.Errorf("doctor printed the address or the key:\n%s", r.out)
	}
	if len(sc.auth) == 0 || sc.auth[0] != "Bearer "+key {
		t.Errorf("health was asked with %q, want the key as a Bearer token", sc.auth)
	}
}

func TestDoctorWarnsWhenTheSidecarIsDown(t *testing.T) {
	const key = "sk-doctor-4567"
	e := newTestEnv(t)
	dead := deadURL(t)
	writeLayaConfig(t, e, dead)
	t.Setenv("STOXICITY_LAYA_API_KEY", key)

	r := e.run(t, "doctor")
	r.wantCode(t, exitOK) // laya is optional
	l := r.line("laya")
	if !strings.HasPrefix(l, "WARN") {
		t.Fatalf("laya line is %q, want WARN", l)
	}
	for _, want := range []string{"LAYA_HOST=127.0.0.1 laya-serve", "LAYA_API_KEY", "STOXICITY_LAYA_API_KEY"} {
		if !strings.Contains(l, want) {
			t.Errorf("laya line %q lacks %q", l, want)
		}
	}
	if strings.Contains(r.out, dead) || strings.Contains(r.out, key) {
		t.Errorf("doctor printed the address or the key:\n%s", r.out)
	}
}

func TestDoctorWarnsAboutUnusableLayaSettings(t *testing.T) {
	e := newTestEnv(t)
	writeLayaConfig(t, e, "http://someone:hunter2@127.0.0.1:9")
	r := e.run(t, "doctor")
	r.wantCode(t, exitOK)
	if l := r.line("laya"); !strings.HasPrefix(l, "WARN") || strings.Contains(l, "hunter2") {
		t.Errorf("laya line is %q, want a WARN without the credential", l)
	}
}

func TestFetchStoresTheOpinionAndItsVersion(t *testing.T) {
	e := newTestEnv(t)
	sc := newSidecar(t, confidentAnswers("integrity", "non_reliance"))
	writeLayaConfig(t, e, sc.URL)

	r := e.run(t, "fetch")
	r.wantCode(t, exitOK)
	r.wantOut(t, "2 new")
	// Both articles travel together in each of the two rounds.
	if got := sc.requests(); len(got) != 2 || got[0] != 2 || got[1] != 2 {
		t.Errorf("batch requests = %v, want two rounds of both articles", got)
	}

	db, err := store.Open(filepath.Join(e.dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	arts, err := db.Range(context.Background(), testNow.AddDate(0, 0, -2), testNow.AddDate(0, 0, 1))
	if err != nil || len(arts) != 2 {
		t.Fatalf("stored %d articles, err %v", len(arts), err)
	}
	for _, a := range arts {
		if !strings.Contains(a.Score.LensVersion, "+laya-auto-q") {
			t.Errorf("%s: LensVersion %q lacks the laya version", a.ID, a.Score.LensVersion)
		}
		if a.Score.Tone == 0 {
			t.Errorf("%s: tone was not applied", a.ID)
		}
		found := false
		for _, topic := range a.Topics {
			found = found || topic == "capital"
		}
		if !found {
			t.Errorf("%s: topics %v lack laya's topic", a.ID, a.Topics)
		}
		if a.Score.Sector != "" {
			t.Errorf("%s: sector %q although sector is not in apply", a.ID, a.Score.Sector)
		}
		advisory := false
		for _, reason := range a.Score.Reasons {
			advisory = advisory || strings.HasPrefix(reason, "laya (advisory): sector technology")
		}
		if !advisory {
			t.Errorf("%s: no advisory reason for the sector:\n%q", a.ID, a.Score.Reasons)
		}
	}

	// The lens version in force names the laya version too.
	r = e.run(t, "rescore", "-days", "3")
	r.wantCode(t, exitOK)
	r.wantOut(t, "+laya-auto-q")
}
