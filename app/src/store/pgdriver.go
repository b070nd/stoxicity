package store

import (
	"context"
	"database/sql/driver"
	"strconv"
	"strings"
	"sync"
)

// The store's SQL is written with SQLite's placeholders: "?" (numbered in order
// of appearance) and "?N" (an explicit number, which may repeat). Postgres wants
// "$N". Rather than touch hundreds of statements, the Postgres connections are
// wrapped so every statement is rewritten once, here, on its way to the server.

// rebind turns the placeholders of q into Postgres's. It leaves string
// literals, quoted identifiers, comments and dollar-quoted bodies alone.
func rebind(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}
	if v, ok := rebound.Load(q); ok {
		return v.(string)
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '\'' || c == '"':
			j := skipQuoted(q, i, c)
			b.WriteString(q[i:j])
			i = j
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				j = len(q) - i
			}
			b.WriteString(q[i : i+j])
			i += j
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			j := strings.Index(q[i+2:], "*/")
			if j < 0 {
				j = len(q) - i - 2
			} else {
				j += 2
			}
			b.WriteString(q[i : i+2+j])
			i += 2 + j
		case c == '$' && (i == 0 || !isIdentByte(q[i-1])):
			if tag, ok := dollarTag(q, i); ok {
				end := strings.Index(q[i+len(tag):], tag)
				if end < 0 {
					end = len(q) - i - len(tag)
				}
				b.WriteString(q[i : i+len(tag)+end+len(tag)])
				i += len(tag) + end + len(tag)
				break
			}
			b.WriteByte(c)
			i++
		case c == '?':
			j := i + 1
			for j < len(q) && q[j] >= '0' && q[j] <= '9' {
				j++
			}
			if j > i+1 {
				b.WriteByte('$')
				b.WriteString(q[i+1 : j])
			} else {
				n++
				b.WriteByte('$')
				b.WriteString(strconv.Itoa(n))
			}
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	out := b.String()
	rebound.Store(q, out)
	return out
}

var rebound sync.Map // statement text -> rebound text

// skipQuoted returns the index after the quoted run that starts at i. A doubled
// quote character inside is part of the run.
func skipQuoted(q string, i int, quote byte) int {
	for j := i + 1; j < len(q); j++ {
		if q[j] == quote {
			if j+1 < len(q) && q[j+1] == quote {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(q)
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// dollarTag returns the opening tag ("$$" or "$name$") of a dollar-quoted body
// that starts at i.
func dollarTag(q string, i int) (string, bool) {
	j := i + 1
	for j < len(q) && (q[j] == '_' || q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z') {
		j++
	}
	if j < len(q) && q[j] == '$' {
		return q[i : j+1], true
	}
	return "", false
}

// rebindConnector wraps a Postgres connector so that its connections rewrite
// placeholders.
type rebindConnector struct{ inner driver.Connector }

func (c rebindConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &rebindConn{conn}, nil
}

func (c rebindConnector) Driver() driver.Driver { return c.inner.Driver() }

// rebindConn forwards everything to the pgx connection, rewriting the text of
// statements. The optional interfaces database/sql looks for are all
// implemented by the pgx connection, so they are all forwarded.
type rebindConn struct{ driver.Conn }

func (c *rebindConn) Prepare(q string) (driver.Stmt, error) { return c.Conn.Prepare(rebind(q)) }

func (c *rebindConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, rebind(q))
}

func (c *rebindConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, rebind(q), args)
}

func (c *rebindConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, rebind(q), args)
}

func (c *rebindConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *rebindConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *rebindConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *rebindConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue sends a Go bool as the 0 or 1 the schema stores: every flag
// in it is an integer, as in SQLite.
func (c *rebindConn) CheckNamedValue(nv *driver.NamedValue) error {
	if b, ok := nv.Value.(bool); ok {
		if b {
			nv.Value = int64(1)
		} else {
			nv.Value = int64(0)
		}
		return nil
	}
	if ck, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}
