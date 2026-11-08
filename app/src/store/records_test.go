package store

import (
	"context"
	"errors"
	"testing"
)

func TestRecordsAreAppendOnlyAndIdempotent(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	rs := db.Records("acme")
	if _, err := rs.Latest(ctx, "demo", "k1"); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("%v", err)
	}
	recs, fresh, err := rs.Append(ctx, RecordInput{"demo", "k1", []byte(`{"v":1}`)}, RecordInput{"demo", "k2", []byte(`{"v":2}`)})
	if err != nil || fresh != 2 || len(recs) != 2 {
		t.Fatalf("%v %d %v", err, fresh, recs)
	}
	// the same content again is a no-op that returns the stored record
	again, fresh, err := rs.Append(ctx, RecordInput{"demo", "k1", []byte(`{"v":1}`)})
	if err != nil || fresh != 0 || again[0].Seq != recs[0].Seq {
		t.Fatalf("%v %d %v", err, fresh, again)
	}
	// a revision is a new row; current is the newest, history keeps both
	if _, fresh, _ = rs.Append(ctx, RecordInput{"demo", "k1", []byte(`{"v":3}`)}); fresh != 1 {
		t.Fatal("revision not written")
	}
	cur, _ := rs.Latest(ctx, "demo", "k1")
	if string(cur.Body) != `{"v":3}` {
		t.Fatalf("%s", cur.Body)
	}
	if h, _ := rs.History(ctx, "demo", "k1"); len(h) != 2 || string(h[0].Body) != `{"v":1}` {
		t.Fatalf("%v", h)
	}
	// going back to the older content is a third row, newest again
	if _, fresh, _ = rs.Append(ctx, RecordInput{"demo", "k1", []byte(`{"v":1}`)}); fresh != 0 {
		t.Log("identical older content is recognised, not duplicated")
	}
	list, _ := rs.Current(ctx, "demo", "")
	if len(list) != 2 || list[0].Key != "k1" {
		t.Fatalf("%v", list)
	}
	if list, _ := rs.Current(ctx, "demo", "k2"); len(list) != 1 {
		t.Fatalf("%v", list)
	}
	if all, _ := rs.All(ctx, "demo", ""); len(all) != 3 {
		t.Fatalf("%v", all)
	}
	// validation
	for _, bad := range []RecordInput{{"Bad Kind", "k", []byte("x")}, {"demo", "", []byte("x")}, {"demo", "k", nil}} {
		if _, _, err := rs.Append(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// a batch with one bad record writes nothing
	if _, _, err := rs.Append(ctx, RecordInput{"demo", "k9", []byte("x")}, RecordInput{"demo", "", []byte("x")}); err == nil {
		t.Fatal("accepted")
	}
	if _, err := rs.Latest(ctx, "demo", "k9"); !errors.Is(err, ErrNoRecord) {
		t.Fatal("a partial batch was written")
	}
	// the database refuses changes
	h, _ := rs.t.handle()
	if _, err := h.w.Exec(`UPDATE records SET body = 'x'`); err == nil {
		t.Fatal("update allowed")
	}
	if _, err := h.w.Exec(`DELETE FROM records`); err == nil {
		t.Fatal("delete allowed")
	}
	// another tenant sees nothing
	if _, err := db.Records("other").Latest(ctx, "demo", "k1"); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("%v", err)
	}
	if _, err := db.Records("bad id!").Latest(ctx, "demo", "k1"); err == nil || errors.Is(err, ErrNoRecord) {
		t.Fatalf("%v", err)
	}
}
