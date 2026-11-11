// Package dbtest gives each test a fresh, migrated database: SQLite in memory by default, or a private
// schema on the PostgreSQL named by VB_TEST_PG_DSN (CI runs the whole suite both ways).
package dbtest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
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
	g, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
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
