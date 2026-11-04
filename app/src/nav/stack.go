package nav

import "slices"

// Crumb is one entry of the breadcrumb bar.
type Crumb struct {
	Label string
	Route Route
}

// Stack is the trail of screens the user followed, plus the screens they went
// back from, and the watch the screens show. The zero value is not usable; call
// New.
type Stack struct {
	trail   []Route
	forward []Route
	watch   string
}

// New starts a trail at Home, with no watch chosen yet.
func New() *Stack { return &Stack{trail: []Route{{Kind: Home}}} }

// Current is the screen being shown.
func (s *Stack) Current() Route { return s.trail[len(s.trail)-1] }

// Watch is the watch whose data the screens show.
func (s *Stack) Watch() string { return s.watch }

// SwitchWatch shows the same screens for another watch. The trail, the current
// screen and the forward history stay as they are, so a user looking at
// Home › Topics › AI infrastructure sees the same topic page for the new
// watch. Screens that belong to no watch are left alone.
func (s *Stack) SwitchWatch(id string) {
	s.watch = id
	for _, list := range [][]Route{s.trail, s.forward} {
		for i := range list {
			if list[i].Scoped() {
				list[i].Watch = id
			}
		}
	}
}

// stamp gives a watch-scoped route the current watch if it names none.
func (s *Stack) stamp(r Route) Route {
	if r.Scoped() && r.Watch == "" {
		r.Watch = s.watch
	}
	return r
}

// Go opens r. A section starts a new trail (Home › Section); a detail route
// is added under its list (Home › Topics › AI infrastructure), and opening
// another detail from a detail extends the trail (… › NVDA). Opening a route
// already on the trail cuts the trail back to it. Going anywhere clears forward.
// A route that names another watch (a deep link) switches to it first.
func (s *Stack) Go(r Route) {
	if r.Scoped() && r.Watch != "" && r.Watch != s.watch {
		s.SwitchWatch(r.Watch)
	}
	r = s.stamp(r)
	s.forward = nil
	if i := slices.Index(s.trail, r); i >= 0 {
		s.trail = s.trail[:i+1]
		return
	}
	if r.Section() {
		s.trail = []Route{s.stamp(Route{Kind: Home})}
		s.trail = append(s.trail, r)
		return
	}
	if parent, ok := r.Parent(); ok && !slices.Contains(s.trail, parent) && s.Current().Section() {
		s.trail = append(s.trail, parent)
	}
	s.trail = append(s.trail, r)
}

// Back returns to the previous screen; it reports false at Home.
func (s *Stack) Back() bool {
	if len(s.trail) == 1 {
		return false
	}
	s.forward = append(s.forward, s.Current())
	s.trail = s.trail[:len(s.trail)-1]
	return true
}

// Forward undoes the last Back.
func (s *Stack) Forward() bool {
	if len(s.forward) == 0 {
		return false
	}
	s.trail = append(s.trail, s.forward[len(s.forward)-1])
	s.forward = s.forward[:len(s.forward)-1]
	return true
}

// Crumbs is the breadcrumb bar, from Home to the current screen.
func (s *Stack) Crumbs() []Crumb {
	out := make([]Crumb, len(s.trail))
	for i, r := range s.trail {
		out[i] = Crumb{Label: r.Label(), Route: r}
	}
	return out
}

// JumpTo makes crumb i the current screen, dropping the crumbs after it.
func (s *Stack) JumpTo(i int) bool {
	if i < 0 || i >= len(s.trail) {
		return false
	}
	s.forward = nil
	s.trail = s.trail[:i+1]
	return true
}

// Next returns the watch dir steps (1 or -1) from cur in ids, wrapping at the
// ends. If cur is not in ids it returns the first (dir > 0) or last (dir < 0).
// It returns "" when ids is empty. The TUI binds it to tab and shift-tab.
func Next(ids []string, cur string, dir int) string {
	n := len(ids)
	if n == 0 {
		return ""
	}
	i := slices.Index(ids, cur)
	if i < 0 {
		if dir < 0 {
			return ids[n-1]
		}
		return ids[0]
	}
	return ids[((i+dir)%n+n)%n]
}

// Nth returns the n-th watch, counting from 1, for the number-key shortcuts.
func Nth(ids []string, n int) (string, bool) {
	if n < 1 || n > len(ids) {
		return "", false
	}
	return ids[n-1], true
}
