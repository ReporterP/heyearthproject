package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

var DB *sql.DB

func InitDB() error {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		// Default local connection for dev
		connStr = "postgres://postgres:postgres@localhost:5432/heyearth?sslmode=disable"
	}

	// Migrations run on their own handle without statement_timeout (they may
	// rewrite large tables); the API's pool gets the session limits.
	migDB, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to db: %w", err)
	}
	defer migDB.Close()
	if err = migDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping db: %w", err)
	}

	DB, err = sql.Open("postgres", withSessionLimits(connStr))
	if err != nil {
		return fmt.Errorf("failed to connect to db: %w", err)
	}
	// Postgres allows 100 connections by default; leave room for psql, backups and migrations.
	DB.SetMaxOpenConns(60)
	DB.SetMaxIdleConns(10)
	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping db: %w", err)
	}
	var st string
	_ = DB.QueryRow(`SHOW statement_timeout`).Scan(&st)
	log.Printf("Connected to PostgreSQL successfully (statement_timeout=%s)", st)

	// Run migrations
	driver, err := postgres.WithInstance(migDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("could not create postgres driver: %w", err)
	}

	// Use absolute path or relative path depending on how it's run.
	// For API server, it usually runs from backend/
	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"postgres", driver)
	if err != nil {
		return fmt.Errorf("could not create migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("could not run migrations: %w", err)
	}

	log.Println("Database migrations applied successfully")
	return nil
}

func CloseDB() {
	if DB != nil {
		DB.Close()
	}
}

// OpenReadOnly connects WITHOUT running migrations, every transaction read-only on the server
// (default_transaction_read_only): `heyearth-api --predict-restarts` runs the new build against
// the database the running build uses, before the swap, and must change nothing.
func OpenReadOnly() error {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5432/heyearth?sslmode=disable"
	}
	var err error
	DB, err = sql.Open("postgres", withReadOnly(withSessionLimits(connStr)))
	if err != nil {
		return fmt.Errorf("failed to connect to db: %w", err)
	}
	DB.SetMaxOpenConns(4)
	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping db: %w", err)
	}
	return nil
}
