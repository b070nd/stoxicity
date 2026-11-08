package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/b070nd/stoxicity/app/src/order"
	"github.com/b070nd/stoxicity/app/src/portfolio"
)

// Orders is the order log of one tenant. It implements order.Runner.
type Orders struct{ p Portfolios }

// Orders returns the order store of one tenant.
func (db *DB) Orders(tenantID string) Orders { return Orders{db.Portfolios(tenantID)} }

var _ order.Runner = Orders{}

// Run loads a book's ledger, the logs of every order of its account and the
// controls, calls fn, and writes what fn posted to the ledger together with the
// events it returned in one database transaction: an intent and its hold are
// both there or neither is. Other processes are held off for the length of the
// transaction. fn must not call out to anything slow.
func (o Orders) Run(ctx context.Context, bookRef string, fn func(order.Context) ([]order.Event, error)) error {
	var events []order.Event
	var logs map[string][]order.Event
	var book portfolio.Book
	err := o.p.update(ctx, []string{bookRef}, func(tx *sql.Tx, now time.Time, ls []*portfolio.Ledger) (*ImportBatch, error) {
		book = ls[0].Book
		var err error
		if logs, err = loadLogs(ctx, tx, book.Account); err != nil {
			return nil, err
		}
		controls, err := loadControls(ctx, tx)
		if err != nil {
			return nil, err
		}
		events, err = fn(order.Context{Now: now, Ledger: ls[0], Logs: logs, Controls: controls})
		return nil, err
	}, func(tx *sql.Tx, now time.Time) error {
		next := map[string]int{}
		for _, e := range events {
			if e.Order == "" {
				return errors.New("store: orders: an event without an order")
			}
			n, ok := next[e.Order]
			if !ok {
				n = len(logs[e.Order])
			}
			n++
			next[e.Order] = n
			e.N, e.At = n, now
			raw, err := json.Marshal(e)
			if err != nil {
				return fmt.Errorf("store: orders: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO order_event (order_id, book_id, account, n, kind, key, at_ms, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				e.Order, book.ID, book.Account, n, string(e.Kind), e.Key, ms(now), string(raw)); err != nil {
				return fmt.Errorf("store: orders: %s: %w", e.Order, err)
			}
		}
		return nil
	})
	return err
}

func loadLogs(ctx context.Context, q pfQuery, account string) (map[string][]order.Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT body FROM order_event WHERE account = ? ORDER BY order_id, n`, account)
	if err != nil {
		return nil, fmt.Errorf("store: orders: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

func scanLogs(rows *sql.Rows) (map[string][]order.Event, error) {
	out := map[string][]order.Event{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("store: orders: %w", err)
		}
		var e order.Event
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			return nil, fmt.Errorf("store: orders: an event does not decode: %w", err)
		}
		out[e.Order] = append(out[e.Order], e)
	}
	return out, rows.Err()
}

func loadControls(ctx context.Context, q pfQuery) (map[portfolio.Env]order.Control, error) {
	rows, err := q.QueryContext(ctx, `SELECT env, disabled, reason, by, at_ms FROM order_control ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("store: orders: %w", err)
	}
	defer rows.Close()
	out := map[portfolio.Env]order.Control{}
	for rows.Next() {
		var env string
		var dis int
		var at int64
		var c order.Control
		if err := rows.Scan(&env, &dis, &c.Reason, &c.By, &at); err != nil {
			return nil, fmt.Errorf("store: orders: %w", err)
		}
		c.Disabled, c.At = dis == 1, fromMS(at)
		out[portfolio.Env(env)] = c
	}
	return out, rows.Err()
}

// Logs returns the events of every order of an account (all accounts when it is
// empty), in order.
func (o Orders) Logs(ctx context.Context, account string) (map[string][]order.Event, error) {
	h, err := o.p.t.handle()
	if err != nil {
		return nil, err
	}
	q, args := `SELECT body FROM order_event ORDER BY order_id, n`, []any(nil)
	if account != "" {
		q, args = `SELECT body FROM order_event WHERE account = ? ORDER BY order_id, n`, []any{account}
	}
	rows, err := h.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: orders: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// Events returns the log of one order.
func (o Orders) Events(ctx context.Context, id string) ([]order.Event, error) {
	logs, err := o.Logs(ctx, "")
	if err != nil {
		return nil, err
	}
	if l, ok := logs[id]; ok {
		return l, nil
	}
	return nil, fmt.Errorf("store: orders: no order %s", id)
}

// Controls returns the switches in force, one per environment that has been set.
func (o Orders) Controls(ctx context.Context) (map[portfolio.Env]order.Control, error) {
	h, err := o.p.t.handle()
	if err != nil {
		return nil, err
	}
	return loadControls(ctx, h.r)
}

// SetControl turns new orders of an environment on or off. Every change is a
// new row; the history stays.
func (o Orders) SetControl(ctx context.Context, env portfolio.Env, disabled bool, reason, by string) error {
	h, err := o.p.t.handle()
	if err != nil {
		return err
	}
	if !env.Valid() {
		return fmt.Errorf("store: orders: unknown environment %q", env)
	}
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(by) == "" {
		return errors.New("store: orders: a control change says why and who")
	}
	d := 0
	if disabled {
		d = 1
	}
	if _, err := h.w.ExecContext(ctx, `INSERT INTO order_control (env, disabled, reason, by, at_ms) VALUES (?, ?, ?, ?, ?)`, string(env), d, reason, by, ms(o.p.t.db.now())); err != nil {
		return fmt.Errorf("store: orders: %w", err)
	}
	return nil
}
