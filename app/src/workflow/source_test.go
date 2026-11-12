package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/distribution"
	"github.com/b070nd/stoxicity/app/src/instrument"
	"github.com/b070nd/stoxicity/app/src/policy"
	"github.com/b070nd/stoxicity/app/src/store"
)

// fakeActive is a policy store that holds one activation.
type fakeActive struct {
	policy.Store
	rec policy.Record
}

func (f fakeActive) AsOf(context.Context, time.Time) (policy.Record, bool, error) {
	return f.rec, true, nil
}

// fakeStore is an in-memory Store with a register and one active policy. The
// readers a test does not use return their zero value.
type fakeStore struct {
	set    instrument.Set
	policy policy.Record
	clock  func() time.Time
}

func (f *fakeStore) Portfolios(string) store.Portfolios { return store.Portfolios{} }
func (f *fakeStore) Policies(string) policy.Store       { return fakeActive{rec: f.policy} }
func (f *fakeStore) Orders(string) store.Orders         { return store.Orders{} }
func (f *fakeStore) Records(string) store.Records       { return store.Records{} }
func (f *fakeStore) DistributionObservations(context.Context, []string, time.Time) ([]distribution.Obs, error) {
	return nil, nil
}
func (f *fakeStore) InstrumentsAsOf(context.Context, time.Time) (instrument.Set, error) {
	return f.set, nil
}
func (f *fakeStore) SetClock(now func() time.Time) { f.clock = now }

func TestASourceWorksOverAnyStore(t *testing.T) {
	fake := &fakeStore{
		set:    instrument.Set{Securities: []instrument.Security{{ID: "sec-1", Name: "One"}}},
		policy: policy.Record{Policy: policy.Policy{Tier: 2}},
	}
	at := time.Date(2026, 11, 12, 23, 30, 0, 0, time.UTC)
	src := Source{DB: fake, Tenant: "default", Now: func() time.Time { return at }}
	ctx := context.Background()

	secs, err := src.Securities(ctx)
	if err != nil || len(secs) != 1 || secs["sec-1"].Name != "One" {
		t.Fatalf("securities = %v, %v", secs, err)
	}
	if tier, ok, err := src.Tier(ctx); err != nil || !ok || tier != 2 {
		t.Fatalf("tier = %d, %v, %v", tier, ok, err)
	}
	if got := src.Day(); got != "2026-11-12" {
		t.Fatalf("day = %q", got)
	}
	src.prep()
	if fake.clock == nil || !fake.clock().Equal(at) {
		t.Fatal("the source did not give the store its clock")
	}
}
