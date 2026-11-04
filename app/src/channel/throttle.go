package channel

import (
	"fmt"
	"sync"
	"time"
)

// Window is a daily span of time, used for quiet hours. It may cross
// midnight ("22:00-07:00"). The zero Window is empty and contains no time.
type Window struct {
	Start, End int // minutes since midnight
}

// ParseWindow reads "HH:MM-HH:MM". An empty string is the empty Window.
func ParseWindow(s string) (Window, error) {
	if s == "" {
		return Window{}, nil
	}
	var sh, sm, eh, em int
	if _, err := fmt.Sscanf(s, "%d:%d-%d:%d", &sh, &sm, &eh, &em); err != nil ||
		sh < 0 || sh > 23 || eh < 0 || eh > 23 || sm < 0 || sm > 59 || em < 0 || em > 59 {
		return Window{}, fmt.Errorf("channel: quiet hours %q: want HH:MM-HH:MM", s)
	}
	return Window{Start: sh*60 + sm, End: eh*60 + em}, nil
}

// Empty reports whether the window contains no time at all.
func (w Window) Empty() bool { return w.Start == w.End }

// Contains reports whether t, in its own time zone, falls inside the window.
func (w Window) Contains(t time.Time) bool {
	if w.Empty() {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if w.Start < w.End {
		return m >= w.Start && m < w.End
	}
	return m >= w.Start || m < w.End
}

// Policy limits how often one alert may fire.
type Policy struct {
	Quiet    Window
	Cooldown time.Duration // minimum gap between two deliveries of the same alert
}

// Throttle applies Policies and remembers delivered events. It keeps its state
// in memory, so a restart forgets it; the store keeps the durable record.
type Throttle struct {
	mu   sync.Mutex
	last map[string]time.Time
	done map[string]struct{}
}

// NewThrottle returns an empty Throttle.
func NewThrottle() *Throttle {
	return &Throttle{last: map[string]time.Time{}, done: map[string]struct{}{}}
}

// Allow reports whether the alert identified by key may fire at now, and if
// so counts it as fired. Quiet hours and the cooldown both block it; a blocked
// call changes nothing.
func (t *Throttle) Allow(key string, now time.Time, p Policy) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p.Quiet.Contains(now) {
		return false
	}
	if last, ok := t.last[key]; ok && p.Cooldown > 0 && now.Sub(last) < p.Cooldown {
		return false
	}
	t.last[key] = now
	return true
}

// Once reports true the first time it sees key and false afterwards. Use it
// with Notification.Key to deliver an event once.
func (t *Throttle) Once(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.done[key]; ok {
		return false
	}
	t.done[key] = struct{}{}
	return true
}
