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
		{name: "label", usage: "label keep|drop ID... [-sampled] | list | sample", summary: "record keep/drop verdicts, list them, or draw a sample to label", bootstrap: true, run: cmdLabel},
		{name: "eval", usage: "eval [-days 90] [-threshold X]", summary: "measure the lens against your labels", bootstrap: true, run: cmdEval},
		{name: "rescore", usage: "rescore [-days N | -from DAY -to DAY]", summary: "score stored articles again with the current lens", bootstrap: true, run: cmdRescore},
		{name: "classify", usage: "classify [-no-laya] TEXT... | -", summary: "show how one headline is scored and classified; stores nothing", bootstrap: true, run: cmdClassify},
		{name: "company", usage: "company status | update [-force] | find TEXT...", summary: "the company master (SEC tickers and names): refresh or search it", bootstrap: true, run: cmdCompany},
		{name: "gmail", usage: "gmail status | auth | test", summary: "set up and test the email channel (Gmail API with OAuth)", bootstrap: true, run: cmdGmail},
		{name: "watch", usage: "watch list | check [id...] | test ID [-since 24h]", summary: "list, validate or try the tenant's watches", bootstrap: true, run: cmdWatch},
		{name: "tenant", usage: "tenant list", summary: "list tenants", bootstrap: true, run: cmdTenant},
		{name: "sources", usage: "sources list", summary: "list sources with their last health", bootstrap: true, run: cmdSources},
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
