package db

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const migDir = "../../migrations"

func readMigration(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(migDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// golang-migrate cannot step down through a lone up half.
func TestMigrationsArePaired(t *testing.T) {
	ups, _ := filepath.Glob(filepath.Join(migDir, "*.up.sql"))
	if len(ups) == 0 {
		t.Fatal("no migrations found")
	}
	for _, up := range ups {
		if _, err := os.Stat(strings.TrimSuffix(up, ".up.sql") + ".down.sql"); err != nil {
			t.Errorf("%s has no down half", filepath.Base(up))
		}
	}
}

func TestMigration000061Shape(t *testing.T) {
	up := readMigration(t, "000061_node_roles_and_jobs.up.sql")
	for _, re := range []string{
		`ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'exit'`,
		`CHECK \(role IN \('exit', 'relay'\)\)`,
		`ADD COLUMN IF NOT EXISTS ssh_host_key_fp TEXT`,
		`ADD COLUMN IF NOT EXISTS ssh_host_key_confirmed_at TIMESTAMPTZ`,
		`ADD COLUMN IF NOT EXISTS tunnel_fallback_enabled BOOLEAN NOT NULL DEFAULT true`,
		`ALTER COLUMN lifecycle_state SET DEFAULT 'pending'`,
		`server_id INT NOT NULL REFERENCES servers\(id\) ON DELETE CASCADE`,
		`job_id BIGINT NOT NULL REFERENCES provision_jobs\(id\) ON DELETE CASCADE`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_provision_jobs_active\s+ON provision_jobs \(server_id\) WHERE status IN \('queued', 'running'\)`,
		`(?m)^\s*run_token TEXT,$`,
		`runner TEXT NOT NULL DEFAULT 'api' CHECK \(runner IN \('api', 'cli'\)\)`,
		`'unhealthy', 'disabled'`,
		`\('nodes\.provision',`,
		// One row per host (no two provisioning jobs on one machine); the MTProxy job kind.
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_servers_ip_address ON servers \(ip_address\);`,
		`CHECK \(kind IN \('provision', 'reprovision', 'deprovision', 'verify', 'mtproxy'\)\)`,
	} {
		if !regexp.MustCompile(re).MatchString(up) {
			t.Errorf("000061 up lacks /%s/", re)
		}
	}
	// The owner confirms every host key himself: nothing is marked confirmed by a migration.
	if regexp.MustCompile(`(?i)ssh_host_key_confirmed_at\s*=`).MatchString(up) {
		t.Error("000061 must not set ssh_host_key_confirmed_at")
	}
	down := readMigration(t, "000061_node_roles_and_jobs.down.sql")
	for _, want := range []string{"DROP TABLE IF EXISTS provision_steps", "DROP TABLE IF EXISTS provision_jobs",
		"SET DEFAULT 'online'", "DROP COLUMN IF EXISTS role", "permission_key = 'nodes.provision'",
		"DROP INDEX IF EXISTS uq_servers_ip_address"} {
		if !strings.Contains(down, want) {
			t.Errorf("000061 down lacks %q", want)
		}
	}
	if strings.Index(down, "provision_steps") > strings.Index(down, "DROP TABLE IF EXISTS provision_jobs") {
		t.Error("down must drop provision_steps before provision_jobs")
	}
}

// With TEST_DATABASE_URL (a throwaway database) the chain runs up, one step down and up again.
func TestMigrationsApplyOnPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	m, err := migrate.New("file://"+migDir, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatal(err)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatal(err)
	}
	if err := m.Up(); err != nil {
		t.Fatal(err)
	}
}
