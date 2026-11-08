package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/alerts"
	"github.com/b070nd/stoxicity/app/src/channel"
	"github.com/b070nd/stoxicity/app/src/ops"
)

// cmdAlerts finds the things the user should hear about, keeps them once, and
// delivers them through the channels the tenant chose, recording honestly what
// reached whom. Looking at the plans, books, income, broker differences and
// orders reads stored records only; delivery sends no market data, no article
// text and never an order.
func cmdAlerts(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: scan | status | deliver | answer | config")
	}
	switch args[0] {
	case "scan":
		return alertsScan(ctx, a, args[1:])
	case "status":
		return alertsStatus(ctx, a, args[1:])
	case "deliver":
		return alertsDeliver(ctx, a, args[1:])
	case "answer":
		return alertsAnswer(ctx, a, args[1:])
	case "config":
		return alertsConfig(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (scan, status, deliver, answer, config)", args[0])
}

func alertOutbox(a *app) (alerts.Outbox, error) {
	rs, err := a.records()
	if err != nil {
		return alerts.Outbox{}, err
	}
	return alerts.Outbox{RS: rs, Tenant: a.tenantID, Now: a.now}, nil
}

func alertsScan(ctx context.Context, a *app, args []string) error {
	fs := newFlags("alerts scan")
	deliver := fs.Bool("deliver", false, "deliver what is waiting after the scan")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	cfg, _, err := ob.Config(ctx)
	if err != nil {
		return err
	}
	src, err := workflowSource(a)
	if err != nil {
		return err
	}
	sc, errs := src.AlertScan(ctx, cfg)
	res, err := ob.Raise(ctx, cfg, sc)
	if err != nil {
		return err
	}
	if *asJSON {
		type item struct {
			ID    string `json:"id"`
			Rule  string `json:"rule"`
			Key   string `json:"key"`
			Title string `json:"title"`
		}
		out := struct {
			New        []item   `json:"new"`
			Known      int      `json:"already_raised"`
			Reopened   []string `json:"reopened"`
			Superseded []string `json:"superseded"`
			Withdrawn  []string `json:"withdrawn"`
			Failed     []string `json:"failed_rules"`
		}{New: []item{}, Known: res.Known, Reopened: nz(res.Reopened), Superseded: nz(res.Superseded), Withdrawn: nz(res.Withdrawn), Failed: []string{}}
		for _, x := range res.New {
			out.New = append(out.New, item{x.ID(), string(x.Rule), x.Key(), x.Title})
		}
		for _, e := range errs {
			out.Failed = append(out.Failed, e.Error())
		}
		if err := printJSON(a, out); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "%d new alert(s), %d already raised, %d replaced by a newer revision, %d withdrawn because the situation ended, %d reopened.\n",
			len(res.New), res.Known, len(res.Superseded), len(res.Withdrawn), len(res.Reopened))
		for _, x := range res.New {
			fmt.Fprintf(a.stdout, "  %s  %-17s %s\n", x.ID(), x.Rule, x.Title)
		}
		for _, e := range errs {
			fmt.Fprintf(a.stderr, "stoxicity: not looked at: %v\n", e)
		}
	}
	if *deliver {
		if err := deliverAlerts(ctx, a, ob, cfg, alerts.DeliverOptions{}, *asJSON); err != nil {
			return err
		}
	}
	if len(errs) > 0 {
		return errors.New("some rules could not be looked at, so nothing was withdrawn for them")
	}
	return nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func alertsStatus(ctx context.Context, a *app, args []string) error {
	fs := newFlags("alerts status")
	all := fs.Bool("all", false, "include answered and ended alerts")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	rows, err := ob.Status(ctx, *all)
	if err != nil {
		return err
	}
	if *asJSON {
		type d struct {
			Channel  string `json:"channel"`
			State    string `json:"state"`
			Attempts int    `json:"attempts"`
			Next     string `json:"next,omitempty"`
			Reason   string `json:"reason,omitempty"`
		}
		type item struct {
			ID         string            `json:"id"`
			Key        string            `json:"key"`
			Rule       string            `json:"rule"`
			Title      string            `json:"title"`
			Raised     string            `json:"raised"`
			Corrects   string            `json:"corrects,omitempty"`
			Open       bool              `json:"open"`
			Ended      bool              `json:"ended"`
			Deliveries []d               `json:"deliveries"`
			Responses  []alerts.Response `json:"responses"`
		}
		out := []item{}
		for _, r := range rows {
			it := item{ID: r.Alert.ID(), Key: r.Alert.Key(), Rule: string(r.Alert.Rule), Title: r.Alert.Title, Raised: r.Alert.Raised.UTC().Format(time.RFC3339),
				Corrects: r.Alert.Corrects, Open: r.Open(), Ended: r.Ended, Deliveries: []d{}, Responses: r.Responses}
			if it.Responses == nil {
				it.Responses = []alerts.Response{}
			}
			for _, x := range r.Deliveries {
				n := ""
				if !x.Next.IsZero() {
					n = x.Next.UTC().Format(time.RFC3339)
				}
				it.Deliveries = append(it.Deliveries, d{string(x.Channel), string(x.State), x.Attempts, n, x.Reason})
			}
			out = append(out, it)
		}
		return printJSON(a, out)
	}
	if len(rows) == 0 {
		fmt.Fprintln(a.stdout, "no alerts waiting for you (alerts scan looks for new ones; -all lists the answered and ended ones)")
		return nil
	}
	t := cliTable{Header: []string{"alert", "rule", "raised", "state", "delivery", "title"}}
	for _, r := range rows {
		state, st := "open", cliYellow
		switch {
		case r.Answered():
			state, st = r.Responses[len(r.Responses)-1].Kind, cliDim
			for _, x := range r.Responses {
				if x.Kind == "accepted" || x.Kind == "rejected" {
					state = x.Kind
				}
			}
		case r.Ended:
			state, st = "ended", cliDim
		}
		var dl []string
		worst := cliPlain
		for _, x := range r.Deliveries {
			s := string(x.Channel) + " " + string(x.State)
			if x.State == alerts.Failed || x.State == alerts.Uncertain {
				worst = cliRed
			} else if x.State == alerts.Held && worst == cliPlain {
				worst = cliYellow
			}
			if x.Reason != "" && x.State.Waiting() {
				s += " (" + x.Reason + ")"
			}
			dl = append(dl, s)
		}
		if len(dl) == 0 {
			dl = []string{"shown here only"}
		}
		t.Rows = append(t.Rows, []cliCell{plain(r.Alert.ID()), plain(string(r.Alert.Rule)), plain(r.Alert.Raised.UTC().Format("2006-01-02 15:04")), styled(state, st), styled(strings.Join(dl, "; "), worst), plain(r.Alert.Title)})
	}
	t.write(a.stdout, useColor(a.stdout))
	fmt.Fprintln(a.stdout, "\nA delivery is \"sent\" only when the channel accepted it. alerts answer ID accepted|rejected|noted records your answer without changing the alert, the plan or its evidence.")
	return nil
}

func alertsDeliver(ctx context.Context, a *app, args []string) error {
	fs := newFlags("alerts deliver")
	retry := fs.Bool("retry", false, "also send what gave up, is waiting for its next try or is held by quiet hours")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	cfg, _, err := ob.Config(ctx)
	if err != nil {
		return err
	}
	return deliverAlerts(ctx, a, ob, cfg, alerts.DeliverOptions{Retry: *retry}, *asJSON)
}

func deliverAlerts(ctx context.Context, a *app, ob alerts.Outbox, cfg alerts.Config, opt alerts.DeliverOptions, asJSON bool) error {
	// One delivery at a time per tenant. A lease that is not renewed runs out,
	// so a delivery that died does not block the next for more than ten minutes.
	lease, err := a.leases().Acquire("alerts-deliver-"+a.tenantID, holder(), 10*time.Minute)
	if err != nil {
		var held *ops.ErrHeld
		if errors.As(err, &held) {
			return fmt.Errorf("another delivery is running (%s) until %s; nothing was sent from here", held.Holder, held.Until.UTC().Format(time.RFC3339))
		}
		return err
	}
	defer lease.Release()
	// only the channels some alert still waits for are built, so a channel nobody
	// uses is never asked for its settings
	rows, err := ob.Status(ctx, true)
	if err != nil {
		return err
	}
	need := map[channel.Kind]bool{}
	for _, r := range rows {
		for _, d := range r.Deliveries {
			if d.State.Waiting() {
				need[d.Channel] = true
			}
		}
	}
	var kinds []channel.Kind
	for _, k := range []channel.Kind{channel.Email, channel.Feed, channel.File, channel.Webhook} {
		if need[k] {
			kinds = append(kinds, k)
		}
	}
	senders := a.buildChannels(kinds)
	out, err := ob.Deliver(ctx, cfg, senders, opt)
	if asJSON {
		fix := func(l []alerts.Line) []alerts.Line {
			if l == nil {
				return []alerts.Line{}
			}
			return l
		}
		if perr := printJSON(a, map[string]any{"sent": fix(out.Sent), "failed": fix(out.Failed), "held": fix(out.Held), "waiting": fix(out.Waiting), "gave_up": fix(out.Gone), "recovered": fix(out.Recovered)}); perr != nil {
			return perr
		}
		return err
	}
	show := func(title string, ls []alerts.Line) {
		if len(ls) == 0 {
			return
		}
		fmt.Fprintf(a.stdout, "%s: %d\n", title, len(ls))
		for _, l := range ls {
			note := ""
			if l.Note != "" {
				note = " - " + l.Note
			}
			fmt.Fprintf(a.stdout, "  %s  %-7s %s%s\n", l.ID, l.Channel, l.Title, note)
		}
	}
	show("found with a send of unknown result (now uncertain, sent again below with a note)", out.Recovered)
	show("sent", out.Sent)
	show("failed (kept, will be tried again)", out.Failed)
	show("held by quiet hours", out.Held)
	show("waiting", out.Waiting)
	show("gave up", out.Gone)
	if len(out.Recovered)+len(out.Sent)+len(out.Failed)+len(out.Held)+len(out.Waiting)+len(out.Gone) == 0 {
		fmt.Fprintln(a.stdout, "nothing to deliver")
	}
	if err != nil {
		return err
	}
	if len(out.Failed) > 0 {
		return errReported
	}
	return nil
}

func alertsAnswer(ctx context.Context, a *app, args []string) error {
	fs := newFlags("alerts answer")
	note := fs.String("note", "", "your reason or comment (required to reject or to note)")
	by := fs.String("by", "", "who is answering")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return usageErrorf("usage: alerts answer ALERT accepted|rejected|noted -by NAME [-note TEXT]")
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	al, err := ob.Respond(ctx, rest[0], rest[1], *note, *by)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "%s %s by %s. The alert, the plan and the evidence behind them are unchanged; this answer sits beside them.\n", al.ID(), rest[1], strings.TrimSpace(*by))
	if al.Rule == alerts.PlanRevision {
		fmt.Fprintln(a.stdout, "Answering an alert does not move the plan. To decide the plan and write down why, use journal add.")
	}
	return nil
}

func alertsConfig(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: show | set")
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	cfg, stored, err := ob.Config(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		fs := newFlags("alerts config show")
		asJSON := fs.Bool("json", false, "print JSON")
		if _, err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if *asJSON {
			return printJSON(a, cfg)
		}
		src := "starter configuration (nothing stored)"
		if stored {
			src = "stored"
		}
		fmt.Fprintf(a.stdout, "alert configuration, %s\n", src)
		if cfg.QuietStart == "" {
			fmt.Fprintln(a.stdout, "  quiet hours:   none")
		} else {
			fmt.Fprintf(a.stdout, "  quiet hours:   %s to %s, %s\n", cfg.QuietStart, cfg.QuietEnd, cfg.Zone)
		}
		var crit []string
		for _, r := range cfg.Critical {
			crit = append(crit, string(r))
		}
		fmt.Fprintf(a.stdout, "  critical (go through quiet hours): %s\n", orNone(strings.Join(crit, ", ")))
		for _, r := range alerts.Rules() {
			var ks []string
			for _, k := range cfg.ChannelsFor(r) {
				ks = append(ks, string(k))
			}
			fmt.Fprintf(a.stdout, "  %-18s -> %s\n", r, orNone(strings.Join(ks, ", ")))
		}
		fmt.Fprintf(a.stdout, "  retries: %d attempts, first wait %d min, doubling\n  ex-date notice: %d days; review interval: %d days\n", cfg.MaxAttempts, cfg.RetryMinutes, cfg.DividendDays, cfg.ReviewDays)
		fmt.Fprintln(a.stdout, "  The terminal interface and this command show every alert; channels carry copies. email and webhook leave this machine and are used only for the rules that list them.")
		return nil
	case "set":
		fs := newFlags("alerts config set")
		qs := fs.String("quiet-start", "", "start of the quiet hours, HH:MM")
		qe := fs.String("quiet-end", "", "end of the quiet hours, HH:MM")
		noQuiet := fs.Bool("no-quiet", false, "remove the quiet hours")
		zone := fs.String("zone", "", "time zone of the quiet hours, e.g. Europe/Budapest")
		crit := fs.String("critical", "", "comma-separated rules that go through quiet hours (empty list: -critical none)")
		chans := fs.String("channels", "", `channels per rule, e.g. "*=file,feed;order_unknown=file,email"`)
		attempts := fs.Int("attempts", 0, "send attempts before giving up")
		wait := fs.Int("retry-minutes", 0, "minutes before the first retry")
		dd := fs.Int("dividend-days", 0, "days of notice before an ex-date")
		rd := fs.Int("review-days", 0, "days a holding may go without a review")
		if rest, err := parseFlags(fs, args[1:]); err != nil {
			return err
		} else if len(rest) > 0 {
			return usageErrorf("unexpected argument %q", rest[0])
		}
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if len(set) == 0 {
			return usageErrorf("nothing to set")
		}
		if set["quiet-start"] {
			cfg.QuietStart = *qs
		}
		if set["quiet-end"] {
			cfg.QuietEnd = *qe
		}
		if *noQuiet {
			cfg.QuietStart, cfg.QuietEnd = "", ""
		}
		if set["zone"] {
			cfg.Zone = *zone
		}
		if set["critical"] {
			cfg.Critical = nil
			if *crit != "none" {
				for _, r := range strings.Split(*crit, ",") {
					if r = strings.TrimSpace(r); r != "" {
						cfg.Critical = append(cfg.Critical, alerts.Rule(r))
					}
				}
			}
		}
		shared := false
		if set["channels"] {
			m := map[string][]channel.Kind{}
			for k, v := range cfg.Channels {
				m[k] = v
			}
			for _, part := range strings.Split(*chans, ";") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				rule, list, ok := strings.Cut(part, "=")
				if !ok {
					return usageErrorf("-channels: %q must be RULE=kind,kind", part)
				}
				var ks []channel.Kind
				for _, k := range strings.Split(list, ",") {
					if k = strings.TrimSpace(k); k != "" {
						ks = append(ks, channel.Kind(k))
						if k == string(channel.Email) || k == string(channel.Webhook) {
							shared = true
						}
					}
				}
				m[strings.TrimSpace(rule)] = ks
			}
			cfg.Channels = m
		}
		if set["attempts"] {
			cfg.MaxAttempts = *attempts
		}
		if set["retry-minutes"] {
			cfg.RetryMinutes = *wait
		}
		if set["dividend-days"] {
			cfg.DividendDays = *dd
		}
		if set["review-days"] {
			cfg.ReviewDays = *rd
		}
		if err := ob.SetConfig(ctx, cfg); err != nil {
			return usageErrorf("%v", err)
		}
		fmt.Fprintln(a.stdout, "alert configuration stored")
		if shared {
			fmt.Fprintln(a.stdout, "note: email and webhook carry the title and the facts of the alert (never article text) to a service outside this machine.")
		}
		return nil
	}
	return usageErrorf("unknown subcommand %q (show, set)", args[0])
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// cmdDaily prints the daily review: what asks for attention today, ending with
// the journal. It reads stored records and changes nothing.
func cmdDaily(ctx context.Context, a *app, args []string) error {
	fs := newFlags("daily")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	ob, err := alertOutbox(a)
	if err != nil {
		return err
	}
	cfg, _, err := ob.Config(ctx)
	if err != nil {
		return err
	}
	src, err := workflowSource(a)
	if err != nil {
		return err
	}
	d, err := src.Daily(ctx, cfg)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(a, d)
	}
	fmt.Fprint(a.stdout, d.Text())
	return nil
}
