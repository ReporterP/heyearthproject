// Package testpg gives tests a real PostgreSQL database with every migration applied.
//
// Money code (wallet, payments, promocodes) is tested against Postgres itself: row
// locks, unique keys and CHECK constraints are the behaviour under test, and a fake
// driver cannot show a race. Set TEST_DATABASE_URL to a server the tests may create
// databases on (a throwaway container); without it the tests are skipped.
package testpg

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"

	"github.com/heyearth/backend/internal/db"
)

// migrationsDir is server/backend/migrations, found from this file's location.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// Open creates a fresh database, applies all migrations, points db.DB at it for the
// duration of the test and drops it afterwards. It skips the test when
// TEST_DATABASE_URL is not set.
func Open(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), rand.Intn(1_000_000))
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		admin.Close()
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()

	m, err := migrate.New("file://"+filepath.ToSlash(migrationsDir()), dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	if srcErr != nil || dbErr != nil {
		t.Fatalf("migrate close: %v %v", srcErr, dbErr)
	}

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(20)
	prev := db.DB
	db.DB = conn
	t.Cleanup(func() {
		db.DB = prev
		conn.Close()
		// Other connections may linger for a moment; FORCE ends them.
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); err != nil && !strings.Contains(err.Error(), "does not exist") {
			t.Logf("drop database %s: %v", name, err)
		}
		admin.Close()
	})
	return conn
}

// MustExec runs a statement and fails the test on error.
func MustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// NewUser inserts a user with the given text id and returns users.id.
func NewUser(t *testing.T, conn *sql.DB, userID string) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`INSERT INTO users (user_id) VALUES ($1) RETURNING id`, userID).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", userID, err)
	}
	return id
}
