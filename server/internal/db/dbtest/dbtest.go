// Package dbtest gives each test a fresh, migrated database: a SQLite file opened exactly as in
// production (WAL, busy timeout, immediate transactions) by default, or a private schema on the
// PostgreSQL named by VB_TEST_PG_DSN (CI runs the whole suite both ways).
package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"voicebook/internal/db"
)

var unsafe = regexp.MustCompile(`[^a-z0-9_]+`)

// Postgres reports whether tests run against PostgreSQL.
func Postgres() bool { return os.Getenv("VB_TEST_PG_DSN") != "" }

func Open(t testing.TB) *gorm.DB {
	t.Helper()
	if dsn := os.Getenv("VB_TEST_PG_DSN"); dsn != "" {
		return openPostgres(t, dsn)
	}
	// a file rather than shared-cache memory: concurrent writers then wait for each other as they do
	// in production instead of failing with "database table is locked"
	g, err := db.Open("sqlite", filepath.Join(t.TempDir(), "test.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	g.Logger = logger.Default.LogMode(logger.Silent)
	if err := db.Migrate(g); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, err := g.DB(); err == nil {
			s.Close()
		}
	})
	return g
}

func openPostgres(t testing.TB, dsn string) *gorm.DB {
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := unsafe.ReplaceAllString(strings.ToLower(t.Name()), "_")
	if len(name) > 40 {
		name = name[:40]
	}
	schema := "t_" + name + "_" + hex.EncodeToString(b)
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	g, err := db.Open("postgres", dsn+" search_path="+schema, false)
	if err != nil {
		t.Fatal(err)
	}
	g.Logger = logger.Default.LogMode(logger.Silent)
	t.Cleanup(func() {
		if s, err := g.DB(); err == nil {
			s.Close()
		}
		admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		if s, err := admin.DB(); err == nil {
			s.Close()
		}
	})
	if err := db.Migrate(g); err != nil {
		t.Fatal(err)
	}
	return g
}
