package laya

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
)

// The server below speaks the wire format of laya-serve as probed on the
// development machine (POST /v1/systemone/batch, GET /health); the answer
// values are made up.

const (
	fakeKey    = "sk-SECRET-KEY-0123"
	fakePath   = "/tok-SECRET-PATH"
	batchRoute = "/v1/systemone/batch"
)

func noulAnswer(p, conf float64) map[string]any {
	return map[string]any{"type": "noul", "noul": p, "answer_confidence": conf}
}

func choiceAnswer(key string, conf float64) map[string]any {
	return map[string]any{
		"type": "choice", "choice": key, "answer_confidence": conf,
		"probabilities": map[string]float64{key: conf},
	}
}

func scoreAnswer(v, conf float64) map[string]any {
	return map[string]any{
		"type": "score", "score": v, "answer_confidence": conf,
		"probabilities": map[string]float64{"0": 0.1, "1": 0.2, "2": 0.3, "3": 0.2, "4": 0.2},
	}
}

// truth maps the start of a state to the answers the fake server gives for it.
type truth map[string]map[string]any

// reply is what the fake server sends for one request.
type reply struct {
	status int
	header map[string]string
	body   []byte // sent as is when set
}

// fakeServer records requests and answers them from a truth table, unless its
// handler returns a reply of its own.
type fakeServer struct {
	*httptest.Server
	mu      sync.Mutex
	reqs    []batchRequest
	raw     [][]byte
	headers []http.Header
	truth   truth
	// handler, when set, is asked first with the 1-based request number; a
	// nil reply means answer from the truth table.
	handler func(n int, req batchRequest) *reply
}

func newFake(t *testing.T, tr truth) *fakeServer {
	t.Helper()
	f := &fakeServer{truth: tr}
	mux := http.NewServeMux()
	mux.HandleFunc(fakePath+batchRoute, f.serveBatch)
	mux.HandleFunc(batchRoute, f.serveBatch)
	health := func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"status":"ok","loaded":["english"]}`)
	}
	mux.HandleFunc("/health", health)
	mux.HandleFunc(fakePath+"/health", health)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) serveBatch(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req batchRequest
	if err := json.Unmarshal(raw, &req); err != nil || r.Method != http.MethodPost {
		http.Error(w, `{"detail":"bad"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.raw = append(f.raw, raw)
	f.headers = append(f.headers, r.Header.Clone())
	n := len(f.reqs)
	f.mu.Unlock()

	if f.handler != nil {
		if rp := f.handler(n, req); rp != nil {
			for k, v := range rp.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(rp.status)
			_, _ = w.Write(rp.body)
			return
		}
	}
	results := make([]map[string]any, len(req.States))
	for i, st := range req.States {
		answers := map[string]any{}
		for prefix, ans := range f.truth {
			if strings.HasPrefix(st, prefix) {
				for qid, a := range ans {
					if _, asked := req.Questions[qid]; asked {
						answers[qid] = a
					}
				}
			}
		}
		results[i] = map[string]any{
			"model":   "english",
			"answers": answers,
			"usage":   map[string]any{"truncated": false, "state_tokens": 20},
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "total_usage": map[string]any{}})
}

func (f *fakeServer) requests() []batchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]batchRequest(nil), f.reqs...)
}

// sleeps collects the waits a Scorer asked for instead of sleeping.
type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return ctx.Err()
}

func newTestScorer(t *testing.T, base string, mut func(*Config)) (*Scorer, *sleeps) {
	t.Helper()
	cfg := Config{URL: base}
	if mut != nil {
		mut(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sl := &sleeps{}
	s.sleep = sl.sleep
	return s, sl
}

func article(title string) model.Article {
	return model.Article{ID: title, Title: title, Summary: "Details of " + title, URL: "https://example.com/secret-url/" + title}
}

func criteriaKeys(t *testing.T, q question) []string {
	t.Helper()
	m, ok := q.Criteria.(map[string]any)
	if !ok {
		t.Fatalf("criteria is %T, want an object", q.Criteria)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func kindNames(g model.EventGroup) []string {
	var out []string
	for _, k := range model.KindsIn(g) {
		out = append(out, string(k))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOpinionsConversion(t *testing.T) {
	f := newFake(t, truth{"Acme restates": {
		qRelevant:    noulAnswer(0.91, 0.91),
		qGroup:       choiceAnswer("integrity", 0.8),
		qKind:        choiceAnswer("non_reliance", 0.6),
		qMateriality: scoreAnswer(3.5, 0.45),
		qTone:        scoreAnswer(0.6, 0.55),
		qSector:      choiceAnswer("Technology", 0.7),
		qTopic:       choiceAnswer("ai", 0.5),
	}})
	s, _ := newTestScorer(t, f.URL, func(c *Config) {
		c.Sectors = []string{"Technology", "Energy"}
		c.Topics = []string{"ai", "rates"}
	})
	ops, err := s.Opinions(context.Background(), []model.Article{article("Acme restates 2024 results")})
	if err != nil {
		t.Fatal(err)
	}
	want := lens.Opinion{
		Source: "laya", Version: s.Version(),
		Relevance: 0.91, RelevanceConfidence: 0.91,
		Group: model.GroupIntegrity, GroupConfidence: 0.8,
		Kind: model.EventNonReliance, KindConfidence: 0.6,
		Materiality: 4.5, MaterialityConfidence: 0.45,
		Tone: -0.7, ToneConfidence: 0.55,
		Sector: "Technology", SectorConfidence: 0.7,
		Topic: "ai", TopicConfidence: 0.5,
	}
	if len(ops) != 1 || !reflect.DeepEqual(ops[0], want) {
		t.Fatalf("got %+v\nwant %+v", ops, want)
	}
}

func TestKindConfidenceIsTheLowerOfTheTwo(t *testing.T) {
	f := newFake(t, truth{
		"low group": {qGroup: choiceAnswer("capital", 0.3), qKind: choiceAnswer("buyback", 0.9)},
		"low kind":  {qGroup: choiceAnswer("capital", 0.9), qKind: choiceAnswer("buyback", 0.4)},
		"wrong":     {qGroup: choiceAnswer("capital", 0.9), qKind: choiceAnswer("cyber", 0.9)},
		"unknown":   {qGroup: choiceAnswer("capital", 0.9), qKind: choiceAnswer("not_a_kind", 0.9)},
	})
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{
		article("low group"), article("low kind"), article("wrong"), article("unknown"),
	})
	if err != nil {
		t.Fatal(err)
	}
	type kc struct {
		kind model.EventKind
		conf float64
	}
	want := []kc{{model.EventBuyback, 0.3}, {model.EventBuyback, 0.4}, {"", 0}, {"", 0}}
	for i, w := range want {
		if ops[i].Kind != w.kind || ops[i].KindConfidence != w.conf {
			t.Errorf("article %d: kind %q conf %v, want %q %v", i, ops[i].Kind, ops[i].KindConfidence, w.kind, w.conf)
		}
	}
}

func TestScoreMapping(t *testing.T) {
	tests := []struct {
		score    float64
		materia  float64
		tone     float64
		scenario string
	}{
		{0, 1, -1, "lowest level"},
		{1, 2, -0.5, "level one"},
		{2, 3, 0, "middle"},
		{2.5, 3.5, 0.25, "expected value between levels"},
		{4, 5, 1, "highest level"},
		{9, 5, 1, "out of range is clamped"},
		{-3, 1, -1, "negative is clamped"},
	}
	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			f := newFake(t, truth{"x": {qMateriality: scoreAnswer(tc.score, 0.5), qTone: scoreAnswer(tc.score, 0.5)}})
			s, _ := newTestScorer(t, f.URL, nil)
			ops, err := s.Opinions(context.Background(), []model.Article{article("x")})
			if err != nil {
				t.Fatal(err)
			}
			if ops[0].Materiality != tc.materia || ops[0].Tone != tc.tone {
				t.Errorf("materiality %v tone %v, want %v %v", ops[0].Materiality, ops[0].Tone, tc.materia, tc.tone)
			}
		})
	}
}

func TestSectorAndTopicQuestionsAreOptional(t *testing.T) {
	f := newFake(t, nil)
	s, _ := newTestScorer(t, f.URL, nil)
	if _, err := s.Opinions(context.Background(), []model.Article{article("x")}); err != nil {
		t.Fatal(err)
	}
	got := sortedKeys(f.requests()[0].Questions)
	if want := []string{"group", "materiality", "relevant", "tone"}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions without lists: %v, want %v", got, want)
	}

	f2 := newFake(t, nil)
	s2, _ := newTestScorer(t, f2.URL, func(c *Config) {
		c.Sectors = []string{"Technology", "Energy", "macro", "none", " ", "Energy"}
		c.Topics = []string{"ai"}
	})
	if _, err := s2.Opinions(context.Background(), []model.Article{article("x")}); err != nil {
		t.Fatal(err)
	}
	q := f2.requests()[0].Questions
	if got := sortedKeys(q); !reflect.DeepEqual(got, []string{"group", "materiality", "relevant", "sector", "tone", "topic"}) {
		t.Errorf("questions with lists: %v", got)
	}
	if got := criteriaKeys(t, q["sector"]); !reflect.DeepEqual(got, []string{"Energy", "Technology", "macro", "none"}) {
		t.Errorf("sector options: %v", got)
	}
	if got := criteriaKeys(t, q["topic"]); !reflect.DeepEqual(got, []string{"ai", "none"}) {
		t.Errorf("topic options: %v", got)
	}
}

func TestGroupQuestionOffersEveryGroup(t *testing.T) {
	f := newFake(t, nil)
	s, _ := newTestScorer(t, f.URL, nil)
	if _, err := s.Opinions(context.Background(), []model.Article{article("x")}); err != nil {
		t.Fatal(err)
	}
	got := criteriaKeys(t, f.requests()[0].Questions["group"])
	var want []string
	for _, g := range groupOrder {
		want = append(want, string(g))
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) || len(got) != 10 {
		t.Errorf("group options: %v", got)
	}
	mat, ok := f.requests()[0].Questions["materiality"].Criteria.([]any)
	if !ok || len(mat) != 5 {
		t.Errorf("materiality criteria: %v", f.requests()[0].Questions["materiality"].Criteria)
	}
}

func TestSectorAndTopicAnswersThatGiveNothing(t *testing.T) {
	tests := []struct {
		name         string
		sector, topc string
	}{
		{"none", "none", "none"},
		{"macro", "macro", "none"},
		{"unknown", "Biotech", "crypto"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t, truth{"x": {qSector: choiceAnswer(tc.sector, 0.9), qTopic: choiceAnswer(tc.topc, 0.9)}})
			s, _ := newTestScorer(t, f.URL, func(c *Config) {
				c.Sectors = []string{"Technology"}
				c.Topics = []string{"ai"}
			})
			ops, err := s.Opinions(context.Background(), []model.Article{article("x")})
			if err != nil {
				t.Fatal(err)
			}
			o := ops[0]
			if o.Sector != "" || o.SectorConfidence != 0 || o.Topic != "" || o.TopicConfidence != 0 {
				t.Errorf("got sector %q/%v topic %q/%v, want none", o.Sector, o.SectorConfidence, o.Topic, o.TopicConfidence)
			}
		})
	}
}

func TestTwoRoundsWithDifferentGroupsInOneBatch(t *testing.T) {
	f := newFake(t, truth{
		"buyback":  {qGroup: choiceAnswer("capital", 0.9), qKind: choiceAnswer("buyback", 0.8)},
		"dividend": {qGroup: choiceAnswer("capital", 0.7), qKind: choiceAnswer("dividend", 0.7)},
		"cyber":    {qGroup: choiceAnswer("integrity", 0.6), qKind: choiceAnswer("cyber", 0.9)},
		"target":   {qGroup: choiceAnswer("noise", 0.5), qKind: choiceAnswer("analyst_target", 0.5)},
		"nonsense": {qGroup: choiceAnswer("weather", 0.9), qKind: choiceAnswer("buyback", 0.9)},
	})
	s, _ := newTestScorer(t, f.URL, nil)
	arts := []model.Article{article("target raised"), article("buyback big"), article("nonsense"), article("cyber hit"), article("dividend cut")}
	ops, err := s.Opinions(context.Background(), arts)
	if err != nil {
		t.Fatal(err)
	}

	reqs := f.requests()
	if len(reqs) != 4 {
		t.Fatalf("%d requests, want 1 for round one and 3 for round two", len(reqs))
	}
	if len(reqs[0].States) != 5 || reqs[0].Questions["kind"].Type != "" {
		t.Fatalf("round one: %d states, kind question %+v", len(reqs[0].States), reqs[0].Questions["kind"])
	}
	// Round two: one call per predicted group, in the order of the taxonomy.
	round2 := []struct {
		group  model.EventGroup
		states int
	}{{model.GroupCapital, 2}, {model.GroupIntegrity, 1}, {model.GroupNoise, 1}}
	for i, w := range round2 {
		r := reqs[i+1]
		if got := sortedKeys(r.Questions); !reflect.DeepEqual(got, []string{"kind"}) {
			t.Errorf("round two %v asks %v", w.group, got)
		}
		if got, want := criteriaKeys(t, r.Questions["kind"]), kindNames(w.group); !reflect.DeepEqual(got, want) {
			t.Errorf("round two %v kinds %v, want %v", w.group, got, want)
		}
		if len(r.States) != w.states {
			t.Errorf("round two %v has %d states, want %d", w.group, len(r.States), w.states)
		}
	}
	wantKinds := []model.EventKind{model.EventAnalystTgt, model.EventBuyback, "", model.EventCyber, model.EventDividend}
	for i, k := range wantKinds {
		if ops[i].Kind != k {
			t.Errorf("article %d kind %q, want %q", i, ops[i].Kind, k)
		}
	}
	if ops[2].Group != "" || ops[2].GroupConfidence != 0 {
		t.Errorf("an unknown group should give no group, got %q/%v", ops[2].Group, ops[2].GroupConfidence)
	}
}

func TestChunkingKeepsOrder(t *testing.T) {
	tr := truth{}
	var arts []model.Article
	for i := 0; i < 7; i++ {
		title := fmt.Sprintf("art%d", i)
		arts = append(arts, article(title))
		tr[title] = map[string]any{
			qRelevant: noulAnswer(float64(i+1)/10, 0.5),
			qGroup:    choiceAnswer("noise", 0.5),
			qKind:     choiceAnswer("other", float64(i+1)/10),
		}
	}
	f := newFake(t, tr)
	s, _ := newTestScorer(t, f.URL, func(c *Config) { c.BatchSize = 3 })
	ops, err := s.Opinions(context.Background(), arts)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for _, r := range f.requests() {
		sizes = append(sizes, len(r.States))
	}
	if want := []int{3, 3, 1, 3, 3, 1}; !reflect.DeepEqual(sizes, want) {
		t.Errorf("request sizes %v, want %v", sizes, want)
	}
	if len(ops) != 7 {
		t.Fatalf("%d opinions", len(ops))
	}
	for i, o := range ops {
		want := float64(i+1) / 10
		if o.Relevance != want || o.Kind != model.EventOther || o.KindConfidence != min(want, 0.5) {
			t.Errorf("article %d: %+v", i, o)
		}
	}
}

func TestEmptyStateGetsZeroOpinionWithoutACall(t *testing.T) {
	f := newFake(t, truth{"real": {qRelevant: noulAnswer(0.5, 0.5)}})
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{{ID: "a", URL: "https://example.com/a"}, article("real"), {Title: "  ", Summary: "\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ops[0], lens.Opinion{}) || !reflect.DeepEqual(ops[2], lens.Opinion{}) || ops[1].Relevance != 0.5 {
		t.Errorf("got %+v", ops)
	}
	if reqs := f.requests(); len(reqs) != 1 || len(reqs[0].States) != 1 {
		t.Errorf("requests: %+v", reqs)
	}

	f2 := newFake(t, nil)
	s2, _ := newTestScorer(t, f2.URL, nil)
	if ops, err := s2.Opinions(context.Background(), []model.Article{{}}); err != nil || len(ops) != 1 || len(f2.requests()) != 0 {
		t.Errorf("only empty articles: ops %v err %v requests %d", ops, err, len(f2.requests()))
	}
	if ops, err := s2.Opinions(context.Background(), nil); err != nil || len(ops) != 0 {
		t.Errorf("no articles: %v %v", ops, err)
	}
}

func TestTruncatedFlag(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(n int, req batchRequest) *reply {
		results := make([]map[string]any, len(req.States))
		for i := range results {
			results[i] = map[string]any{
				"answers": map[string]any{"group": choiceAnswer("macro", 0.5)},
				"usage":   map[string]any{"truncated": i == 1, "state_tokens": 1024},
			}
		}
		body, _ := json.Marshal(map[string]any{"results": results})
		return &reply{status: 200, body: body}
	}
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a"), article("b")})
	if err != nil {
		t.Fatal(err)
	}
	if ops[0].Truncated || !ops[1].Truncated {
		t.Errorf("truncated flags: %v %v", ops[0].Truncated, ops[1].Truncated)
	}
}

func TestUnknownAnswerShapesAreIgnored(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(n int, req batchRequest) *reply {
		body := `{"results":[{"answers":{
			"relevant":{"type":"choice","choice":"x","answer_confidence":0.9},
			"group":42,
			"materiality":{"type":"score","score":"high","answer_confidence":0.9},
			"tone":{"type":"score","answer_confidence":0.9},
			"sector":null,
			"topic":["a"]
		}}]}`
		return &reply{status: 200, body: []byte(body)}
	}
	s, _ := newTestScorer(t, f.URL, func(c *Config) { c.Sectors = []string{"A"}; c.Topics = []string{"a"} })
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if err != nil {
		t.Fatal(err)
	}
	want := lens.Opinion{Source: "laya", Version: s.Version()}
	if !reflect.DeepEqual(ops[0], want) {
		t.Errorf("got %+v, want only source and version", ops[0])
	}
}

func TestResultCountMismatchIsAnError(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply { return &reply{status: 200, body: []byte(`{"results":[]}`)} }
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if err == nil || ops != nil {
		t.Errorf("got %v, %v", ops, err)
	}
}

func TestBusyServerIsRetriedWithRetryAfter(t *testing.T) {
	f := newFake(t, truth{"a": {qRelevant: noulAnswer(0.5, 0.5)}})
	f.handler = func(n int, _ batchRequest) *reply {
		if n == 1 {
			return &reply{status: 503, header: map[string]string{"Retry-After": "2"}, body: []byte(`{"detail":"busy"}`)}
		}
		return nil
	}
	s, sl := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if err != nil || ops[0].Relevance != 0.5 {
		t.Fatalf("ops %v err %v", ops, err)
	}
	if !reflect.DeepEqual(sl.d, []time.Duration{2 * time.Second}) {
		t.Errorf("waits %v", sl.d)
	}
}

func TestBusyServerGivesUpAfterThreeAttemptsWithinTheCap(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply {
		return &reply{status: 503, header: map[string]string{"Retry-After": "30"}}
	}
	s, sl := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if ops != nil || err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("ops %v err %v", ops, err)
	}
	if n := len(f.requests()); n != 3 {
		t.Errorf("%d attempts, want 3", n)
	}
	var total time.Duration
	for _, d := range sl.d {
		total += d
	}
	if total > 10*time.Second {
		t.Errorf("waited %v in total: %v", total, sl.d)
	}
}

func TestBusyServerWithoutRetryAfterWaitsAMoment(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(n int, _ batchRequest) *reply {
		if n == 1 {
			return &reply{status: 503, header: map[string]string{"Retry-After": "soon"}}
		}
		return nil
	}
	s, sl := newTestScorer(t, f.URL, nil)
	if _, err := s.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sl.d, []time.Duration{time.Second}) {
		t.Errorf("waits %v", sl.d)
	}
}

func TestOtherServerErrorsAreRetriedOnce(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(n int, _ batchRequest) *reply {
		if n == 1 {
			return &reply{status: 500, body: []byte(`{"detail":"inference failed"}`)}
		}
		return nil
	}
	s, sl := newTestScorer(t, f.URL, nil)
	if _, err := s.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	if len(f.requests()) != 2 || len(sl.d) != 1 {
		t.Errorf("requests %d, waits %v", len(f.requests()), sl.d)
	}

	f2 := newFake(t, nil)
	f2.handler = func(int, batchRequest) *reply { return &reply{status: 500} }
	s2, _ := newTestScorer(t, f2.URL, nil)
	ops, err := s2.Opinions(context.Background(), []model.Article{article("a")})
	if ops != nil || err == nil || !strings.Contains(err.Error(), "500") || len(f2.requests()) != 2 {
		t.Errorf("ops %v err %v requests %d", ops, err, len(f2.requests()))
	}
}

func TestRefusedBatchIsRetriedStateByState(t *testing.T) {
	for _, code := range []int{http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := newFake(t, truth{
				"good": {qRelevant: noulAnswer(0.7, 0.7)},
				"fine": {qRelevant: noulAnswer(0.2, 0.8)},
			})
			f.handler = func(_ int, req batchRequest) *reply {
				bad := len(req.States) > 1
				for _, st := range req.States {
					bad = bad || strings.HasPrefix(st, "BAD")
				}
				if bad {
					return &reply{status: code, body: []byte(`{"detail":"state too long"}`)}
				}
				return nil
			}
			s, _ := newTestScorer(t, f.URL, nil)
			ops, err := s.Opinions(context.Background(), []model.Article{article("good one"), article("BAD one"), article("fine one")})
			if err != nil {
				t.Fatal(err)
			}
			if ops[0].Relevance != 0.7 || ops[2].Relevance != 0.2 || !reflect.DeepEqual(ops[1], lens.Opinion{}) {
				t.Errorf("got %+v", ops)
			}
			if n := len(f.requests()); n != 4 {
				t.Errorf("%d requests, want the batch and three singles", n)
			}
		})
	}
}

func TestRefusedSingleArticleGetsZeroOpinion(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply { return &reply{status: 422} }
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if err != nil || len(ops) != 1 || !reflect.DeepEqual(ops[0], lens.Opinion{}) || len(f.requests()) != 1 {
		t.Errorf("ops %v err %v requests %d", ops, err, len(f.requests()))
	}
}

func TestOtherClientErrorsFailTheBatch(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		f := newFake(t, nil)
		f.handler = func(int, batchRequest) *reply { return &reply{status: code} }
		s, _ := newTestScorer(t, f.URL, nil)
		ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
		if ops != nil || err == nil || !strings.Contains(err.Error(), strconv.Itoa(code)) || len(f.requests()) != 1 {
			t.Errorf("%d: ops %v err %v requests %d", code, ops, err, len(f.requests()))
		}
	}
}

func TestLaterBatchFailureReturnsNoPartialResults(t *testing.T) {
	f := newFake(t, truth{"": {qRelevant: noulAnswer(0.5, 0.5)}})
	f.handler = func(n int, _ batchRequest) *reply {
		if n == 2 {
			return &reply{status: 401}
		}
		return nil
	}
	s, _ := newTestScorer(t, f.URL, func(c *Config) { c.BatchSize = 1 })
	ops, err := s.Opinions(context.Background(), []model.Article{article("a"), article("b")})
	if ops != nil || err == nil {
		t.Errorf("ops %v err %v", ops, err)
	}
}

func TestSecretsNeverAppearInErrors(t *testing.T) {
	statuses := []int{400, 401, 403, 422, 500, 503, 307}
	for _, code := range statuses {
		f := newFake(t, nil)
		f.handler = func(int, batchRequest) *reply {
			return &reply{status: code, header: map[string]string{"Location": f.URL + "/elsewhere", "Retry-After": "0"}, body: []byte(fakeKey)}
		}
		s, _ := newTestScorer(t, f.URL+fakePath, func(c *Config) { c.APIKey = fakeKey })
		_, err := s.Opinions(context.Background(), []model.Article{article("a")})
		if code == 422 {
			if err != nil {
				t.Errorf("422 should give a zero opinion, got %v", err)
			}
			continue
		}
		assertClean(t, fmt.Sprintf("status %d", code), err, f.URL)
		if err2 := s.Ping(context.Background()); err2 != nil {
			assertClean(t, "ping", err2, f.URL)
		}
	}

	// A closed port, a body that is not JSON, and a reply that is too big.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	s, _ := newTestScorer(t, deadURL+fakePath, func(c *Config) { c.APIKey = fakeKey })
	_, err := s.Opinions(context.Background(), []model.Article{article("a")})
	assertClean(t, "connection refused", err, deadURL)
	assertClean(t, "ping refused", s.Ping(context.Background()), deadURL)

	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply { return &reply{status: 200, body: []byte("<html>" + fakeKey)} }
	s, _ = newTestScorer(t, f.URL+fakePath, func(c *Config) { c.APIKey = fakeKey })
	_, err = s.Opinions(context.Background(), []model.Article{article("a")})
	assertClean(t, "bad json", err, f.URL)
}

func assertClean(t *testing.T, what string, err error, host string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: want an error", what)
		return
	}
	msg := err.Error()
	for _, bad := range []string{fakeKey, "SECRET", host, strings.TrimPrefix(host, "http://"), "127.0.0.1"} {
		if strings.Contains(msg, bad) {
			t.Errorf("%s: error %q contains %q", what, msg, bad)
		}
	}
	if !strings.HasPrefix(msg, "laya: ") {
		t.Errorf("%s: error %q lacks the package prefix", what, msg)
	}
}

func TestNewErrorsNeverContainTheURLOrKey(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"userinfo", Config{URL: "http://user:hunter2-SECRET@host:8000"}},
		{"scheme", Config{URL: "ftp://secret-host/"}},
		{"no host", Config{URL: "http:///path-SECRET"}},
		{"query", Config{URL: "http://host/?token=hunter2-SECRET"}},
		{"fragment", Config{URL: "http://host/#hunter2-SECRET"}},
		{"unparsable", Config{URL: "http://[::1/hunter2-SECRET"}},
		{"empty", Config{}},
		{"key with newline", Config{URL: "http://host", APIKey: "hunter2-SECRET\nX: y"}},
		{"batch too big", Config{URL: "http://host", BatchSize: 65}},
		{"batch negative", Config{URL: "http://host", BatchSize: -1}},
		{"negative timeout", Config{URL: "http://host", Timeout: -time.Second}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.cfg)
			if err == nil || s != nil {
				t.Fatalf("got %v, %v", s, err)
			}
			if msg := err.Error(); strings.Contains(msg, "hunter2") || strings.Contains(msg, "SECRET") || strings.Contains(msg, "secret-host") || !strings.HasPrefix(msg, "laya: ") {
				t.Errorf("error %q", msg)
			}
		})
	}
	if _, err := New(Config{URL: "https://laya.example.com:8443/prefix/", BatchSize: 64}); err != nil {
		t.Errorf("a valid url: %v", err)
	}
}

func TestAPIKeyIsSentAsBearerHeaderOnly(t *testing.T) {
	f := newFake(t, nil)
	s, _ := newTestScorer(t, f.URL+fakePath, func(c *Config) { c.APIKey = fakeKey })
	if _, err := s.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, h := range f.headers {
		if got := h.Get("Authorization"); got != "Bearer "+fakeKey {
			t.Errorf("request %d Authorization %q", i, got)
		}
	}
	for _, raw := range f.raw {
		if strings.Contains(string(raw), fakeKey) {
			t.Error("the key is in a request body")
		}
	}

	f2 := newFake(t, nil)
	s2, _ := newTestScorer(t, f2.URL, nil)
	if _, err := s2.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	if got := f2.headers[0].Get("Authorization"); got != "" {
		t.Errorf("Authorization %q without a key", got)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hits int
	var mu sync.Mutex
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer other.Close()
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply {
		return &reply{status: http.StatusTemporaryRedirect, header: map[string]string{"Location": other.URL + batchRoute}}
	}

	clients := map[string]*http.Client{
		"default":           nil,
		"following client":  {},
		"client with proxy": {Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	}
	for name, c := range clients {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestScorer(t, f.URL, func(cfg *Config) { cfg.APIKey = fakeKey; cfg.HTTPClient = c })
			_, err := s.Opinions(context.Background(), []model.Article{article("a")})
			if err == nil || !strings.Contains(err.Error(), "307") {
				t.Errorf("err %v", err)
			}
			if c != nil && c.CheckRedirect != nil {
				t.Error("the caller's client was modified")
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("the redirect target was contacted %d times", hits)
	}
}

func TestDefaultClientIgnoresProxyEnvironment(t *testing.T) {
	s, err := New(Config{URL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := s.client.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Errorf("transport %T has a proxy function", s.client.Transport)
	}
}

func TestResponseBodyIsLimited(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply {
		return &reply{status: 200, body: []byte(`{"results":[` + strings.Repeat(" ", 9<<20) + `]}`)}
	}
	s, _ := newTestScorer(t, f.URL, nil)
	ops, err := s.Opinions(context.Background(), []model.Article{article("a")})
	if ops != nil || err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Errorf("ops %v err %v", ops, err)
	}
}

func TestRequestShape(t *testing.T) {
	f := newFake(t, nil)
	s, _ := newTestScorer(t, f.URL, func(c *Config) { c.Model = "english" })
	if _, err := s.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(f.raw[0], &m); err != nil {
		t.Fatal(err)
	}
	if string(m["model"]) != `"english"` || m["states"] == nil || m["questions"] == nil {
		t.Errorf("body %s", f.raw[0])
	}
	if strings.Contains(string(f.raw[0]), "secret-url") || strings.Contains(string(f.raw[0]), "example.com") {
		t.Errorf("the article URL was sent: %s", f.raw[0])
	}

	f2 := newFake(t, nil)
	s2, _ := newTestScorer(t, f2.URL, nil)
	if _, err := s2.Opinions(context.Background(), []model.Article{article("a")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(f2.raw[0]), `"model"`) {
		t.Errorf("model sent although none is configured: %s", f2.raw[0])
	}
}

func TestVersion(t *testing.T) {
	mk := func(mut func(*Config)) string {
		cfg := Config{URL: "http://127.0.0.1:8000"}
		if mut != nil {
			mut(&cfg)
		}
		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return s.Version()
	}
	base := mk(nil)
	if !regexp.MustCompile(`^laya-auto-q[0-9a-f]{8}$`).MatchString(base) {
		t.Errorf("version %q", base)
	}
	if got := mk(func(c *Config) { c.Model = "english" }); !regexp.MustCompile(`^laya-english-q[0-9a-f]{8}$`).MatchString(got) {
		t.Errorf("version %q", got)
	}

	a := mk(func(c *Config) { c.Topics = []string{"ai", "rates", "energy"}; c.Sectors = []string{"Tech", "Energy"} })
	b := mk(func(c *Config) {
		c.Topics = []string{"rates", "energy", "ai", "ai"}
		c.Sectors = []string{"Energy", "Tech", "none"}
	})
	if a != b {
		t.Errorf("reordered lists changed the version: %q %q", a, b)
	}
	different := map[string]string{
		"base":          base,
		"with topics":   a,
		"fewer topics":  mk(func(c *Config) { c.Topics = []string{"ai", "rates"}; c.Sectors = []string{"Tech", "Energy"} }),
		"other topic":   mk(func(c *Config) { c.Topics = []string{"ai", "rates", "metals"}; c.Sectors = []string{"Tech", "Energy"} }),
		"other sectors": mk(func(c *Config) { c.Topics = []string{"ai", "rates", "energy"}; c.Sectors = []string{"Tech"} }),
	}
	seen := map[string]string{}
	for name, v := range different {
		if prev, dup := seen[v]; dup {
			t.Errorf("%q and %q share version %q", prev, name, v)
		}
		seen[v] = name
	}
	if mk(func(c *Config) { c.BatchSize = 5; c.APIKey = "k"; c.Timeout = time.Second }) != base {
		t.Error("settings that do not change the questions changed the version")
	}
}

func TestVersionChangesWithTheQuestionText(t *testing.T) {
	a := buildQuestions(nil, nil)
	b := buildQuestions(nil, nil)
	if a.fingerprint() != b.fingerprint() {
		t.Fatal("fingerprint is not deterministic")
	}
	q := b.round1[qTone]
	q.Instructions += " "
	b.round1[qTone] = q
	if a.fingerprint() == b.fingerprint() {
		t.Error("an edit of an instruction did not change the fingerprint")
	}
	c := buildQuestions(nil, nil)
	kq := c.kinds[model.GroupCapital][qKind]
	kq.Criteria.(map[string]string)["buyback"] = "changed"
	if a.fingerprint() == c.fingerprint() {
		t.Error("an edit of a criterion did not change the fingerprint")
	}
}

func TestContextCancellation(t *testing.T) {
	t.Run("before the call", func(t *testing.T) {
		f := newFake(t, nil)
		s, _ := newTestScorer(t, f.URL+fakePath, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ops, err := s.Opinions(ctx, []model.Article{article("a")})
		if ops != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("ops %v err %v", ops, err)
		}
		if len(f.requests()) != 0 {
			t.Error("a request was sent")
		}
	})
	t.Run("during the call", func(t *testing.T) {
		started := make(chan struct{})
		f := newFake(t, nil)
		f.handler = func(int, batchRequest) *reply {
			close(started)
			time.Sleep(300 * time.Millisecond)
			return &reply{status: 200, body: []byte(`{"results":[{"answers":{}}]}`)}
		}
		s, _ := newTestScorer(t, f.URL+fakePath, nil)
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-started; cancel() }()
		ops, err := s.Opinions(ctx, []model.Article{article("a")})
		if ops != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("ops %v err %v", ops, err)
		}
	})
	t.Run("while waiting for a busy server", func(t *testing.T) {
		f := newFake(t, nil)
		f.handler = func(int, batchRequest) *reply {
			return &reply{status: 503, header: map[string]string{"Retry-After": "5"}}
		}
		s, err := New(Config{URL: f.URL})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err = s.Opinions(ctx, []model.Article{article("a")})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
			t.Errorf("err %v after %v", err, time.Since(start))
		}
	})
}

func TestRequestTimeout(t *testing.T) {
	f := newFake(t, nil)
	f.handler = func(int, batchRequest) *reply {
		time.Sleep(400 * time.Millisecond)
		return nil
	}
	s, _ := newTestScorer(t, f.URL+fakePath, func(c *Config) { c.Timeout = 50 * time.Millisecond; c.APIKey = fakeKey })
	_, err := s.Opinions(context.Background(), []model.Article{article("a")})
	assertClean(t, "timeout", err, f.URL)
	if err != nil && !strings.Contains(err.Error(), "timeout") {
		t.Errorf("err %v", err)
	}
}

func TestPing(t *testing.T) {
	f := newFake(t, nil)
	s, _ := newTestScorer(t, f.URL, nil)
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("healthy server: %v", err)
	}

	for name, body := range map[string]string{"degraded": `{"status":"loading"}`, "not json": "<html>", "empty": ""} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		s, _ := newTestScorer(t, srv.URL, nil)
		if err := s.Ping(context.Background()); err == nil {
			t.Errorf("%s: want an error", name)
		}
		srv.Close()
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer down.Close()
	s, _ = newTestScorer(t, down.URL, nil)
	if err := s.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("502: %v", err)
	}
}

func TestTaxonomyIsCoveredByTheQuestions(t *testing.T) {
	groups := map[model.EventGroup]bool{}
	for _, k := range model.EventKinds() {
		groups[k.Group()] = true
		if kindDescriptions[k] == "" {
			t.Errorf("no description for kind %q", k)
		}
	}
	if len(groups) != len(groupOrder) {
		t.Errorf("%d groups in the taxonomy, %d in groupOrder", len(groups), len(groupOrder))
	}
	qs := buildQuestions(nil, nil)
	for _, g := range groupOrder {
		if !groups[g] {
			t.Errorf("group %q is not in the taxonomy", g)
		}
		if groupDescriptions[g] == "" {
			t.Errorf("no description for group %q", g)
		}
		kq, ok := qs.kinds[g][qKind]
		if !ok {
			t.Errorf("no kind question for group %q", g)
			continue
		}
		crit := kq.Criteria.(map[string]string)
		if len(crit) != len(model.KindsIn(g)) || len(crit) > 20 {
			t.Errorf("group %q has %d kind options", g, len(crit))
		}
	}
	if len(kindDescriptions) != len(model.EventKinds()) {
		t.Errorf("%d kind descriptions for %d kinds", len(kindDescriptions), len(model.EventKinds()))
	}
}

func TestState(t *testing.T) {
	long := strings.Repeat("alpha ", 300)
	tests := []struct {
		name string
		a    model.Article
		want string
	}{
		{"title and summary", model.Article{Title: "Acme buys back shares", Summary: "A new  program\nof $5bn.", URL: "https://x.test/u"}, "Acme buys back shares — A new program of $5bn."},
		{"title only", model.Article{Title: "Fed holds rates"}, "Fed holds rates"},
		{"summary only", model.Article{Summary: "Just text"}, "Just text"},
		{"empty", model.Article{URL: "https://x.test/u", Form: "8-K"}, ""},
		{"form and items", model.Article{Title: "Acme 8-K", Summary: "Restatement.", Form: "8-K", Items: []string{"4.02", "7.01"}}, "Acme 8-K — Form 8-K (items 4.02, 7.01): Restatement."},
		{"form only", model.Article{Title: "Acme", Summary: "Annual report.", Form: "10-K"}, "Acme — Form 10-K: Annual report."},
		{"items only", model.Article{Title: "Acme", Summary: "Text.", Items: []string{"2.02"}}, "Acme — Items 2.02: Text."},
		{"form without a summary", model.Article{Title: "Acme", Form: "8-K"}, "Acme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := state(tc.a); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("long summary is cut at a word", func(t *testing.T) {
		got := state(model.Article{Title: "T", Summary: long})
		body := strings.TrimPrefix(got, "T — ")
		if n := len([]rune(body)); n > 1200 || n < 1190 {
			t.Errorf("summary has %d runes", n)
		}
		if !strings.HasSuffix(body, "alpha") || strings.Contains(body, "alph ") {
			t.Errorf("not cut at a word boundary: ...%q", body[len(body)-12:])
		}
	})
	t.Run("multi-byte runes", func(t *testing.T) {
		got := state(model.Article{Title: "T", Summary: strings.Repeat("é", 2000)})
		if body := strings.TrimPrefix(got, "T — "); len([]rune(body)) != 1200 {
			t.Errorf("%d runes", len([]rune(body)))
		}
	})
	t.Run("exactly the limit", func(t *testing.T) {
		s := strings.Repeat("a", 1200)
		if got := state(model.Article{Summary: s}); got != s {
			t.Errorf("changed a summary of exactly 1200 runes")
		}
	})
}

func healthServer(t *testing.T, reply string) *Scorer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	s, err := New(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPinTiesTheVersionToTheCheckpointRevisionsAndDevice(t *testing.T) {
	a := healthServer(t, `{"status":"ok","loaded":["english","multilingual"],"revisions":{"multilingual":"bbb222","english":"aaa111"},"device":"cpu"}`)
	b := healthServer(t, `{"status":"ok","loaded":["multilingual","english"],"revisions":{"english":"aaa111","multilingual":"bbb222"},"device":"cpu"}`)
	c := healthServer(t, `{"status":"ok","loaded":["english","multilingual"],"revisions":{"english":"aaa112","multilingual":"bbb222"},"device":"cpu"}`)
	d := healthServer(t, `{"status":"ok","loaded":["english","multilingual"],"revisions":{"english":"aaa111","multilingual":"bbb222"},"device":"mps"}`)
	base := a.Version()
	for _, s := range []*Scorer{a, b, c, d} {
		if err := s.Pin(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if a.Version() == base || !strings.HasPrefix(a.Version(), base+"-r") {
		t.Errorf("pinned version = %q, base %q", a.Version(), base)
	}
	if a.Version() != b.Version() {
		t.Errorf("the order of the reply changed the version: %q %q", a.Version(), b.Version())
	}
	if a.Version() == c.Version() {
		t.Error("another revision gave the same version")
	}
	if a.Version() == d.Version() {
		t.Error("another device gave the same version")
	}
}

func TestPinWithNothingLoadedLeavesTheVersionAlone(t *testing.T) {
	s := healthServer(t, `{"status":"ok","loaded":[],"revisions":{},"device":"cpu"}`)
	base := s.Version()
	if err := s.Pin(context.Background()); !errors.Is(err, ErrNotPinned) {
		t.Fatalf("Pin = %v, want ErrNotPinned", err)
	}
	if s.Version() != base {
		t.Errorf("version = %q", s.Version())
	}
	if err := healthServer(t, `{"status":"loading"}`).Pin(context.Background()); err == nil {
		t.Error("a server that is not ok pinned")
	}
}
