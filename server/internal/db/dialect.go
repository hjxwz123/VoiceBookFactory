package db

import (
	"context"
	"fmt"
	"regexp"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"voicebook/internal/model"
)

// The app grew up on SQLite. These helpers keep its SQL meaning the same on PostgreSQL.

func IsPostgres(g *gorm.DB) bool { return g.Dialector.Name() == "postgres" }

// Like is "col LIKE ?" with SQLite's semantics everywhere: case-insensitive for Latin letters (ILIKE on
// PostgreSQL), and JSON columns (jsonb there) matched against their text.
func Like(g *gorm.DB, col string) string {
	if IsPostgres(g) {
		return col + "::text ILIKE ?"
	}
	return col + " LIKE ?"
}

// NotLike is the negation of Like.
func NotLike(g *gorm.DB, col string) string {
	if IsPostgres(g) {
		return col + "::text NOT ILIKE ?"
	}
	return col + " NOT LIKE ?"
}

var jsonKey = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// JSONText reads a top-level key of a JSON column as text.
func JSONText(g *gorm.DB, col, key string) string {
	if !jsonKey.MatchString(key) {
		panic("db.JSONText: bad key " + key)
	}
	if IsPostgres(g) {
		return fmt.Sprintf("(%s->>'%s')", col, key)
	}
	return fmt.Sprintf("json_extract(%s, '$.%s')", col, key)
}

// Day is the UTC calendar day of a time column as YYYY-MM-DD.
func Day(g *gorm.DB, col string) string {
	if IsPostgres(g) {
		return fmt.Sprintf("to_char(%s AT TIME ZONE 'UTC', 'YYYY-MM-DD')", col)
	}
	return fmt.Sprintf("substr(%s, 1, 10)", col)
}

// relaxStrings drops the length limits of string columns (other than primary keys) on PostgreSQL, where
// varchar(n) is enforced; SQLite never enforced them, so the app may store longer values. PostgreSQL's
// text has no cost over varchar. It edits gorm's cached schemas, so it must run on the *gorm.DB that
// migrates and serves.
func relaxStrings(g *gorm.DB) error {
	if !IsPostgres(g) {
		return nil
	}
	for _, m := range model.All() {
		stmt := &gorm.Statement{DB: g}
		if err := stmt.Parse(m); err != nil {
			return err
		}
		for _, f := range stmt.Schema.Fields {
			if f.DataType == schema.String && !f.PrimaryKey {
				f.Size = 0
			}
		}
	}
	return nil
}

// Exclusive runs fn while holding a PostgreSQL advisory lock, so instances starting together migrate and
// seed one at a time instead of racing (two CREATE TABLEs, duplicate catalog rows). SQLite has a single
// writer process already, so fn just runs.
func Exclusive(ctx context.Context, g *gorm.DB, key int64, fn func() error) error {
	if !IsPostgres(g) {
		return fn()
	}
	sqlDB, err := g.DB()
	if err != nil {
		return err
	}
	conn, err := sqlDB.Conn(ctx) // the lock belongs to this session
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
	return fn()
}

// Advisory lock keys (any fixed numbers, distinct per purpose).
const (
	LockMigrate int64 = 7_210_001
	LockSeed    int64 = 7_210_002
)
