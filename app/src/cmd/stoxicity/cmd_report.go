package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/b070nd/stoxicity/app/src/report"
	"github.com/b070nd/stoxicity/app/src/workflow"
)

// cmdReport exports what the program holds about one book, from stored records
// only: nothing is fetched, nothing is sent and no other book is opened.
func cmdReport(ctx context.Context, a *app, args []string) error {
	if len(args) == 0 {
		return usageErrorf("expected: export BOOK [-as-of DAY] [-format md|json] [-out FILE] [-force]")
	}
	switch args[0] {
	case "export":
		return reportExport(ctx, a, args[1:])
	}
	return usageErrorf("unknown subcommand %q (export)", args[0])
}

type exportFlags struct {
	format string
	out    string
	force  bool
}

func (e exportFlags) check() error {
	if e.format != "md" && e.format != "json" {
		return usageErrorf("-format %q must be md or json", e.format)
	}
	return nil
}

// emit writes the rendered export to stdout or, with -out, to a file that is
// not replaced unless -force is given.
func (e exportFlags) emit(a *app, md func() string, js func() ([]byte, error)) error {
	var body []byte
	if e.format == "json" {
		raw, err := js()
		if err != nil {
			return err
		}
		body = append(raw, '\n')
	} else {
		body = []byte(md())
	}
	if e.out == "" {
		_, err := a.stdout.Write(body)
		return err
	}
	mode := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if e.force {
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(e.out, mode, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists: use -force to replace it", e.out)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "wrote %s (%d bytes)\n", e.out, len(body))
	return nil
}

func reportExport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("report export")
	asOf := fs.String("as-of", "", "the day to judge payouts and plans from, YYYY-MM-DD; default today")
	format := fs.String("format", "md", "md for a person, json for a program")
	out := fs.String("out", "", "write to this file instead of the screen; an existing file is kept")
	force := fs.Bool("force", false, "replace the file named by -out")
	book, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if book == "" {
		return usageErrorf("usage: report export BOOK [-as-of DAY] [-format md|json] [-out FILE] [-force]")
	}
	ef := exportFlags{*format, *out, *force}
	if err := ef.check(); err != nil {
		return err
	}
	day, err := incomeDay(a, *asOf)
	if err != nil {
		return err
	}
	r, err := buildReport(ctx, a, book, day)
	if err != nil {
		return err
	}
	return ef.emit(a, r.Markdown, r.JSON)
}

// workflowSource is the tenant's stores read through the shared workflow layer.
func workflowSource(a *app) (workflow.Source, error) {
	if _, err := a.requireTenant(); err != nil {
		return workflow.Source{}, err
	}
	db, err := a.store()
	if err != nil {
		return workflow.Source{}, err
	}
	return workflow.Source{DB: db, Tenant: a.tenantID, Now: a.now}, nil
}

func buildReport(ctx context.Context, a *app, book, day string) (report.Report, error) {
	src, err := workflowSource(a)
	if err != nil {
		return report.Report{}, err
	}
	return src.BookReport(ctx, book, day)
}

func planExport(ctx context.Context, a *app, args []string) error {
	fs := newFlags("plan export")
	format := fs.String("format", "md", "md for a person, json for a program")
	out := fs.String("out", "", "write to this file instead of the screen; an existing file is kept")
	force := fs.Bool("force", false, "replace the file named by -out")
	ref, err := parseBookFlags(fs, args)
	if err != nil {
		return err
	}
	if ref == "" {
		return usageErrorf("usage: plan export PLAN[.rN] [-format md|json] [-out FILE] [-force]")
	}
	ef := exportFlags{*format, *out, *force}
	if err := ef.check(); err != nil {
		return err
	}
	src, err := workflowSource(a)
	if err != nil {
		return err
	}
	d, err := src.PlanDoc(ctx, ref)
	if err != nil {
		return err
	}
	return ef.emit(a, d.Markdown, d.JSON)
}
