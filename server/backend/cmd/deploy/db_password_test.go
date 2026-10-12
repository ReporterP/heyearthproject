package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDecideDatabaseURL(t *testing.T) {
	const (
		withPw   = "postgres://postgres:0123abcd@localhost:5432/heyearth?sslmode=disable"
		otherPw  = "postgres://postgres:ffff@localhost:5432/heyearth?sslmode=disable"
		noPw     = "postgres://postgres@localhost:5432/heyearth?sslmode=disable"
		emptyPw  = "postgres://postgres:@localhost:5432/heyearth?sslmode=disable"
		notAnURL = "::not a url"
	)
	tests := []struct {
		name            string
		hub, local      string
		roleHasPassword bool
		want            dbURLDecision
	}{
		{"installed Hub (every --update-main): keep api.env, whatever server/.env says", withPw, otherPw, true, dbURLKeep},
		{"api.env written but ALTER USER never ran (interrupted first install): adopt api.env's password", withPw, otherPw, false, dbURLAdoptHub},
		{"installed Hub, no local DATABASE_URL: keep", withPw, "", true, dbURLKeep},
		{"fresh Hub: generate, even if server/.env carries a (legacy) password", "", otherPw, false, dbURLGenerate},
		{"fresh Hub, nothing anywhere: generate", "", "", false, dbURLGenerate},
		{"api.env with a passwordless URL and a role without password (old fix-db state): generate", noPw, "", false, dbURLGenerate},
		{"api.env lost, the role has a password, server/.env has one: use it, do not reset", "", otherPw, true, dbURLLocal},
		{"api.env lost, the role has a password, nothing usable: refuse", "", noPw, true, dbURLRefuse},
		{"empty password counts as none", emptyPw, emptyPw, true, dbURLRefuse},
		{"garbage counts as none", notAnURL, "", true, dbURLRefuse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideDatabaseURL(tc.hub, tc.local, tc.roleHasPassword); got != tc.want {
				t.Errorf("decideDatabaseURL(%q, %q, %v) = %d, want %d", tc.hub, tc.local, tc.roleHasPassword, got, tc.want)
			}
		})
	}
}

func TestDBPasswordHelpers(t *testing.T) {
	pw, err := newDBPassword()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{48}$`).MatchString(pw) {
		t.Errorf("password %q is not 48 hex characters", pw)
	}
	if pw2, _ := newDBPassword(); pw2 == pw {
		t.Error("two generated passwords are equal")
	}
	u := databaseURLForPassword(pw)
	if u != "postgres://postgres:"+pw+"@localhost:5432/heyearth?sslmode=disable" || !databaseURLHasPassword(u) {
		t.Errorf("url %q", u)
	}
	env := "APP_DIR=/opt/heyearth/mainserver\nDATABASE_URL=postgres://postgres:old@localhost:5432/heyearth\r\nJWT_SECRET=x\nDATABASE_URL=\"postgres://postgres:new@localhost:5432/heyearth\"\n"
	if got := envFileValue(env, "DATABASE_URL"); got != "postgres://postgres:new@localhost:5432/heyearth" {
		t.Errorf("envFileValue = %q (last line wins, quotes stripped)", got)
	}
	if got := envFileValue(env, "MISSING"); got != "" {
		t.Errorf("missing key: %q", got)
	}
}

// The api.env rewrite keeps every other line, replaces DATABASE_URL with the stdin line and
// leaves the file 0600.
func TestWriteDatabaseURLScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	out, exit := runBash(t, `T=$(mktemp -d); export T; trap 'rm -rf "$T"' EXIT
printf 'APP_DIR=/x\nDATABASE_URL=postgres://postgres:old@localhost/heyearth\nJWT_SECRET=s\n' > "$T/api.env"; chmod 644 "$T/api.env"
echo 'DATABASE_URL=postgres://postgres:new@localhost/heyearth' | bash -c `+shQuote(writeDatabaseURLScript(`"$T/api.env"`))+`
echo "STATE rc=$?"; echo "STATE env=$(tr '\n' '|' < "$T/api.env")"; echo "STATE tmp=$(ls "$T" | grep -c tmp)"
echo 'DATABASE_URL=postgres://postgres:fresh@localhost/heyearth' | bash -c `+shQuote(writeDatabaseURLScript(`"$T/none.env"`))+`
echo "STATE fresh=$(cat "$T/none.env")"
`)
	if exit != 0 {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
	for _, l := range []string{"STATE rc=0", "STATE env=APP_DIR=/x|JWT_SECRET=s|DATABASE_URL=postgres://postgres:new@localhost/heyearth|", "STATE tmp=0",
		"STATE fresh=DATABASE_URL=postgres://postgres:fresh@localhost/heyearth"} {
		if !hasLine(out, l) {
			t.Errorf("missing %q:\n%s", l, out)
		}
	}
}

// No deploy path may set or embed the old constant password any more.
func TestNoHardcodedDBPassword(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(files, "../../quick_deploy.go") {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			if strings.Contains(code, "HeYearthDB1") || regexp.MustCompile(`(?i)ALTER USER postgres PASSWORD '[A-Za-z0-9]`).MatchString(code) {
				t.Errorf("%s:%d sets or embeds a fixed DB password: %s", f, i+1, code)
			}
		}
	}
}

// fakeHubDB records the order of the Hub-side steps.
type fakeHubDB struct {
	hubURL      string
	rolePw      string
	failWrite   bool
	failVerify  bool
	calls       []string
	writtenURL  string
	verifiedURL string
}

func (f *fakeHubDB) readHubURL() (string, error) {
	f.calls = append(f.calls, "read")
	return f.hubURL, nil
}
func (f *fakeHubDB) roleHasPassword() (bool, error) { return f.rolePw != "", nil }
func (f *fakeHubDB) setRolePassword(pw string) error {
	f.calls = append(f.calls, "alter")
	f.rolePw = pw
	return nil
}
func (f *fakeHubDB) writeHubURL(u string) error {
	f.calls = append(f.calls, "write")
	if f.failWrite {
		return errors.New("ssh: connection lost")
	}
	f.writtenURL, f.hubURL = u, u
	return nil
}
func (f *fakeHubDB) verifyURL(u string) error {
	f.calls = append(f.calls, "verify")
	f.verifiedURL = u
	if f.failVerify {
		return errors.New("password authentication failed")
	}
	return nil
}

func TestResolveDatabaseURLOrder(t *testing.T) {
	const old = "postgres://postgres:0123abcd@localhost:5432/heyearth?sslmode=disable"
	t.Run("fresh install: api.env is written BEFORE the role gets the password", func(t *testing.T) {
		f := &fakeHubDB{}
		u, err := resolveDatabaseURL(f, "")
		if err != nil || strings.Join(f.calls, ",") != "read,write,alter" || u != f.writtenURL || databaseURLPassword(u) != f.rolePw {
			t.Fatalf("u=%q err=%v calls=%v role=%q", u, err, f.calls, f.rolePw)
		}
	})
	t.Run("fresh install, api.env cannot be written: the role keeps no password", func(t *testing.T) {
		f := &fakeHubDB{failWrite: true}
		if _, err := resolveDatabaseURL(f, ""); err == nil || f.rolePw != "" || strings.Contains(strings.Join(f.calls, ","), "alter") {
			t.Fatalf("err=%v calls=%v role=%q", err, f.calls, f.rolePw)
		}
	})
	t.Run("a run after a drop between write and ALTER adopts api.env's password", func(t *testing.T) {
		f := &fakeHubDB{hubURL: old}
		u, err := resolveDatabaseURL(f, "")
		if err != nil || u != old || f.rolePw != "0123abcd" || strings.Join(f.calls, ",") != "read,alter" {
			t.Fatalf("u=%q err=%v calls=%v role=%q", u, err, f.calls, f.rolePw)
		}
	})
	t.Run("installed Hub: nothing written, the role untouched", func(t *testing.T) {
		f := &fakeHubDB{hubURL: old, rolePw: "0123abcd"}
		if u, err := resolveDatabaseURL(f, ""); err != nil || u != old || strings.Join(f.calls, ",") != "read" {
			t.Fatalf("u=%q err=%v calls=%v", u, err, f.calls)
		}
	})
	t.Run("api.env lost: server/.env's URL only after a successful test connection", func(t *testing.T) {
		f := &fakeHubDB{rolePw: "secret"}
		local := "postgres://postgres:secret@localhost:5432/heyearth?sslmode=disable"
		if u, err := resolveDatabaseURL(f, local); err != nil || u != local || f.verifiedURL != local || strings.Contains(strings.Join(f.calls, ","), "alter") {
			t.Fatalf("u=%q err=%v calls=%v", u, err, f.calls)
		}
		f = &fakeHubDB{rolePw: "secret", failVerify: true}
		if _, err := resolveDatabaseURL(f, local); err == nil || !strings.Contains(err.Error(), "не подключается") || strings.Contains(strings.Join(f.calls, ","), "alter") {
			t.Fatalf("a URL that does not connect must be refused without touching the role: err=%v calls=%v", err, f.calls)
		}
	})
	t.Run("a quote in an adopted password cannot end the SQL literal", func(t *testing.T) {
		if got := alterPasswordSQL("a'b"); got != "ALTER USER postgres PASSWORD 'a''b';"+string(rune(10)) {
			t.Fatalf("%q", got)
		}
	})
}
