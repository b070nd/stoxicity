package entity

import (
	"strings"

	"github.com/b070nd/stoxicity/app/src/model"
)

// A ticker names a security only on its own market: ABC on the London Stock
// Exchange is not ABC on Nasdaq. The text says which market with a prefix such
// as "LSE: ABC", and an entity says where it is listed with its MIC or exchange
// name. Both are reduced to a market area, because the areas are what tickers
// are unique within.

// prefixArea maps the lower-case exchange names a text may use before a ticker
// to the market area.
var prefixArea = map[string]string{}

// micArea maps ISO 10383 market identifiers to the market area.
var micArea = map[string]string{}

func init() {
	for area, names := range map[string]string{
		"us": "nasdaq nasdaqgs nasdaqgm nasdaqcm nyse nyseamerican nysearca nysemkt amex otc otcqb otcqx otcmkts " +
			"nyse_american nyse_arca nyse_mkt otc_markets nasdaq_gs nasdaq_gm nasdaq_cm",
		"ca": "tsx tsxv cve tsx_venture",
		"uk": "lse aim",
		"au": "asx",
		"nz": "nzx",
		"hk": "hkex sehk",
		"sg": "sgx",
		"jp": "tse jpx",
		"kr": "krx",
		"in": "nse bse",
		"de": "xetra fra fwb",
		"fr": "epa euronext_paris",
		"it": "bit mil",
		"ch": "six vtx",
		"se": "omx sto",
		"dk": "cph",
		"fi": "hel",
		"nl": "ams",
		"be": "ebr",
		"es": "bme",
		"br": "bvmf b3",
		"za": "jse",
		"il": "tase",
	} {
		for _, n := range strings.Fields(names) {
			prefixArea[strings.ReplaceAll(n, "_", " ")] = area
			prefixArea[n] = area
		}
	}
	for area, mics := range map[string]string{
		"us": "XNAS XNYS XASE ARCX BATS XOTC OTCM XNMS XNCM XNGS IEXG",
		"ca": "XTSE XTSX",
		"uk": "XLON AIMX",
		"au": "XASX",
		"nz": "XNZE",
		"hk": "XHKG",
		"sg": "XSES",
		"jp": "XTKS",
		"kr": "XKRX XKOS",
		"in": "XNSE XBOM",
		"de": "XETR XFRA",
		"fr": "XPAR",
		"it": "XMIL",
		"ch": "XSWX",
		"se": "XSTO",
		"dk": "XCSE",
		"fi": "XHEL",
		"nl": "XAMS",
		"be": "XBRU",
		"es": "XMAD",
		"br": "BVMF XBSP",
		"za": "XJSE",
		"il": "XTAE",
	} {
		for _, m := range strings.Fields(mics) {
			micArea[m] = area
		}
	}
}

// venueOfPrefix is the market area an exchange prefix in a text names, "" when
// the prefix is not one the table knows.
func venueOfPrefix(prefix string) string {
	return prefixArea[strings.ToLower(strings.Join(strings.Fields(prefix), " "))]
}

// venueOf is the market area an entity is listed in, "" when it does not say
// (or says something the tables do not know).
func venueOf(e model.Entity) string {
	if a, ok := micArea[strings.ToUpper(strings.TrimSpace(e.MIC))]; ok {
		return a
	}
	name := strings.ToLower(strings.TrimSpace(e.Exchange))
	if a, ok := prefixArea[name]; ok {
		return a
	}
	for _, w := range strings.Fields(strings.NewReplacer("-", " ", "/", " ", ",", " ").Replace(name)) {
		if a, ok := prefixArea[w]; ok {
			return a
		}
	}
	return ""
}

// contradicts says two market areas are both known and differ.
func contradicts(a, b string) bool { return a != "" && b != "" && a != b }
