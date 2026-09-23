package postgres

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsTable is kept separate from the default one because the
// accrual system may share the same database.
const migrationsTable = "gophermart_schema_migrations"

// Migrate applies all pending database migrations.
func Migrate(pool *pgxpool.Pool) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{MigrationsTable: migrationsTable})
	if err != nil {
		return fmt.Errorf("create migrate driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	// Close releases the connection held by the driver; otherwise
	// pool.Close would block forever waiting for it.
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
