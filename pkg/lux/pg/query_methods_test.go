package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type methodTestRows struct {
	pgx.Rows
	left    int
	closed  bool
	err     error
	scanErr error
}

func (r *methodTestRows) Close()     { r.closed = true }
func (r *methodTestRows) Err() error { return r.err }
func (r *methodTestRows) Next() bool {
	r.left--
	return r.left >= 0
}
func (r *methodTestRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	*dest[0].(*int64) = 42
	return nil
}

type methodTestConn struct {
	conn
	rows     *methodTestRows
	queryErr error
	ctx      context.Context
	sql      string
	args     []any
}

func (c *methodTestConn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.ctx, c.sql, c.args = ctx, sql, args
	return c.rows, c.queryErr
}

func (c *methodTestConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	c.ctx, c.sql, c.args = ctx, sql, args
	return c.rows
}

func TestDBQueryRowsMethods(t *testing.T) {
	wantErr := errors.New("query failure")
	cases := []struct {
		name     string
		left     int
		queryErr error
		scanErr  error
		rowErr   error
		wantLen  int
	}{
		{name: "empty"},
		{name: "rows", left: 2, wantLen: 2},
		{name: "query", queryErr: wantErr},
		{name: "scan", left: 2, scanErr: wantErr},
		{name: "iterate", left: 1, rowErr: wantErr, wantLen: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := &methodTestRows{left: tc.left, err: tc.rowErr, scanErr: tc.scanErr}
			c := &methodTestConn{rows: rows, queryErr: tc.queryErr}
			db := &DB{conn: c}
			got, err := db.QueryRows(t.Context(), scanMethodValue, "SELECT value WHERE id=$1", int64(7))
			var expected error
			if tc.queryErr != nil || tc.scanErr != nil || tc.rowErr != nil {
				expected = wantErr
			}
			if len(got) != tc.wantLen || !errors.Is(err, expected) {
				t.Fatalf("rows=%v error=%v; want length=%d error=%v", got, err, tc.wantLen, expected)
			}
			if len(got) > 0 && *got[0] != 42 {
				t.Fatalf("value=%d", *got[0])
			}
			assertMethodDispatch(t, c, rows.closed == (tc.queryErr == nil))
		})
	}
}

func TestDBQueryRowMethods(t *testing.T) {
	wantErr := errors.New("row failure")
	for _, failure := range []string{"none", "empty", "query", "scan", "iterate"} {
		t.Run(failure, func(t *testing.T) {
			rows := &methodTestRows{left: 1}
			c := &methodTestConn{rows: rows}
			switch failure {
			case "empty":
				rows.left = 0
			case "query":
				c.queryErr = wantErr
			case "scan":
				rows.scanErr = wantErr
			case "iterate":
				rows.left, rows.err = 0, wantErr
			}
			db := &DB{conn: c}
			value, err := db.QueryRow[int64](t.Context(), scanMethodValue, "SELECT value WHERE id=$1", int64(7))
			if failure == "none" {
				if err != nil || value == nil || *value != 42 {
					t.Fatalf("value=%v error=%v", value, err)
				}
			} else if failure == "empty" {
				if value != nil || err != nil {
					t.Fatalf("empty row=%v error=%v", value, err)
				}
			} else if !errors.Is(err, wantErr) {
				t.Fatalf("error=%v", err)
			}
			assertMethodDispatch(t, c, rows.closed == (c.queryErr == nil))
		})
	}
}

func TestDBQueryScalarMethods(t *testing.T) {
	for _, wantErr := range []error{nil, errors.New("scalar failure")} {
		c := &methodTestConn{rows: &methodTestRows{scanErr: wantErr}}
		db := &DB{conn: c}
		value, err := db.QueryScalar[int64](t.Context(), "SELECT value WHERE id=$1", int64(7))
		if !errors.Is(err, wantErr) || (wantErr == nil && value != 42) || (wantErr != nil && value != 0) {
			t.Fatalf("value=%d error=%v", value, err)
		}
		assertMethodDispatch(t, c, true)
	}
}

func assertMethodDispatch(t *testing.T, c *methodTestConn, closedOK bool) {
	t.Helper()
	if !closedOK || c.ctx != t.Context() || c.sql != "SELECT value WHERE id=$1" || len(c.args) != 1 || c.args[0] != int64(7) {
		t.Fatal("method did not preserve connection, context, arguments or row lifetime")
	}
}
