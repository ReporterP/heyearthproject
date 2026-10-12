package main

// The PostgreSQL password of the Hub.
//
// It used to be the constant "HeYearthDB1", re-set with ALTER USER on every --main and
// --update-main. Now the Hub's api.env is the single home of DATABASE_URL (and so of the
// password):
//
//   - api.env already has a DATABASE_URL with a password -> kept as is, the role is not touched
//     (this is every --update-main, and every --main on an installed Hub);
//   - the postgres role has no password yet (a fresh install) -> a random one is generated,
//     set with ALTER USER (sent over SSH stdin: never in a process argv, never printed) and
//     written to api.env only;
//   - the role has a password but api.env has no usable DATABASE_URL (api.env lost) -> the
//     DATABASE_URL from server/.env is used if it carries a password (the operator vouches for
//     it), otherwise the run stops: resetting the password would cut off whatever uses it.
//
// Rotation is a manual step: docs/runbooks/restore-hub-backup.md ("Пароль PostgreSQL").

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hubAPIEnv is the API's EnvironmentFile on the Hub.
const hubAPIEnv = appDir + "/api.env"

type dbURLDecision int

const (
	dbURLKeep     dbURLDecision = iota // api.env has a DATABASE_URL with a password: keep it
	dbURLGenerate                      // the role has no password: first install, generate one
	dbURLLocal                         // api.env lost, the role has a password: take server/.env's
	dbURLRefuse                        // api.env lost, the role has a password, nothing to take
)

// decideDatabaseURL is the policy described in the file comment. hubURL is the DATABASE_URL of
// the Hub's api.env ("" when absent), localURL the one of server/.env.
func decideDatabaseURL(hubURL, localURL string, roleHasPassword bool) dbURLDecision {
	switch {
	case databaseURLHasPassword(hubURL):
		return dbURLKeep
	case !roleHasPassword:
		return dbURLGenerate
	case databaseURLHasPassword(localURL):
		return dbURLLocal
	default:
		return dbURLRefuse
	}
}

// databaseURLHasPassword: a postgres:// URL with a non-empty password (a passwordless URL does
// not work over TCP on a stock Ubuntu Postgres, so it counts as "not set").
func databaseURLHasPassword(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User == nil {
		return false
	}
	pw, ok := u.User.Password()
	return ok && pw != ""
}

// databaseURLForPassword is the DATABASE_URL of the local heyearth database for pw (hex, so it
// needs no escaping in a URL, in SQL or in an env file).
func databaseURLForPassword(pw string) string {
	return "postgres://postgres:" + pw + "@localhost:5432/heyearth?sslmode=disable"
}

// newDBPassword: 24 random bytes, hex.
func newDBPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// envFileValue returns the value of the last KEY=VALUE line for key ("" when absent).
func envFileValue(content, key string) string {
	v := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if rest, ok := strings.CutPrefix(line, key+"="); ok {
			v = strings.Trim(rest, `"'`)
		}
	}
	return v
}

// runSSHQuiet runs cmd and returns its stdout WITHOUT printing it (the output may carry a
// secret); stdin, when non-empty, is fed to the command — the way to hand a secret to a remote
// command without putting it into an argv.
func runSSHQuiet(client *ssh.Client, cmd, stdin string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out, errOut strings.Builder
	sess.Stdout, sess.Stderr = &out, &errOut
	if stdin != "" {
		sess.Stdin = strings.NewReader(stdin)
	}
	if err := sess.Run(cmd); err != nil {
		if errOut.Len() > 0 {
			fmt.Fprint(os.Stderr, errOut.String())
		}
		return out.String(), fmt.Errorf("%s: %w", cmdLabel(cmd), err)
	}
	return out.String(), nil
}

// readHubDatabaseURL returns the DATABASE_URL of the Hub's api.env ("" when the file or the
// line is missing).
func readHubDatabaseURL(client *ssh.Client) (string, error) {
	out, err := runSSHQuiet(client, "if [ -f "+hubAPIEnv+" ]; then cat "+hubAPIEnv+"; fi", "")
	if err != nil {
		return "", err
	}
	return envFileValue(out, "DATABASE_URL"), nil
}

// postgresRoleHasPassword asks the Hub's PostgreSQL (peer auth as postgres) whether the role
// postgres has a password.
func postgresRoleHasPassword(client *ssh.Client) (bool, error) {
	out, err := runSSHQuiet(client, `runuser -u postgres -- psql -d postgres -tAc "SELECT rolpassword IS NOT NULL FROM pg_authid WHERE rolname='postgres'"`, "")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "t":
		return true, nil
	case "f":
		return false, nil
	}
	return false, fmt.Errorf("unexpected answer about the postgres role password: %q", strings.TrimSpace(out))
}

// setPostgresPassword sets the role's password; the SQL goes over stdin.
func setPostgresPassword(client *ssh.Client, pw string) error {
	_, err := runSSHQuiet(client, "runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q -d postgres",
		"ALTER USER postgres PASSWORD '"+pw+"';\n")
	return err
}

// writeHubDatabaseURL replaces the DATABASE_URL line of the Hub's api.env (0600, temp + mv);
// the new line goes over stdin.
func writeHubDatabaseURL(client *ssh.Client, dbURL string) error {
	_, err := runSSHQuiet(client, writeDatabaseURLScript(hubAPIEnv), "DATABASE_URL="+dbURL+"\n")
	return err
}

// writeDatabaseURLScript: the env file at path without its DATABASE_URL lines, plus the line read
// from stdin, written to a temp file (umask 077, chmod 600) and moved over the original.
func writeDatabaseURLScript(path string) string {
	return `set -e; umask 077; f=` + path + `; { if [ -f "$f" ]; then grep -v '^DATABASE_URL=' "$f" || true; fi; cat; } > "$f.tmp"; chmod 600 "$f.tmp"; mv -f "$f.tmp" "$f"`
}

// resolveHubDatabaseURL applies decideDatabaseURL on the Hub and returns the DATABASE_URL to
// write into api.env. It changes the role's password only on a first install. Fatal when the
// Hub cannot be asked or the decision is "refuse".
func resolveHubDatabaseURL(client *ssh.Client, cfg *deployConfig) string {
	hubURL, err := readHubDatabaseURL(client)
	if err != nil {
		log.Fatalf("[db] не удалось прочитать %s: %v", hubAPIEnv, err)
	}
	localURL := cfg.env.get("DATABASE_URL", "")
	roleHasPassword := true
	if !databaseURLHasPassword(hubURL) {
		if roleHasPassword, err = postgresRoleHasPassword(client); err != nil {
			log.Fatalf("[db] не удалось проверить пароль роли postgres: %v", err)
		}
	}
	switch decideDatabaseURL(hubURL, localURL, roleHasPassword) {
	case dbURLKeep:
		if databaseURLHasPassword(localURL) && strings.TrimSpace(localURL) != hubURL {
			fmt.Println("[db] WARN: DATABASE_URL в server/.env отличается от api.env на Hub — используется значение с Hub, пароль БД не меняется")
		}
		return hubURL
	case dbURLGenerate:
		pw, err := newDBPassword()
		if err != nil {
			log.Fatalf("[db] генерация пароля: %v", err)
		}
		if err := setPostgresPassword(client, pw); err != nil {
			log.Fatalf("[db] не удалось задать пароль роли postgres: %v", err)
		}
		fmt.Println("[db] первая установка: роли postgres задан случайный пароль, он хранится только в " + hubAPIEnv + " (DATABASE_URL)")
		return databaseURLForPassword(pw)
	case dbURLLocal:
		fmt.Println("[db] в " + hubAPIEnv + " нет DATABASE_URL, у роли postgres уже есть пароль — беру DATABASE_URL из server/.env (пароль не меняю)")
		return strings.TrimSpace(localURL)
	default:
		log.Fatalf("[db] ОШИБКА: у роли postgres уже есть пароль, а в %s нет DATABASE_URL с паролем — сбрасывать пароль не буду.\n"+
			"[db] Верните api.env из бэкапа или укажите DATABASE_URL в server/.env (docs/runbooks/restore-hub-backup.md, «Пароль PostgreSQL»).", hubAPIEnv)
	}
	return ""
}
