package main

// The PostgreSQL password of the Hub.
//
// It used to be the constant "HeYearthDB1", re-set with ALTER USER on every --main and
// --update-main. Now the Hub's api.env is the single home of DATABASE_URL (and so of the
// password):
//
//   - api.env has a DATABASE_URL with a password and the role has one -> kept as is, the role is
//     not touched (every --update-main, and every --main on an installed Hub);
//   - api.env has a DATABASE_URL with a password but the role has none -> a fresh install was
//     interrupted between writing api.env and ALTER USER: the role gets api.env's password;
//   - neither has one (a fresh install) -> a random password is generated, written to api.env
//     FIRST (over SSH stdin, temp + mv, 0600) and only then set with ALTER USER (also over
//     stdin: never in a process argv, never printed). SFTP and SSH to the Hub are unstable: in
//     this order a drop can only leave the previous case behind, never a password nobody has;
//   - the role has a password but api.env has no usable DATABASE_URL (api.env lost) -> the
//     DATABASE_URL from server/.env, but only after a test connection with it succeeded;
//     otherwise the run stops: resetting the password would cut off whatever uses it.
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
	dbURLKeep     dbURLDecision = iota // api.env and the role both have a password: keep it
	dbURLAdoptHub                      // api.env has one, the role none (interrupted first install)
	dbURLGenerate                      // neither has one: first install, generate
	dbURLLocal                         // api.env lost, the role has a password: server/.env's, if it connects
	dbURLRefuse                        // api.env lost, the role has a password, nothing to take
)

// decideDatabaseURL is the policy described in the file comment. hubURL is the DATABASE_URL of
// the Hub's api.env ("" when absent), localURL the one of server/.env.
func decideDatabaseURL(hubURL, localURL string, roleHasPassword bool) dbURLDecision {
	switch {
	case databaseURLHasPassword(hubURL) && roleHasPassword:
		return dbURLKeep
	case databaseURLHasPassword(hubURL):
		return dbURLAdoptHub
	case !roleHasPassword:
		return dbURLGenerate
	case databaseURLHasPassword(localURL):
		return dbURLLocal
	default:
		return dbURLRefuse
	}
}

// databaseURLPassword returns the password of a postgres:// URL ("" when none).
func databaseURLPassword(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
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

// alterPasswordSQL is the statement setPostgresPassword sends over stdin (quotes doubled, so an
// adopted password from api.env cannot end the literal).
func alterPasswordSQL(pw string) string {
	return "ALTER USER postgres PASSWORD '" + strings.ReplaceAll(pw, "'", "''") + "';" + string(rune(10))
}

// setPostgresPassword sets the role's password; the SQL goes over stdin.
func setPostgresPassword(client *ssh.Client, pw string) error {
	_, err := runSSHQuiet(client, "runuser -u postgres -- psql -v ON_ERROR_STOP=1 -q -d postgres", alterPasswordSQL(pw))
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

// hubDBOps are the Hub-side steps of the password policy (an interface, so the order of the
// steps is testable).
type hubDBOps interface {
	readHubURL() (string, error)
	roleHasPassword() (bool, error)
	setRolePassword(pw string) error
	writeHubURL(dbURL string) error
	verifyURL(dbURL string) error
}

// sshHubDBOps runs them on the Hub over SSH.
type sshHubDBOps struct{ client *ssh.Client }

func (o sshHubDBOps) readHubURL() (string, error)     { return readHubDatabaseURL(o.client) }
func (o sshHubDBOps) roleHasPassword() (bool, error)  { return postgresRoleHasPassword(o.client) }
func (o sshHubDBOps) setRolePassword(pw string) error { return setPostgresPassword(o.client, pw) }
func (o sshHubDBOps) writeHubURL(dbURL string) error  { return writeHubDatabaseURL(o.client, dbURL) }
func (o sshHubDBOps) verifyURL(dbURL string) error {
	_, closeDB, err := openDBTunnel(o.client, dbURL) // connects and pings through the SSH tunnel
	if err != nil {
		return err
	}
	closeDB()
	return nil
}

// resolveDatabaseURL applies decideDatabaseURL and returns the DATABASE_URL api.env must carry.
// It changes the role's password only when the role has none.
func resolveDatabaseURL(ops hubDBOps, localURL string) (string, error) {
	hubURL, err := ops.readHubURL()
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать %s: %w", hubAPIEnv, err)
	}
	roleHasPassword, err := ops.roleHasPassword()
	if err != nil {
		return "", fmt.Errorf("не удалось проверить пароль роли postgres: %w", err)
	}
	localURL = strings.TrimSpace(localURL)
	switch decideDatabaseURL(hubURL, localURL, roleHasPassword) {
	case dbURLKeep:
		if databaseURLHasPassword(localURL) && localURL != hubURL {
			fmt.Println("[db] WARN: DATABASE_URL в server/.env отличается от api.env на Hub — используется значение с Hub, пароль БД не меняется")
		}
		return hubURL, nil
	case dbURLAdoptHub:
		if err := ops.setRolePassword(databaseURLPassword(hubURL)); err != nil {
			return "", fmt.Errorf("не удалось задать роли postgres пароль из %s: %w", hubAPIEnv, err)
		}
		fmt.Println("[db] у роли postgres не было пароля, а в " + hubAPIEnv + " он есть (прерванная первая установка) — роли задан пароль из api.env")
		return hubURL, nil
	case dbURLGenerate:
		pw, err := newDBPassword()
		if err != nil {
			return "", fmt.Errorf("генерация пароля: %w", err)
		}
		dbURL := databaseURLForPassword(pw)
		// api.env first: a drop after this step leaves the AdoptHub case, never a lost password.
		if err := ops.writeHubURL(dbURL); err != nil {
			return "", fmt.Errorf("не удалось записать DATABASE_URL в %s: %w", hubAPIEnv, err)
		}
		if err := ops.setRolePassword(pw); err != nil {
			return "", fmt.Errorf("DATABASE_URL записан в %s, но пароль роли postgres задать не удалось: %w (повторный запуск задаст его из api.env)", hubAPIEnv, err)
		}
		fmt.Println("[db] первая установка: роли postgres задан случайный пароль, он хранится только в " + hubAPIEnv + " (DATABASE_URL)")
		return dbURL, nil
	case dbURLLocal:
		if err := ops.verifyURL(localURL); err != nil {
			return "", fmt.Errorf("в %s нет DATABASE_URL, у роли postgres есть пароль, а DATABASE_URL из server/.env не подключается (%v) — сбрасывать пароль не буду; "+
				"верните api.env из бэкапа или исправьте DATABASE_URL в server/.env (docs/runbooks/restore-hub-backup.md, «Пароль PostgreSQL»)", hubAPIEnv, err)
		}
		fmt.Println("[db] в " + hubAPIEnv + " нет DATABASE_URL, у роли postgres есть пароль — беру DATABASE_URL из server/.env (подключение проверено, пароль не меняю)")
		return localURL, nil
	}
	return "", fmt.Errorf("у роли postgres уже есть пароль, а в %s нет DATABASE_URL с паролем — сбрасывать пароль не буду. "+
		"Верните api.env из бэкапа или укажите DATABASE_URL в server/.env (docs/runbooks/restore-hub-backup.md, «Пароль PostgreSQL»)", hubAPIEnv)
}

// resolveHubDatabaseURL is resolveDatabaseURL on the Hub; fatal on any error.
func resolveHubDatabaseURL(client *ssh.Client, cfg *deployConfig) string {
	dbURL, err := resolveDatabaseURL(sshHubDBOps{client}, cfg.env.get("DATABASE_URL", ""))
	if err != nil {
		log.Fatalf("[db] ОШИБКА: %v", err)
	}
	return dbURL
}
