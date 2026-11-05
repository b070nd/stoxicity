package filter

import (
	"errors"
	"strings"
	"testing"

	"github.com/b070nd/stoxicity/app/src/model"
)

func testEnv() Env {
	return Env{Tickers: map[string]func(string) (map[string]struct{}, error){
		"list": func(v string) (map[string]struct{}, error) {
			if v == "none" {
				return nil, errors.New("no such list")
			}
			return map[string]struct{}{"KO": {}, "PEP": {}}, nil
		},
	}}
}

func TestEnvOperatorMatchesAnyTickerOfTheSet(t *testing.T) {
	q, err := ParseWith("list:staples dividend", testEnv())
	if err != nil {
		t.Fatal(err)
	}
	art := model.Article{Title: "Dividend raised", Tickers: []string{"ko"}}
	if !q.Match(art) {
		t.Error("an article naming KO is in the list")
	}
	art.Tickers = []string{"AAPL"}
	if q.Match(art) {
		t.Error("AAPL is not in the list")
	}
	art.Tickers = nil
	if q.Match(art) {
		t.Error("an article with no ticker is in no list")
	}
}

func TestEnvOperatorCombinesWithNegationAndOr(t *testing.T) {
	q, err := ParseWith("-list:staples OR ticker:KO", testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if q.Match(model.Article{Tickers: []string{"PEP"}}) {
		t.Error("PEP is in the list and is not KO")
	}
	if !q.Match(model.Article{Tickers: []string{"AAPL"}}) {
		t.Error("AAPL is outside the list")
	}
}

func TestEnvOperatorRoundTripsAndErrorsAtTheValue(t *testing.T) {
	q, err := ParseWith(`list:"my list"`, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if got := q.String(); got != `list:"my list"` {
		t.Errorf("String = %q", got)
	}
	_, err = ParseWith("dividend list:none", testEnv())
	var se *SyntaxError
	if !errors.As(err, &se) || se.Pos != len("dividend list:") || !strings.Contains(se.Msg, "no such list") {
		t.Errorf("err = %v", err)
	}
}

func TestEnvOperatorIsUnknownWithoutTheEnvAndListedWithIt(t *testing.T) {
	if _, err := Parse("list:x"); err == nil {
		t.Error("Parse alone must not know list:")
	}
	_, err := ParseWith("nope:x", testEnv())
	if err == nil || !strings.Contains(err.Error(), "list") || !strings.Contains(err.Error(), "ticker") {
		t.Errorf("the error should list built-in and extra operators: %v", err)
	}
}

func TestEnvOperatorNeedsNoFullTextPart(t *testing.T) {
	q, _ := ParseWith("list:staples", testEnv())
	if _, ok := q.FTS(); ok {
		t.Error("a set operator gives the store nothing to narrow with")
	}
}
