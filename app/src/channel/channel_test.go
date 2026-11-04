package channel

import (
	"testing"
	"time"

	"github.com/b070nd/stoxicity/app/src/model"
)

func at(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseWindow(t *testing.T) {
	for _, s := range []string{"nope", "25:00-07:00", "22:00-07:60", "22:00"} {
		if _, err := ParseWindow(s); err == nil {
			t.Errorf("ParseWindow(%q): expected an error", s)
		}
	}
	w, err := ParseWindow("")
	if err != nil || !w.Empty() {
		t.Errorf("empty string = %+v, %v; want the empty window", w, err)
	}
}

func TestWindowContains(t *testing.T) {
	night, _ := ParseWindow("22:00-07:00")
	day, _ := ParseWindow("09:00-17:00")
	cases := []struct {
		w    Window
		t    string
		want bool
	}{
		{night, "23:30", true},
		{night, "03:00", true},
		{night, "07:00", false}, // end is exclusive
		{night, "12:00", false},
		{day, "09:00", true},
		{day, "17:00", false},
		{Window{}, "12:00", false},
	}
	for _, c := range cases {
		if got := c.w.Contains(at(c.t)); got != c.want {
			t.Errorf("%+v.Contains(%s) = %v, want %v", c.w, c.t, got, c.want)
		}
	}
}

func TestThrottleAllow(t *testing.T) {
	th := NewThrottle()
	night, _ := ParseWindow("22:00-07:00")
	p := Policy{Quiet: night, Cooldown: 2 * time.Hour}

	if th.Allow("k", at("23:00"), p) {
		t.Error("allowed during quiet hours")
	}
	if !th.Allow("k", at("08:00"), p) {
		t.Error("first call outside quiet hours should be allowed")
	}
	if th.Allow("k", at("09:00"), p) {
		t.Error("allowed inside the cooldown")
	}
	if !th.Allow("k", at("10:30"), p) {
		t.Error("should be allowed after the cooldown")
	}
	if !th.Allow("other", at("10:31"), p) {
		t.Error("a different key has its own cooldown")
	}
}

func TestThrottleOnce(t *testing.T) {
	th := NewThrottle()
	if !th.Once("e") || th.Once("e") || !th.Once("f") {
		t.Fatal("Once should be true on first sight of a key only")
	}
}

func TestNotificationKey(t *testing.T) {
	a := model.Article{ID: "1", ClusterID: "c1"}
	b := model.Article{ID: "2", ClusterID: "c1"} // syndicated copy of a
	c := model.Article{ID: "3"}
	n1 := Notification{TenantID: "t", WatchID: "w", Alert: "x", Articles: []model.Article{a, c}}
	n2 := Notification{TenantID: "t", WatchID: "w", Alert: "x", Articles: []model.Article{c, b}}
	if n1.Key() != n2.Key() {
		t.Errorf("same event gave different keys: %q vs %q", n1.Key(), n2.Key())
	}
	n3 := n1
	n3.WatchID = "other"
	if n1.Key() == n3.Key() {
		t.Error("different watches must not share a key")
	}
}
