package main

import (
	"context"
	"fmt"
	"github.com/b070nd/stoxicity/app/src/pages"
	"strings"
)

// cmdTopic prints the page of one topic.
func cmdTopic(ctx context.Context, a *app, args []string) error {
	fs := newFlags("topic")
	days := fs.Int("days", defaultPageDays, "days the page covers, ending today")
	limit := fs.Int("limit", defaultPageLimit, "most rows in the article and domain lists")
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return usageErrorf("want exactly one topic name")
	}
	topic := strings.TrimSpace(rest[0])
	if topic == "" {
		return usageErrorf("the topic name is empty")
	}
	if err := checkWindow(*days, *limit); err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}
	p := pages.NewParams(a.now(), *days, *limit, a.cfg.Lens.Threshold, a.location())
	arts, err := rd.Range(ctx, p.First, p.End())
	if err != nil {
		return err
	}

	configured := make([]string, len(a.cfg.Topics))
	for i, t := range a.cfg.Topics {
		configured[i] = t.Name
	}
	known := pages.KnownTopics(pages.InWindow(arts, p), configured)
	if !pages.ContainsFold(known, topic) {
		fmt.Fprintf(a.stdout, "Unknown topic %q: no article in %s carries it and the configuration does not define it.\n", topic, p.Describe())
		if near := pages.ClosestNames(topic, known, pages.ClosestTopics); len(near) > 0 {
			fmt.Fprintln(a.stdout, "Closest topics:")
			for _, n := range near {
				fmt.Fprintf(a.stdout, "  %s\n", n)
			}
		} else {
			fmt.Fprintln(a.stdout, "No topic name is similar. List them with: stoxicity topics")
		}
		return nil
	}
	renderTopicPage(a.stdout, pages.BuildTopicPage(arts, topic, p), a.tenantID)
	return nil
}

// cmdTopics lists all topics with their counts and trend lift.
func cmdTopics(ctx context.Context, a *app, args []string) error {
	fs := newFlags("topics")
	days := fs.Int("days", defaultListDays, "days the list covers, ending today")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usageErrorf("unexpected argument %q", rest[0])
	}
	if err := checkWindow(*days, 1); err != nil {
		return err
	}
	if _, err := a.requireTenant(); err != nil {
		return err
	}
	rd, err := a.reader()
	if err != nil {
		return err
	}
	p := pages.NewParams(a.now(), *days, 0, a.cfg.Lens.Threshold, a.location())
	prev := p.Previous()
	arts, err := rd.Range(ctx, prev.First, p.End())
	if err != nil {
		return err
	}
	renderTopicsPage(a.stdout, pages.BuildTopicsPage(arts, p), a.tenantID)
	return nil
}
