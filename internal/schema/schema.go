package schema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"finance-platform/go/internal/store"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

//go:embed seed/demo.sql
var demoSeed string

// Migrate applies the goose baseline. A database that already has the Laravel
// tables is stamped at version 1 and is not replayed.
func Migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("mysql"); err != nil {
		return err
	}
	present, err := hasTable(ctx, db, "users")
	if err != nil {
		return err
	}
	if present {
		version, err := goose.EnsureDBVersionContext(ctx, db)
		if err != nil {
			return err
		}
		if version < 1 {
			if _, err := db.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, true)`); err != nil {
				return err
			}
		}
	}
	return goose.UpContext(ctx, db, "migrations")
}

// Seed loads the stand rows. A database that already has users is left as it is.
// Role rows are copied into user_roles afterwards.
func Seed(ctx context.Context, db *sql.DB) error {
	present, err := hasTable(ctx, db, "users")
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("schema is missing: run migrate first")
	}
	var users int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return err
	}
	if users == 0 {
		for _, statement := range splitSQL(demoSeed) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("seed: %w", err)
			}
		}
	}
	return store.EnsureUserRoles(ctx, db)
}

func hasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = ?`, name).Scan(&n)
	return n > 0, err
}

func splitSQL(sqlText string) []string {
	var out []string
	var buf strings.Builder
	inString := false
	escape := false
	var quote byte
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		if inString {
			buf.WriteByte(c)
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		if c == '\'' || c == '"' {
			inString = true
			quote = c
			buf.WriteByte(c)
			continue
		}
		if c == ';' {
			statement := strings.TrimSpace(buf.String())
			buf.Reset()
			if statement != "" && !strings.HasPrefix(statement, "--") {
				out = append(out, statement)
			}
			continue
		}
		buf.WriteByte(c)
	}
	if tail := strings.TrimSpace(buf.String()); tail != "" && !strings.HasPrefix(tail, "--") {
		out = append(out, tail)
	}
	return out
}
