package watch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/dashboard"
	"github.com/b070nd/stoxicity/app/src/model"
)

var clock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Validate reports every problem in the watch, not just the first, so a user
// can fix a file in one pass.
func (w Watch) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("watch %q: "+format, append([]any{w.ID}, args...)...))
	}

	if !model.ValidID(w.ID) {
		bad("id must be 1-40 lower-case letters, digits or hyphens")
	}
	if strings.TrimSpace(w.Name) == "" {
		bad("name is empty")
	}
	if w.Lens.Threshold < 0 || w.Lens.Threshold > 1 {
		bad("lens.threshold %v is outside 0..1", w.Lens.Threshold)
	}
	if w.Lens.MinMateriality < 0 || w.Lens.MinMateriality > 5 {
		bad("lens.min_materiality %d is outside 0..5", w.Lens.MinMateriality)
	}
	for _, k := range w.Scope.Kinds {
		if !k.Valid() {
			bad("scope.kinds: unknown kind %q", k)
		}
	}
	for _, g := range w.Scope.Groups {
		if !g.Valid() {
			bad("scope.groups: unknown group %q", g)
		}
	}
	if err := dashboard.Validate(w.Dashboard); err != nil {
		bad("%v", err)
	}

	names := map[string]bool{}
	for i, a := range w.Alerts {
		if a.Name == "" {
			bad("alerts[%d]: name is empty", i)
		} else if names[a.Name] {
			bad("alerts[%d]: name %q is used twice", i, a.Name)
		}
		names[a.Name] = true
		if a.When.MinRelevance < 0 || a.When.MinRelevance > 1 {
			bad("alert %q: min_relevance %v is outside 0..1", a.Name, a.When.MinRelevance)
		}
		if a.When.MinMateriality < 0 || a.When.MinMateriality > 5 {
			bad("alert %q: min_materiality %d is outside 0..5", a.Name, a.When.MinMateriality)
		}
		for _, k := range a.When.Kinds {
			if !k.Valid() {
				bad("alert %q: unknown kind %q", a.Name, k)
			}
		}
		if len(a.Channels) == 0 {
			bad("alert %q: no channels", a.Name)
		}
		errs = append(errs, channels(w.ID, "alert "+a.Name, a.Channels)...)
		if _, err := channel.ParseWindow(a.Quiet); err != nil {
			bad("alert %q: %v", a.Name, err)
		}
		if a.Cooldown != "" {
			if d, err := time.ParseDuration(a.Cooldown); err != nil || d < 0 {
				bad("alert %q: cooldown %q is not a duration like 2h", a.Name, a.Cooldown)
			}
		}
	}

	switch w.Digest.Cadence {
	case "", "off":
	case "daily", "weekly":
		if !clock.MatchString(w.Digest.At) {
			bad("digest.at %q: want HH:MM", w.Digest.At)
		}
		if len(w.Digest.Channels) == 0 {
			bad("digest has a cadence but no channels")
		}
		errs = append(errs, channels(w.ID, "digest", w.Digest.Channels)...)
	default:
		bad("digest.cadence %q: want off, daily or weekly", w.Digest.Cadence)
	}
	return errors.Join(errs...)
}

func channels(id, where string, ks []channel.Kind) []error {
	var errs []error
	for _, k := range ks {
		if !k.Valid() {
			errs = append(errs, fmt.Errorf("watch %q: %s: unknown channel %q", id, where, k))
		}
	}
	return errs
}
