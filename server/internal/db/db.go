// Package db opens the database. SQLite (pure Go, no CGO) is used for development and single-machine
// installs; production uses PostgreSQL (VB_DB_DRIVER=postgres, VB_DB_DSN=...) with the same code.
package db

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"voicebook/internal/model"
)

func Open(driver, dsn string, debug bool) (*gorm.DB, error) {
	quiet := logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{SlowThreshold: 800 * time.Millisecond, LogLevel: logger.Warn, IgnoreRecordNotFoundError: true, Colorful: false})
	cfg := &gorm.Config{
		Logger:                                   quiet,
		DisableForeignKeyConstraintWhenMigrating: true,
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
	}
	var (
		db  *gorm.DB
		err error
	)
	switch driver {
	case "postgres":
		db, err = gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			return nil, err
		}
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(10)
	case "sqlite", "":
		if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
			return nil, err
		}
		full := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(0)&_pragma=synchronous(NORMAL)", dsn)
		db, err = gorm.Open(sqlite.Open(full), cfg)
		if err != nil {
			return nil, err
		}
		sqlDB, _ := db.DB()
		// WAL lets readers run alongside the single writer; immediate transactions take the write
		// lock up front so lock upgrades never fail, and busy_timeout serializes competing writers.
		sqlDB.SetMaxOpenConns(4)
		sqlDB.SetMaxIdleConns(4)
	default:
		return nil, fmt.Errorf("unsupported db driver %q", driver)
	}
	return db, nil
}

func Migrate(db *gorm.DB) error {
	start := time.Now()
	if err := relaxStrings(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(model.All()...); err != nil {
		return err
	}
	slog.Info("database migrated", "tables", len(model.All()), "took", time.Since(start).Round(time.Millisecond))
	return nil
}
