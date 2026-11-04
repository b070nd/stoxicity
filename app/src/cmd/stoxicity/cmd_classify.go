package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/b070nd/stoxicity/app/src/lens"
	"github.com/b070nd/stoxicity/app/src/model"
	"github.com/b070nd/stoxicity/app/src/sanitize"
)

// maxClassifyInput bounds the text read from standard input.
const maxClassifyInput = 64 << 10

// cmdClassify shows how one headline would be scored, without storing anything:
// the result of the rule lens, the topics and sector of the classifier and,
// when laya is switched on and answers, its raw opinion and the score that
// merging it under the configured policy would give. It is the tool for judging
// laya on real headlines before switching any of its answers on.
func cmdClassify(ctx context.Context, a *app, args []string) error {
	fs := newFlags("classify")
	noLaya := fs.Bool("no-laya", false, "show the rule results only, without asking laya")
	words, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return usageErrorf("no text given")
	}
	art, err := a.classifyInput(words)
	if err != nil {
		return err
	}

	w := a.stdout
	fmt.Fprintf(w, "text          %s\n", oneLine(art.Title))
	if art.Summary != "" {
		fmt.Fprintf(w, "summary       %s\n", oneLine(art.Summary))
	}
	for _, e := range a.entities().Find(art.Title + "\n" + art.Summary) {
		art.Tickers = append(art.Tickers, e.Ticker)
	}
	slices.Sort(art.Tickers)
	art.Tickers = slices.Compact(art.Tickers)
	if len(art.Tickers) > 0 {
		fmt.Fprintf(w, "tickers       %s\n", strings.Join(art.Tickers, ", "))
	}

	l := a.lens()
	score := l.Score(art)
	fmt.Fprintf(w, "\nrule lens     %s\n", l.Version())
	printScore(w, score, a.cfg.Lens.Threshold)

	version := l.Version()
	topics := slices.Clone(art.Topics)
	if cl := a.classifier(); cl != nil {
		found, sector := cl.Classify(art)
		topics = append(topics, found...)
		slices.Sort(topics)
		topics = slices.Compact(topics)
		if score.Sector == "" {
			score.Sector = sector
		}
		fmt.Fprintf(w, "\nclassifier    %s\n", cl.Version())
		fmt.Fprintf(w, "  topics      %s\n", orDash(strings.Join(topics, ", ")))
		fmt.Fprintf(w, "  sector      %s\n", orDash(sector))
		if v := cl.Version(); v != "" {
			version += "+" + v
		}
	}

	if *noLaya {
		fmt.Fprintln(w, "\nlaya          skipped (-no-laya)")
		return nil
	}
	if !a.cfg.Laya.Enabled {
		fmt.Fprintln(w, "\nlaya          switched off (laya.enabled is false in config.yaml)")
		return nil
	}
	enr := a.enricher()
	if enr == nil {
		fmt.Fprintln(w, "\nlaya          not available: its settings are not usable, see the warning on stderr")
		return nil
	}
	ops, err := enr.Opinions(ctx, []model.Article{art})
	if err == nil && len(ops) != 1 {
		err = fmt.Errorf("%d opinions for 1 article", len(ops))
	}
	if err != nil {
		fmt.Fprintf(w, "\nlaya          no answer (%v)\n", err)
		fmt.Fprintln(w, "              the rule results above are all there is. Start the sidecar with LAYA_HOST=127.0.0.1 laya-serve")
		return nil
	}

	fmt.Fprintf(w, "\nlaya          %s\n", enr.Version())
	printOpinion(w, ops[0])

	use := layaUse(a.cfg.Laya)
	merged, topic := lens.Apply(score, ops[0], use, a.cfg.Laya.MinConfidence)
	if topic != "" && !slices.Contains(topics, topic) {
		topics = append(topics, topic)
		slices.Sort(topics)
	}
	fmt.Fprintf(w, "\nmerged        what ingest would store (applied: %s; minimum confidence %.2f)\n",
		orDash(strings.Join(a.cfg.Laya.Apply, ", ")), a.cfg.Laya.MinConfidence)
	printScore(w, merged, a.cfg.Lens.Threshold)
	fmt.Fprintf(w, "  topics      %s\n", orDash(strings.Join(topics, ", ")))
	fmt.Fprintf(w, "  version     %s+%s\n", version, enr.Version())
	return nil
}

// classifyInput builds the article to score from the words of the command
// line, or from standard input when the only word is "-". Standard input gives
// the first line as the title and the rest as the summary.
func (a *app) classifyInput(words []string) (model.Article, error) {
	art := model.Article{ID: "classify-input"}
	if len(words) == 1 && words[0] == "-" {
		in := a.opts.stdin
		if in == nil {
			in = os.Stdin
		}
		raw, err := io.ReadAll(io.LimitReader(in, maxClassifyInput))
		if err != nil {
			return art, fmt.Errorf("read standard input: %w", err)
		}
		title, summary, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
		art.Title, art.Summary = sanitize.Text(title), sanitize.Text(summary)
	} else {
		art.Title = sanitize.Text(strings.Join(words, " "))
	}
	if art.Title == "" {
		return art, usageErrorf("the text is empty")
	}
	return art, nil
}

// printScore writes the fields of a score and its reasons.
func printScore(w io.Writer, s model.Score, threshold float64) {
	verdict := "below"
	if s.Relevant(threshold) {
		verdict = "relevant, at or above"
	}
	fmt.Fprintf(w, "  kind        %s\n", orDash(string(s.Kind)))
	fmt.Fprintf(w, "  materiality %d\n", s.Materiality)
	fmt.Fprintf(w, "  relevance   %.3f (%s threshold %.2f)\n", s.Relevance, verdict, threshold)
	fmt.Fprintf(w, "  tone        %.2f\n", s.Tone)
	fmt.Fprintf(w, "  sector      %s\n", orDash(s.Sector))
	fmt.Fprintln(w, "  reasons")
	for _, r := range s.Reasons {
		fmt.Fprintf(w, "    - %s\n", oneLine(r))
	}
}

// printOpinion writes every answer of an opinion with its confidence.
func printOpinion(w io.Writer, op lens.Opinion) {
	row := func(name, value string, conf float64) {
		if conf <= 0 {
			fmt.Fprintf(w, "  %-12s %-24s no answer\n", name, "-")
			return
		}
		fmt.Fprintf(w, "  %-12s %-24s confidence %.2f\n", name, value, conf)
	}
	row("relevant", fmt.Sprintf("%.2f", op.Relevance), op.RelevanceConfidence)
	row("group", string(op.Group), op.GroupConfidence)
	row("kind", string(op.Kind), op.KindConfidence)
	row("materiality", fmt.Sprintf("%.1f", op.Materiality), op.MaterialityConfidence)
	row("tone", fmt.Sprintf("%+.2f", op.Tone), op.ToneConfidence)
	row("sector", op.Sector, op.SectorConfidence)
	row("topic", op.Topic, op.TopicConfidence)
	if op.Truncated {
		fmt.Fprintln(w, "  the text was cut: the answers saw only its start")
	}
}

// orDash is s, or "-" when s is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
