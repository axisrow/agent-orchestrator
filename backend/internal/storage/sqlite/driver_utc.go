package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	// The zero-value modernc Driver is safe to instantiate: Open only reads
	// its UDF/collation/module registries, and the package init has already
	// run the C-library setup by the time this file's init executes.
	modernc "modernc.org/sqlite"
)

// The driver stores time.Time params in their text String() form. A time
// carrying a nameless zone — what JSON-decoding a hook client's offset
// foreign to the daemon host produces — prints the numeric offset twice
// ("2026-11-25 01:00:25.570831 +0700 +0700"), and nothing can parse that back:
// every later read of the row dies with "unsupported Scan, storing
// driver.Value type string into type *time.Time" (issue #383, beamcraft-433).
//
// sqliteUTC rewrites every bound instant to UTC before the driver sees it, so
// the stored form is always the named-zone "… +0000 UTC" form the driver
// round-trips. It covers NullTime params (unwrapped by database/sql through
// driver.Valuer) and bare time.Time params alike, on every table, for every
// current and future caller.
type sqliteUTC struct{ driver.Driver }

// DriverName registers the UTC-normalizing sqlite driver.
const DriverName = "sqlite-utc"

func init() {
	sql.Register(DriverName, sqliteUTC{&modernc.Driver{}})
}

func (d sqliteUTC) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return sqliteUTCConn{Conn: conn}, nil
}

// sqliteUTCConn forwards the plain driver.Conn interface only: it deliberately
// does not expose the underlying conn's legacy Execer/Queryer, so database/sql
// always routes args through prepared statements — and through
// sqliteUTCStmt.CheckNamedValue, where instants are normalized.
type sqliteUTCConn struct{ driver.Conn }

func (c sqliteUTCConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return sqliteUTCStmt{Stmt: stmt}, nil
}

func (c sqliteUTCConn) Close() error { return c.Conn.Close() }

func (c sqliteUTCConn) Begin() (driver.Tx, error) { return c.Conn.Begin() } //nolint:staticcheck // legacy method required by the driver.Conn interface; database/sql prefers BeginTx below

func (c sqliteUTCConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if bt, ok := c.Conn.(driver.ConnBeginTx); ok {
		return bt.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback only reached when the wrapped conn lacks ConnBeginTx
}

func (c sqliteUTCConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := pc.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return sqliteUTCStmt{Stmt: stmt}, nil
	}
	return c.Prepare(query)
}

func (c sqliteUTCConn) ResetSession(ctx context.Context) error {
	if sr, ok := c.Conn.(driver.SessionResetter); ok {
		return sr.ResetSession(ctx)
	}
	return driver.ErrSkip
}

// sqliteUTCStmt normalizes bound instants to UTC before execution.
type sqliteUTCStmt struct{ driver.Stmt }

func (s sqliteUTCStmt) Close() error { return s.Stmt.Close() }

func (s sqliteUTCStmt) NumInput() int { return s.Stmt.NumInput() }

func (s sqliteUTCStmt) Exec(args []driver.Value) (driver.Result, error) {
	utcValues(args)
	return s.Stmt.Exec(args) //nolint:staticcheck // legacy method required by the driver.Stmt interface; database/sql prefers StmtExecContext
}

func (s sqliteUTCStmt) Query(args []driver.Value) (driver.Rows, error) {
	utcValues(args)
	return s.Stmt.Query(args) //nolint:staticcheck // legacy method required by the driver.Stmt interface; database/sql prefers StmtQueryContext
}

// CheckNamedValue converts each arg through the default converter (which
// unwraps driver.Valuer params like sql.NullTime into time.Time) and rewrites
// instants to UTC. Accepting everything the default converter accepts matches
// the no-checker behavior this wrapper replaces.
func (s sqliteUTCStmt) CheckNamedValue(nv *driver.NamedValue) error {
	value, err := driver.DefaultParameterConverter.ConvertValue(nv.Value)
	if err != nil {
		return err
	}
	if t, ok := value.(time.Time); ok {
		value = t.UTC()
	}
	nv.Value = value
	return nil
}

func utcValues(args []driver.Value) {
	for i, value := range args {
		if t, ok := value.(time.Time); ok {
			args[i] = t.UTC()
		}
	}
}
