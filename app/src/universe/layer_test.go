package universe

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLayerRoundTripNormalizesAndIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t", FileName)
	l := Layer{
		Lists: []ListDef{{ID: "Mine", Tickers: []string{"ko", "KO", " pep "}}},
		Tags:  map[string][]string{"ko": {"Moat", "moat", "dividend"}, "empty": nil},
		Extra: []Extra{{Ticker: "nesn", Name: "Nestlé"}},
	}
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLayer(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lists[0].ID != "mine" || !slices.Equal(got.Lists[0].Tickers, []string{"KO", "PEP"}) {
		t.Errorf("lists = %+v", got.Lists)
	}
	if !slices.Equal(got.Tags["KO"], []string{"dividend", "moat"}) || len(got.Tags) != 1 {
		t.Errorf("tags = %v", got.Tags)
	}
	if got.Extra[0].Ticker != "NESN" {
		t.Errorf("extra = %+v", got.Extra)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("no temporary file may stay behind: %v", entries)
	}
}

func TestLoadLayerMissingFileIsEmptyAndBadFilesAreErrors(t *testing.T) {
	dir := t.TempDir()
	if l, err := LoadLayer(filepath.Join(dir, "none.yaml")); err != nil || len(l.Lists) != 0 {
		t.Fatalf("missing: %+v, %v", l, err)
	}
	for name, body := range map[string]string{
		"unknown key":    "lists: []\nwatch: x\n",
		"bad list id":    "lists:\n  - id: Bad Id\n    tickers: [A]\n",
		"duplicate list": "lists:\n  - id: a\n  - id: a\n",
		"bad tag":        "tags:\n  KO: [Not A Tag]\n",
		"extra no name":  "extra:\n  - ticker: X\n",
		"extra twice":    "extra:\n  - {ticker: X, name: A}\n  - {ticker: x, name: B}\n",
		"not yaml":       "lists: [",
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".yaml")
		os.WriteFile(path, []byte(body), 0o644)
		if _, err := LoadLayer(path); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLayerMutations(t *testing.T) {
	var l Layer
	if ch, err := l.AddTag("ko", "Moat"); err != nil || !ch {
		t.Fatalf("AddTag = %v, %v", ch, err)
	}
	if ch, _ := l.AddTag("KO", "moat"); ch {
		t.Error("adding a tag twice changes nothing")
	}
	if _, err := l.AddTag("KO", "not valid"); err == nil {
		t.Error("a bad tag is an error")
	}
	if _, err := l.AddTag("", "x"); err == nil {
		t.Error("no ticker is an error")
	}
	if !l.RemoveTag("ko", "MOAT") || l.RemoveTag("KO", "moat") || len(l.Tags) != 0 {
		t.Errorf("RemoveTag: tags = %v", l.Tags)
	}

	if ch, err := l.SetMember("Picks", "pep", true); err != nil || !ch || l.Lists[0].ID != "picks" || l.Lists[0].Name != "picks" {
		t.Fatalf("SetMember add: %v %v %+v", ch, err, l.Lists)
	}
	if ch, _ := l.SetMember("picks", "PEP", true); ch {
		t.Error("adding a member twice changes nothing")
	}
	l.SetMember("picks", "ko", true)
	if !slices.Equal(l.Lists[0].Tickers, []string{"KO", "PEP"}) {
		t.Errorf("members = %v", l.Lists[0].Tickers)
	}
	if ch, _ := l.SetMember("picks", "KO", false); !ch || !slices.Equal(l.Lists[0].Tickers, []string{"PEP"}) {
		t.Errorf("members = %v", l.Lists[0].Tickers)
	}
	if ch, _ := l.SetMember("other", "KO", false); ch || len(l.Lists) != 1 {
		t.Error("removing from a list that does not exist creates nothing")
	}
	if _, err := l.SetMember("Bad Id", "KO", true); err == nil {
		t.Error("a bad list ID is an error")
	}
}

func TestCloneSharesNothing(t *testing.T) {
	l := Layer{Lists: []ListDef{{ID: "a", Tickers: []string{"KO"}}}, Tags: map[string][]string{"KO": {"x"}}}
	c := l.Clone()
	c.SetMember("a", "PEP", true)
	c.AddTag("KO", "y")
	if len(l.Lists[0].Tickers) != 1 || len(l.Tags["KO"]) != 1 {
		t.Errorf("the original changed: %+v", l)
	}
}
