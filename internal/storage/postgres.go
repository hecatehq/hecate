package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresConfig struct {
	DatabaseURL  string
	TablePrefix  string
	MaxOpenConns int
	MaxIdleConns int
}

type PostgresClient struct {
	db          *sql.DB
	wrapped     DB
	tablePrefix string
}

func NewPostgresClient(ctx context.Context, cfg PostgresConfig) (*PostgresClient, error) {
	dsn := strings.TrimSpace(cfg.DatabaseURL)
	if dsn == "" {
		return nil, fmt.Errorf("postgres database url is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 10
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgresClient{
		db:          db,
		wrapped:     &rebindingDB{db: db, dialect: DialectPostgres},
		tablePrefix: sanitizeIdentifier(cfg.TablePrefix, "hecate"),
	}, nil
}

func (c *PostgresClient) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *PostgresClient) DB() DB {
	if c == nil {
		return nil
	}
	return c.wrapped
}

func (c *PostgresClient) Dialect() Dialect {
	return DialectPostgres
}

func (c *PostgresClient) Backend() string {
	return "postgres"
}

func (c *PostgresClient) QualifiedTable(name string) string {
	return fmt.Sprintf(`"%s"`, c.TableName(name))
}

func (c *PostgresClient) TableName(name string) string {
	base := sanitizeIdentifier(name, "table")
	if c.tablePrefix == "" {
		return base
	}
	return c.tablePrefix + "_" + base
}

func (c *PostgresClient) ClearData(ctx context.Context) (int, error) {
	if c == nil || c.db == nil {
		return 0, nil
	}
	prefix := c.tablePrefix + "_"
	rows, err := c.db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_type = 'BASE TABLE'
		  AND table_name LIKE $1
	`, prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("list postgres tables: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return 0, fmt.Errorf("scan postgres table: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("list postgres tables: %w", err)
	}
	sort.Strings(tables)
	if len(tables) == 0 {
		return 0, nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin postgres clear: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for _, table := range tables {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+quotePostgresIdentifier(table))
		if err != nil {
			return deleted, fmt.Errorf("clear postgres table %q: %w", table, err)
		}
		if n, err := result.RowsAffected(); err == nil {
			deleted += int(n)
		}
	}
	if err := tx.Commit(); err != nil {
		return deleted, fmt.Errorf("commit postgres clear: %w", err)
	}
	return deleted, nil
}

func quotePostgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
