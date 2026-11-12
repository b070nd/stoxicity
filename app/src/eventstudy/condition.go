package eventstudy

import (
	"fmt"
	"strconv"
	"strings"
)

// Condition is one test of an event: of a figure, a word, its sector or the
// conditions around it. It is data, so that a study and a signal rule can state
// their conditions before they are run and be compared by hash.
type Condition struct {
	// Field is "number:<name>" for a figure of the event, "label:<name>" for a
	// word of it, "sector", or "context:<dimension>" for a bucket of Dimensions.
	Field string `json:"field"`
	// Op is gt, ge, lt, le or eq for a figure, and is or not for a word.
	Op    string  `json:"op"`
	Value float64 `json:"value,omitempty"`
	Text  string  `json:"text,omitempty"`
}

func (c Condition) kind() (kind, name string) {
	if c.Field == "sector" {
		return "sector", ""
	}
	kind, name, _ = strings.Cut(c.Field, ":")
	return kind, name
}

// Validate says why the condition cannot be evaluated.
func (c Condition) Validate() error {
	kind, name := c.kind()
	switch kind {
	case "number":
		if name == "" {
			return fmt.Errorf("eventstudy: condition %q names no figure", c.Field)
		}
		switch c.Op {
		case "gt", "ge", "lt", "le", "eq":
			return nil
		}
		return fmt.Errorf("eventstudy: condition on %s: op %q is not gt, ge, lt, le or eq", c.Field, c.Op)
	case "label", "sector", "context":
		if kind == "label" && name == "" {
			return fmt.Errorf("eventstudy: condition %q names no label", c.Field)
		}
		if kind == "context" && !isDimension(name) {
			return fmt.Errorf("eventstudy: condition %q names no context dimension", c.Field)
		}
		if c.Op != "is" && c.Op != "not" {
			return fmt.Errorf("eventstudy: condition on %s: op %q is not is or not", c.Field, c.Op)
		}
		if c.Text == "" {
			return fmt.Errorf("eventstudy: condition on %s needs the word to compare with", c.Field)
		}
		return nil
	}
	return fmt.Errorf("eventstudy: condition field %q is not number:<name>, label:<name>, sector or context:<dimension>", c.Field)
}

func isDimension(name string) bool {
	for _, d := range Dimensions() {
		if d == name {
			return true
		}
	}
	return false
}

// Holds reports whether the condition is met. known is false when the event
// does not carry what the condition tests (a figure it lacks, a condition that
// could not be measured): an unknown is never taken for met.
func (c Condition) Holds(o Observation, ctx Context) (holds, known bool) {
	kind, name := c.kind()
	switch kind {
	case "number":
		v, ok := o.Numbers[name]
		if !ok {
			return false, false
		}
		switch c.Op {
		case "gt":
			return v > c.Value, true
		case "ge":
			return v >= c.Value, true
		case "lt":
			return v < c.Value, true
		case "le":
			return v <= c.Value, true
		case "eq":
			return v == c.Value, true
		}
		return false, false
	case "label", "sector", "context":
		var word string
		switch kind {
		case "label":
			word = o.Labels[name]
		case "sector":
			word = o.Sector
		default:
			word = ctx.Bucket(name)
		}
		if word == "" || word == Unknown {
			return false, false
		}
		same := strings.EqualFold(word, c.Text)
		if c.Op == "not" {
			return !same, true
		}
		return same, c.Op == "is"
	}
	return false, false
}

// String is the condition in words, for explanations.
func (c Condition) String() string {
	kind, _ := c.kind()
	if kind == "number" {
		return c.Field + " " + c.Op + " " + strconv.FormatFloat(c.Value, 'g', -1, 64)
	}
	return c.Field + " " + c.Op + " " + c.Text
}
