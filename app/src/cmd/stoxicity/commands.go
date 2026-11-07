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
		{name: "tenant", usage: "tenant list", summary: "list tenants", bootstrap: true, run: cmdTenant},
		{name: "sources", usage: "sources list | eval [-days 30]", summary: "list sources with their health, or judge each by what it does across stories", bootstrap: true, run: cmdSources},
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
