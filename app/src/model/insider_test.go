package model

import "testing"

func TestIsCommonNamesCommonSharesAndNothingElse(t *testing.T) {
	for title, want := range map[string]bool{
		"Common Stock": true, "Class A Common Stock": true, "Ordinary Shares": true, "Common Shares, no par value": true,
		"Capital Stock": true, "Shares of Beneficial Interest": true,
		"Series A Preferred Stock": false, "Common Stock Warrant": false, "Stock Option (Right to Buy)": false,
		"6.5% Convertible Senior Notes due 2030": false, "Restricted Stock Unit": false, "Phantom Common Stock": false,
		"": false, "Deferred Stock Units - DDECP": false,
	} {
		if got := (InsiderTrade{Security: title}).IsCommon(); got != want {
			t.Errorf("%q: IsCommon = %v, want %v", title, got, want)
		}
	}
}
