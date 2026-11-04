package entity

import "strings"

// ambiguousWords holds lower-case words that are ordinary English words or
// brand-ambiguous words (Apple, Target, Visa, Block, Ford, Shell...). A
// company whose only name word is on this list, or is shorter than four
// letters, is matched by name only when a second signal in the same text backs
// it up. The list also covers generic first words of multi-word names
// ("General", "American", "Texas") so that "General Motors" is never found by
// the word "General" alone.
var ambiguousWords = wordSet(strings.Fields(`
apple target visa block square amazon shell ford gap oracle meta intel snap
match zoom slack rocket ross ball dollar general american united national first
global international bank delta southern western eastern northern central
pacific atlantic texas home best express century capital energy power digital
systems technologies solutions services industries resources partners trust
financial insurance health medical life sun star summit pioneer liberty freedom
eagle phoenix atlas apex alpha omega nova genesis vista ally chase discover
progressive hartford cardinal comfort crown cooper hunt mercury mosaic compass
edge focus fortune franklin harbor horizon icon impact premier prime quest
regal sapphire spirit standard stellar sterling strategic synergy tower
trinity universal unity venture victory vital wave johnson morgan wells air
sky cloud gold silver iron steel coal oil gas water land park city county
state union republic empire dominion north south east west
`))

// tickerStoplist holds upper-case words and abbreviations that are tickers on
// some exchange but are far more often ordinary text. A bare occurrence of one
// of these is never taken as a company; only a cashtag or an exchange-qualified
// form can select it.
var tickerStoplist = wordSet(strings.Fields(`
A I IT ON ALL ARE KEY NOW FOR CEO CFO USA SEC ETF IPO GDP EPS YOY
AN AS AT BE BY DO GO HE IF IN IS ME MY NO OF OR SO TO UP US WE AM OK
AND BUT CAN HAS HAD HER HIS HOW ITS MAY NEW NOT ONE OUR OUT SEE THE TWO
WAS WHO WHY YOU ANY DAY GET GOT LET OLD OWN PUT SAY SHE TOO USE WAY
CTO COO CIO UK EU UN USD EUR GBP JPY CNY CPI PPI PMI FED FOMC ECB IMF BOE BOJ
NYSE NASDAQ AMEX OTC LLC INC LTD PLC AI ML PR HR TV YTD QOQ MOM ROI ROE ESG
SPAC REIT ADR FY EBIT IRS DOJ FDA FTC FCC EPA NYC LA DC OPEC NATO ASAP FAQ
AGM EGM PE GAAP IFRS ARM BIG FUN CAR EAT RUN WELL
LOVE TRUE FAST GOOD PEAK OPEN LIVE HOPE CASH GOLD BEST REAL SAFE PLAY FREE
`))

// exchanges holds the lower-case names that make a "NAME: TICKER" form an
// exchange-qualified ticker. Multi-word names are separated by one space.
var exchanges = wordSet(strings.Fields(`
nasdaq nasdaqgs nasdaqgm nasdaqcm nyse nyseamerican nysearca nysemkt amex
otc otcqb otcqx otcmkts tsx tsxv cve lse aim asx nzx hkex sehk sgx tse jpx krx
nse bse xetra fra fwb epa bit mil six vtx omx sto cph hel ams ebr bme bvmf b3
jse tase
`))

func init() {
	for _, two := range []string{
		"nyse american", "nyse arca", "nyse mkt", "otc markets", "nasdaq gs",
		"nasdaq gm", "nasdaq cm", "tsx venture", "euronext paris",
	} {
		exchanges[two] = struct{}{}
	}
}

// contextWords holds the lower-case finance words whose presence in a sentence
// makes a bare ticker or an ambiguous name believable.
var contextWords = wordSet(strings.Fields(`
shares stock stocks earnings quarter quarterly revenue revenues dividend
dividends guidance filing filings
`))

// contextAbbreviations holds finance abbreviations that count as context only
// in exactly this spelling, because their lower-case forms are other words.
var contextAbbreviations = wordSet([]string{"CEO", "SEC", "NASDAQ", "Nasdaq", "NYSE"})

// corporateSuffixes holds the lower-case trailing words that are dropped from
// an entity name before it is matched against text.
var corporateSuffixes = wordSet(strings.Fields(`
inc incorporated corp corporation co company companies ltd limited plc sa ag nv
llc lp holdings holding group se ab oyj spa bv asa
`))

// sentenceAbbreviations holds lower-case words whose trailing full stop does
// not end a sentence.
var sentenceAbbreviations = wordSet(strings.Fields(`
inc corp co ltd plc llc cos mr mrs ms dr jr sr st vs etc
`))

func wordSet(words []string) map[string]struct{} {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[w] = struct{}{}
	}
	return set
}
