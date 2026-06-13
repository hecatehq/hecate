package storage

import (
	"context"
	"database/sql"
	"strings"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
}

type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	Commit() error
	Rollback() error
}

type SQLClient interface {
	DB() DB
	QualifiedTable(name string) string
	TableName(name string) string
	Dialect() Dialect
	Backend() string
}

type rebindingDB struct {
	db      *sql.DB
	dialect Dialect
}

func (d *rebindingDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, Rebind(query, d.dialect), args...)
}

func (d *rebindingDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, Rebind(query, d.dialect), args...)
}

func (d *rebindingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, Rebind(query, d.dialect), args...)
}

func (d *rebindingDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error) {
	tx, err := d.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &rebindingTx{tx: tx, dialect: d.dialect}, nil
}

type rebindingTx struct {
	tx      *sql.Tx
	dialect Dialect
}

func (tx *rebindingTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, Rebind(query, tx.dialect), args...)
}

func (tx *rebindingTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, Rebind(query, tx.dialect), args...)
}

func (tx *rebindingTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, Rebind(query, tx.dialect), args...)
}

func (tx *rebindingTx) Commit() error {
	return tx.tx.Commit()
}

func (tx *rebindingTx) Rollback() error {
	return tx.tx.Rollback()
}

func Rebind(query string, dialect Dialect) string {
	if dialect != DialectPostgres || !strings.Contains(query, "?") {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 8)
	placeholder := 1
	inSingle := false
	inDouble := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch ch {
		case '\'':
			b.WriteByte(ch)
			if !inDouble {
				if inSingle && i+1 < len(query) && query[i+1] == '\'' {
					i++
					b.WriteByte(query[i])
					continue
				}
				inSingle = !inSingle
			}
		case '"':
			b.WriteByte(ch)
			if !inSingle {
				if inDouble && i+1 < len(query) && query[i+1] == '"' {
					i++
					b.WriteByte(query[i])
					continue
				}
				inDouble = !inDouble
			}
		case '?':
			if inSingle || inDouble {
				b.WriteByte(ch)
				continue
			}
			b.WriteByte('$')
			b.WriteString(intString(placeholder))
			placeholder++
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func intString(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	out := strings.Repeat("?, ", n)
	return strings.TrimSuffix(out, ", ")
}

func AutoIDColumn(client SQLClient) string {
	if client != nil && client.Dialect() == DialectPostgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

func TimestampColumn(client SQLClient) string {
	if client != nil && client.Dialect() == DialectPostgres {
		return "TIMESTAMPTZ"
	}
	return "TIMESTAMP"
}

func TimestampColumnDefaultZero(client SQLClient) string {
	if client != nil && client.Dialect() == DialectPostgres {
		return "TIMESTAMPTZ NOT NULL DEFAULT '0001-01-01T00:00:00Z'"
	}
	return "TIMESTAMP NOT NULL DEFAULT ''"
}

func BinaryColumn(client SQLClient) string {
	if client != nil && client.Dialect() == DialectPostgres {
		return "BYTEA"
	}
	return "BLOB"
}

func ColumnExists(ctx context.Context, client SQLClient, tableName, column string) (bool, error) {
	if client == nil || client.DB() == nil {
		return false, nil
	}
	tableName = sanitizeIdentifier(tableName, "table")
	column = sanitizeIdentifier(column, "column")
	if client.Dialect() == DialectPostgres {
		var found string
		err := client.DB().QueryRowContext(ctx, `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = ?
			  AND column_name = ?
		`, tableName, column).Scan(&found)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}

	rows, err := client.DB().QueryContext(ctx, `PRAGMA table_info("`+tableName+`")`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
