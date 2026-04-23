package main

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/postgres/*.sql migrations/sqlite/*.sql
var migrationsFS embed.FS

func runMigrations(db *sql.DB, driver string) error {
	var (
		sourceFS   fs.FS
		subdir     string
		driverName string
	)

	switch driver {
	case "postgres":
		sourceFS = migrationsFS
		subdir = "migrations/postgres"
		driverName = "postgres"
	case "sqlite":
		sourceFS = migrationsFS
		subdir = "migrations/sqlite"
		driverName = "sqlite"
	default:
		return fmt.Errorf("unsupported migration driver: %s", driver)
	}

	if err := maybeForceVersion(db, driver); err != nil {
		return fmt.Errorf("force version check: %w", err)
	}

	source, err := iofs.New(sourceFS, subdir)
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}

	var dbDriver database.Driver
	switch driverName {
	case "postgres":
		dbDriver, err = postgres.WithInstance(db, &postgres.Config{})
	case "sqlite":
		dbDriver, err = sqlite3.WithInstance(db, &sqlite3.Config{})
	}
	if err != nil {
		return fmt.Errorf("migrate db driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, driverName, dbDriver)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	log.Println("migrations: up to date")
	return nil
}

func maybeForceVersion(db *sql.DB, driver string) error {
	hasSchemaTbl := tableExists(db, driver, "schema_migrations")
	if hasSchemaTbl {
		return nil
	}

	hasPairs := tableExists(db, driver, "pairs")
	if !hasPairs {
		return nil
	}

	log.Println("migrations: existing DB detected, forcing version to 2")

	switch driver {
	case "postgres":
		_, err := db.Exec(`
                        CREATE TABLE IF NOT EXISTS schema_migrations (version bigint not null primary key, dirty boolean not null);
                        INSERT INTO schema_migrations (version, dirty) VALUES (2, false);
                `)
		return err
	case "sqlite":
		_, err := db.Exec(`
			CREATE TABLE IF NOT EXISTS schema_migrations (version bigint not null primary key, dirty boolean not null);
			INSERT INTO schema_migrations (version, dirty) VALUES (2, false);
		`)
		return err
	}
	return nil
}

func tableExists(db *sql.DB, driver, table string) bool {
	var n int
	switch driver {
	case "postgres":
		err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name=$1", table).Scan(&n)
		return err == nil && n > 0
	case "sqlite":
		err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n)
		return err == nil && n > 0
	}
	return false
}
