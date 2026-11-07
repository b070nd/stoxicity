package model

import "testing"

func TestDescribeItemsAndLabels(t *testing.T) {
	got := DescribeItems([]string{"2.02", "9.01", "9.99"})
	if got[0] != "2.02 Results of Operations and Financial Condition" || got[1] != "9.01 Financial Statements and Exhibits" || got[2] != "9.99" {
		t.Errorf("%q", got)
	}
	if l := ItemLabel([]string{"2.02", "7.01", "9.01"}, 3); l != "results, Reg FD" {
		t.Errorf("label %q", l)
	}
	if l := ItemLabel([]string{"6.01", "6.02", "5.02", "8.01"}, 2); l != "asset-backed, officers and directors" {
		t.Errorf("label %q: the same label twice is written once, and n limits them", l)
	}
	if ItemLabel([]string{"9.01"}, 3) != "" || ItemLabel(nil, 3) != "" {
		t.Error("exhibits alone say nothing")
	}
	if _, _, ok := ItemTitle("2.99"); ok {
		t.Error("2.99 is no item")
	}
}
