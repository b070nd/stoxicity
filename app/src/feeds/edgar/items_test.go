package edgar

import (
	"slices"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

// formItems is every item of Form 8-K as the form lists them.
var formItems = []string{
	"1.01", "1.02", "1.03", "1.04", "1.05",
	"2.01", "2.02", "2.03", "2.04", "2.05", "2.06",
	"3.01", "3.02", "3.03",
	"4.01", "4.02",
	"5.01", "5.02", "5.03", "5.04", "5.05", "5.06", "5.07", "5.08",
	"6.01", "6.02", "6.03", "6.04", "6.05",
	"7.01", "8.01", "9.01",
}

func TestEveryFormItemIsMappedOrIgnored(t *testing.T) {
	for _, item := range formItems {
		_, mapped := itemKinds[item]
		_, ignored := ignoredItems[item]
		if mapped == ignored {
			t.Errorf("item %s: mapped=%v ignored=%v, want exactly one", item, mapped, ignored)
		}
	}
}

func TestTablesHoldOnlyFormItems(t *testing.T) {
	for item, k := range itemKinds {
		if !slices.Contains(formItems, item) {
			t.Errorf("mapped item %s is not an 8-K item", item)
		}
		if !k.Valid() {
			t.Errorf("item %s maps to invalid kind %q", item, k)
		}
	}
	for item, reason := range ignoredItems {
		if !slices.Contains(formItems, item) {
			t.Errorf("ignored item %s is not an 8-K item", item)
		}
		if reason == "" {
			t.Errorf("ignored item %s has no reason", item)
		}
	}
}

func TestItemKind(t *testing.T) {
	tests := []struct {
		item string
		want model.EventKind
		ok   bool
	}{
		{"2.02", model.EventResults, true},
		{"1.03", model.EventBankruptcy, true},
		{"5.02", model.EventOfficerChange, true},
		{"8.01", model.EventOther, true},
		{"9.01", "", false},
		{"1.04", "", false},
		{"9.99", "", false},
		{"", "", false},
	}
	for _, c := range tests {
		got, ok := ItemKind(c.item)
		if got != c.want || ok != c.ok {
			t.Errorf("ItemKind(%q) = (%q, %v), want (%q, %v)", c.item, got, ok, c.want, c.ok)
		}
	}
}

func TestKinds(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		want  model.EventKind
		ok    bool
	}{
		{"single mapped item", []string{"2.02"}, model.EventResults, true},
		{"ignored item adds nothing", []string{"2.02", "9.01"}, model.EventResults, true},
		{"only ignored items", []string{"9.01", "1.04"}, "", false},
		{"no items", nil, "", false},
		{"unknown items", []string{"bogus", "10.01"}, "", false},
		{"highest base wins", []string{"8.01", "1.03"}, model.EventBankruptcy, true},
		{"highest base wins regardless of order", []string{"1.03", "2.02"}, model.EventBankruptcy, true},
		{"tie goes to the earlier item", []string{"2.01", "2.05"}, model.EventAcquisition, true},
		{"tie goes to the earlier item, reversed", []string{"2.05", "2.01"}, model.EventRestructuring, true},
		{"tie between two cyber-level items", []string{"1.05", "4.02"}, model.EventCyber, true},
		{"unknown item skipped before a tie", []string{"x", "5.02", "5.07"}, model.EventOfficerChange, true},
		{"other alone", []string{"7.01"}, model.EventOther, true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Kinds(c.items)
			if got != c.want || ok != c.ok {
				t.Errorf("Kinds(%q) = (%q, %v), want (%q, %v)", c.items, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseItems(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"prefixed list", "Item 2.02, Item 9.01", []string{"2.02", "9.01"}},
		{"bare list", "2.02,9.01", []string{"2.02", "9.01"}},
		{"plural with and", "Items 5.02 and 9.01", []string{"5.02", "9.01"}},
		{"duplicates removed, first order kept", "9.01, 2.02, 9.01", []string{"9.01", "2.02"}},
		{"trailing period", "Item 2.02.", []string{"2.02"}},
		{"lower case word", "item 1.01 (agreement) and 5.02", []string{"1.01", "5.02"}},
		{"longer number ignored", "12.02 and 1.234", nil},
		{"more digits after ignored", "2.025", nil},
		{"dollar amount ignored", "a $2.02 fee", nil},
		{"dotted version ignored", "version 1.2.02", nil},
		{"no items", "no items here", nil},
		{"empty", "", nil},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			got := ParseItems(c.in)
			if !slices.Equal(got, c.want) {
				t.Errorf("ParseItems(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEveryMappedItemHasAMaterialityAndEveryItemATitle(t *testing.T) {
	for item := range itemKinds {
		if m := itemMateriality[item]; m < 1 || m > 5 {
			t.Errorf("mapped item %s has materiality %d", item, m)
		}
	}
	for item := range itemMateriality {
		if _, ok := itemKinds[item]; !ok {
			t.Errorf("item %s has a materiality but no kind", item)
		}
	}
	for _, item := range formItems {
		if title, short, ok := model.ItemTitle(item); !ok || title == "" || short == "" {
			t.Errorf("item %s has no title", item)
		}
	}
}

func TestRateTakesTheMostMaterialItemAndCountsTheOthers(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		item  string
		kind  model.EventKind
		mat   int
		also  []string
		bonus float64
		ok    bool
	}{
		{"results alone", []string{"2.02"}, "2.02", model.EventResults, 3, nil, 0, true},
		{"exhibits are not information", []string{"2.02", "9.01"}, "2.02", model.EventResults, 3, nil, 0, true},
		{"results with a presentation", []string{"2.02", "7.01", "9.01"}, "2.02", model.EventResults, 3, []string{"7.01"}, 0.02, true},
		{"results, presentation and an officer change", []string{"2.02", "7.01", "5.02"}, "2.02", model.EventResults, 3, []string{"7.01", "5.02"}, 0.04, true},
		{"a restatement outweighs the results it comes with", []string{"2.02", "4.02"}, "4.02", model.EventNonReliance, 5, []string{"2.02"}, 0.02, true},
		{"two items of one kind add nothing", []string{"7.01", "8.01"}, "7.01", model.EventOther, 1, nil, 0, true},
		{"an agreement with its debt", []string{"1.01", "2.03"}, "1.01", model.EventAgreement, 3, []string{"2.03"}, 0.02, true},
		{"only exhibits", []string{"9.01"}, "", "", 0, nil, 0, false},
		{"ignored and unknown items", []string{"1.04", "9.99"}, "", "", 0, nil, 0, false},
		{"none", nil, "", "", 0, nil, 0, false},
	}
	for _, c := range tests {
		got, ok := Rate(c.items)
		if ok != c.ok || got.Item != c.item || got.Kind != c.kind || got.Materiality != c.mat ||
			!slices.Equal(got.Also, c.also) || got.Bonus != c.bonus {
			t.Errorf("%s: Rate(%v) = %+v, %v", c.name, c.items, got, ok)
		}
	}
	if r, _ := Rate([]string{"2.02", "7.01", "8.01", "5.02", "1.01", "3.02"}); r.Bonus != 0.06 {
		t.Errorf("the bonus stops at three items: %v", r.Bonus)
	}
}
