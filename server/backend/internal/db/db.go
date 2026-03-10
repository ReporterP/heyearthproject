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

	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to db: %w", err)
	}

	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping db: %w", err)
	}

	log.Println("Connected to PostgreSQL successfully")

	// Run migrations
	driver, err := postgres.WithInstance(DB, &postgres.Config{})
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
