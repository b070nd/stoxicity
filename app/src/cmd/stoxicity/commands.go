package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// command is one subcommand.
type command struct {
	name    string
	usage   string // the command line, without "stoxicity" and the global flags
	summary string
	// bootstrap says the command needs the environment, configuration and
	// sources. tolerant says it reports a failed start-up step itself.
	bootstrap, tolerant bool
	run                 func(ctx context.Context, a *app, args []string) error
}

// commands lists the subcommands in the order the usage shows them.
func commands() []command {
	return []command{
		{name: "fetch", usage: "fetch [source-id...]", summary: "poll sources once and store what they have", bootstrap: true, run: cmdFetch},
		{name: "run", usage: "run", summary: "poll the enabled sources until interrupted", bootstrap: true, run: cmdRun},
		{name: "digest", usage: "digest [-day YYYY-MM-DD]", summary: "the day's relevant news, domains and trends", bootstrap: true, run: cmdDigest},
		{name: "search", usage: "search [-from DAY] [-to DAY] [-limit N] [--] QUERY...", summary: "filter stored news with a Google-style query", bootstrap: true, run: cmdSearch},
		{name: "stock", usage: "stock TICKER [-days 30] [-limit 10]", summary: "the page of one stock: summary, timeline, trend, domains", bootstrap: true, run: cmdStock},
		{name: "stocks", usage: "stocks [-days 14]", summary: "stocks seen in relevant news, with materiality and tone", bootstrap: true, run: cmdStocks},
		{name: "topic", usage: "topic NAME [-days 30] [-limit 10]", summary: "the page of one topic: its stocks, domains, timeline", bootstrap: true, run: cmdTopic},
		{name: "topics", usage: "topics [-days 14]", summary: "all topics as a tag cloud with counts and trend lift", bootstrap: true, run: cmdTopics},
		{name: "coverage", usage: "coverage [-days 7] [-limit 20] [-backing B] [-thin] [-all]", summary: "stories by how well they are covered and what they rest on", bootstrap: true, run: cmdCoverage},
		{name: "label", usage: "label keep|drop ID... [-sampled] | list | sample | queue", summary: "record keep/drop verdicts, list them, draw a sample, or get the most useful articles to label", bootstrap: true, run: cmdLabel},
		{name: "events", usage: "events rebuild [-days N] | timeline TICKER [-at TIME] | show EVENT", summary: "the append-only event history: rebuild it from the articles, a stock's timeline as known at a time, an event's revisions", bootstrap: true, run: cmdEvents},
		{name: "signals", usage: "signals [-days 7] [-at TIME] [-explain] [-min 0] [-limit 20] [-all]", summary: "securities whose events rest on primary or corroborated evidence (independent reporting origins; materiality plays no part), and whether one episode dominates", bootstrap: true, run: cmdSignals},
		{name: "graph", usage: "graph TICKER [-days 30] [-format text|json|dot]", summary: "the small knowledge graph around one security: events, roles, topics, publishers", bootstrap: true, run: cmdGraph},
		{name: "export", usage: "export [-out research] [-days 30]", summary: "write the events and securities as Markdown files for Git (only changed files are written)", bootstrap: true, run: cmdExport},
		{name: "reconcile", usage: "reconcile [-days 7] [-from DAY -to DAY] [-catchup 90] [-enrich] [-facts] [-repair]", summary: "compare the SEC's filing indexes with the stored filings, resumably, and show (or fill in, as backfills) what the live feed missed", bootstrap: true, run: cmdReconcile},
		{name: "eval", usage: "eval [-days 90] [-threshold X] | eval claims [-json]", summary: "measure the lens against your labels, or the claim reader on the challenge corpus", bootstrap: true, run: cmdEval},
		{name: "relink", usage: "relink [-days N]", summary: "give stored SEC filings the tickers of their CIK (all of them, or the last N days)", bootstrap: true, run: cmdRelink},
		{name: "rescore", usage: "rescore [-days N | -from DAY -to DAY]", summary: "score stored articles again with the current lens", bootstrap: true, run: cmdRescore},
		{name: "classify", usage: "classify [-no-laya] TEXT... | -", summary: "show how one headline is scored and classified; stores nothing", bootstrap: true, run: cmdClassify},
		{name: "universe", usage: "universe status | update | enrich | find QUERY... | show TICKER | tag TICKER TAG... | list [ID]", summary: "every known stock: search, filter, tag and group them", bootstrap: true, run: cmdUniverse},
		{name: "company", usage: "company status | update [-force] | find TEXT...", summary: "the company master (SEC tickers and names): refresh or search it", bootstrap: true, run: cmdCompany},
		{name: "gmail", usage: "gmail status | auth | test", summary: "set up and test the email channel (Gmail API with OAuth)", bootstrap: true, run: cmdGmail},
		{name: "watch", usage: "watch list | check [id...] | test ID [-since 24h]", summary: "list, validate or try the tenant's watches", bootstrap: true, run: cmdWatch},
		{name: "policy", usage: "policy example [-profile P] | show [-at TIME] [-json] | check -file F | preview -file F [-json] | activate -file F -confirm HASH [-accept-risk] | history", summary: "the investment policy: an example to start from, check and preview a file, activate it with the hash you read, show what was in force", bootstrap: true, run: cmdPolicy},
		{name: "tenant", usage: "tenant list", summary: "list tenants", bootstrap: true, run: cmdTenant},
		{name: "instruments", usage: "instruments example | check -file F | import -file F -source TEXT | list [-at TIME] | resolve [-isin I] [-mic M -ticker T] [-currency C] [-broker B -conid ID] -day D [-security]", summary: "what can be bought: securities, share classes, listings with currency and quote unit, resolved without guessing", bootstrap: true, run: cmdInstruments},
		{name: "fx", usage: "fx fetch [-feed daily|90d|history] | import -file F -source TEXT | list | rate -from EUR -to USD -day D [-max-age N] [-at TIME] [-basis B]", summary: "exchange-rate observations: fetch the ECB reference rates, import your own, ask for the rate usable on a day (stale, conflicting or missing is refused)", bootstrap: true, run: cmdFX},
		{name: "prices", usage: "prices import -file F -source TEXT | at (-mic M -ticker T | -isin I) -day D [-max-age N] [-at TIME] [-adjustment A] [-require-stated-unit]", summary: "price observations with their quote unit: import your own, ask for the price usable on a day (wrong unit, stale or conflicting is refused)", bootstrap: true, run: cmdPrices},
		{name: "dividends", usage: "dividends import -file F -source TEXT | calendar [-isin I | -mic M -ticker T] [-from D] [-to D] [-at TIME] [-splits F] | quality (-isin I | -mic M -ticker T) -day D [-splits F] [-sector S] [-fcf-per-share X -fcf-currency C]", summary: "dividend and fund-distribution observations: import them, list the calendar, judge a security's history (declared vs estimated, cuts, gaps; nothing is cash)", bootstrap: true, run: cmdDividends},
		{name: "portfolio", usage: "portfolio book create|list|archive|reset | add BOOK KIND -date D [columns] | import BOOK -file F | transfer -from B -to B -date D (-amount A | -security S -qty Q) | show BOOK [-as-of T] [-basis event|recorded] [-lots] [-json] | journal BOOK [-limit N] [-json] | verify [BOOK...] | reconcile BOOK -file STATEMENT.csv -as-of T", summary: "accounts and the immutable ledger: real and paper books, deposits, trades, income, FX, splits, transfers, reversals; everything shown is a sum of recorded lines", bootstrap: true, run: cmdPortfolio},
		{name: "scorecard", usage: "scorecard example | import -file F | template -security S -template T | annotate -security S -topic T -stance X -text T -source S -ref R | show -security S [-as-of DAY] [-years N] [-json] [-reasons] | versions", summary: "multi-year sourced scorecards: figures with provenance, sector templates, ratios blank for a stated reason, qualitative notes, content-hashed with keyword and rule versions", bootstrap: true, run: cmdScorecard},
		{name: "strategy", usage: "strategy config example|set -file F|review|show | assumptions example|set -security S -file F|show -security S | propose -security S [-as-of DAY] [-price \"22.50 EUR\" -price-day DAY | -mic M -ticker T] [-held] [-json] | fund -file F|-example [-json]", summary: "the quality-income core strategy and the broad-fund template: a scored checklist, bear/base/bull valuation from your documented assumptions and an entry ceiling; proposals only, no probability, nothing is ordered", bootstrap: true, run: cmdStrategy},
		{name: "allocate", usage: "allocate plan BOOK -candidates FILE -fees FILE [-as-of DAY] [-contribution A] [-rates USD=0.92] [-compare] [-json] | example", summary: "turn strategy proposals into a feasible set of quantities under your policy limits and a sourced fee schedule, or say exactly why none is feasible or why to wait; no sale and no order", bootstrap: true, run: cmdAllocate},
		{name: "plan", usage: "plan generate BOOK -candidates FILE -fees FILE [-as-of DAY] [-valid-days N] | list [-all] | show PLAN[.rN] | diff PLAN | check PLAN | propose|accept|reject PLAN -by NAME [-note T] | expire", summary: "store recommendations as immutable revisions with their exact inputs, a lifecycle and a revision diff; a plan is a record, not an order", bootstrap: true, run: cmdPlan},
		{name: "review", usage: "review run BOOK [-prices FILE] [-as-of DAY] [-save] | config example|set|review|show | thesis set|show | instruct -security S ... | done -security S ...", summary: "hold, reduce or sell what you own by ordered rules, bounded by free shares; a late review is overdue, never a fresh HOLD; nothing is placed", bootstrap: true, run: cmdReview},
		{name: "order", usage: "order draft BOOK ... | confirm ORDER | submit ORDER | cancel ORDER | report ORDER -kind ... | resolve ORDER | list | show ORDER | control show|enable|disable | recover", summary: "the local record of orders: exact terms, the cash or shares held for them, a confirmation bound to those terms, and every fact reported about them; nothing reaches a broker", bootstrap: true, run: cmdOrder},
		{name: "paper", usage: "paper run BOOK -bars FILE [-seed N] [-through DAY] | fills BOOK | reset BOOK-ID -name NEW | example", summary: "the local paper engine: finished daily bars become reports about acknowledged orders, conservatively and reproducibly; no broker, no same-bar or touch fills", bootstrap: true, run: cmdPaper},
		{name: "tier", usage: "tier status | ready [TIER] | evidence set|show | config example|set|review|show | unlock TIER -by NAME -accept-risk | signal set|clear|show KIND | check -equity FILE", summary: "what the policy tier lets the program do, the sourced evidence a higher tier needs, your recorded acceptance, and the health signals that suspend new risk without touching what you hold", bootstrap: true, run: cmdTier},
		{name: "broker", usage: "broker config set|show | mode read_only|trading | bind BOOK -account ID | status | sync BOOK | differences [BOOK] | preview ORDER | limits show|set|review | contracts SYMBOL | fixture example", summary: "the connection to a broker, read-only by default: session and account status, balances, positions, orders and executions compared with the ledger, mismatches recorded as differences and never overwritten; offline unless configured", bootstrap: true, run: cmdBroker},
		{name: "income", usage: "income expected BOOK [-as-of DAY] [-json] | accrue BOOK [-as-of DAY] [-payout KEY] | receive BOOK -payout KEY -date D [-withheld A] [-gross A] [-security S -qty Q] [-import-id ID] | check BOOK [-as-of DAY] [-json]", summary: "what a book is owed by declared payouts: entitlement from the fills before the ex-date, accrual, payment with withholding, and a check that the income booked agrees; gross, never net of an assumed tax", bootstrap: true, run: cmdIncome},
		{name: "sources", usage: "sources list | eval [-days 30] | capabilities [-data TYPE [-market M] [-kind K]] [-allow-paid IDS] [-json]", summary: "list sources with their health, judge each by what it does across stories, or show what each could supply, where and at what cost", bootstrap: true, run: cmdSources},
		{name: "doctor", usage: "doctor", summary: "check the installation", bootstrap: true, tolerant: true, run: cmdDoctor},
		{name: "version", usage: "version", summary: "print the version", run: cmdVersion},
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands() {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// usage prints the command line help to w.
func (a *app) usage(w io.Writer) {
	var b strings.Builder
	b.WriteString("usage: stoxicity [-config-dir DIR] [-env-file FILE]... [-tenant ID] [-v] COMMAND [args]\n\ncommands:\n")
	for _, c := range commands() {
		fmt.Fprintf(&b, "  %-52s %s\n", c.usage, c.summary)
	}
	io.WriteString(w, b.String())
}

func cmdVersion(_ context.Context, a *app, args []string) error {
	if len(args) > 0 {
		return usageErrorf("takes no arguments")
	}
	fmt.Fprintln(a.stdout, "stoxicity", version)
	return nil
}
