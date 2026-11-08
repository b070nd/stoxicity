package workspace

import (
	"context"
	"os"

	"github.com/b070nd/stoxicity/app/src/report"
	"github.com/b070nd/stoxicity/app/src/tui"
	"github.com/b070nd/stoxicity/app/src/workflow"
)

var _ tui.Workflow = (*Workspace)(nil)

func (w *Workspace) source() workflow.Source {
	return workflow.Source{DB: w.db, Tenant: w.tenant.ID, Now: w.now, DataDir: w.dataDir}
}

// PolicyStatus is the policy in force and what its activation changed.
func (w *Workspace) PolicyStatus(ctx context.Context) (report.PolicyStatus, error) {
	return w.source().PolicyStatus(ctx)
}

// PlanRows are the latest revision of every plan.
func (w *Workspace) PlanRows(ctx context.Context) ([]report.PlanRow, error) {
	return w.source().PlanRows(ctx)
}

// PlanDoc is one revision of a plan and the numbers of all its revisions.
func (w *Workspace) PlanDoc(ctx context.Context, ref string) (report.PlanDoc, []int, error) {
	src := w.source()
	d, err := src.PlanDoc(ctx, ref)
	if err != nil {
		return d, nil, err
	}
	revs, err := src.Revisions(ctx, d.Revision.PlanID)
	return d, revs, err
}

// Books lists the books of the tenant.
func (w *Workspace) Books(ctx context.Context) ([]report.BookRef, error) {
	return w.source().Books(ctx)
}

// BookReport is the report of one book.
func (w *Workspace) BookReport(ctx context.Context, book string) (report.Report, error) {
	return w.source().BookReport(ctx, book, "")
}

// Income is the income of every book today.
func (w *Workspace) Income(ctx context.Context) (report.IncomeView, error) {
	return w.source().Income(ctx, "")
}

// OrderRows are the orders of the tenant.
func (w *Workspace) OrderRows(ctx context.Context) ([]report.OrderRow, error) {
	return w.source().OrderRows(ctx)
}

// ConfirmOrder records the go-ahead for a draft. It sends nothing.
func (w *Workspace) ConfirmOrder(ctx context.Context, id, typed string) error {
	by := os.Getenv("USER")
	if by == "" {
		by = "tui"
	}
	return w.source().ConfirmOrder(ctx, id, typed, "tui:"+by)
}
