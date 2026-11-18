package biz

import (
	"context"
	"sort"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// D2: a changed password signs out every other session (the caller's stays), the first
// password signs out nothing; logout closes one session; expired sessions are cleaned up.
func TestPasswordChangeAndLogoutCloseSessions(t *testing.T) {
	conn := testpg.Open(t)
	id := testpg.NewUser(t, conn, "sess-user")
	other := testpg.NewUser(t, conn, "sess-other")
	add := func(user int, token, expires string) {
		testpg.MustExec(t, conn, `INSERT INTO user_sessions (user_id, token_hash, expires_at) VALUES ($1, $2, NOW() + $3::interval)`,
			user, hashToken(token), expires)
	}
	left := func(user int) []string {
		rows, err := conn.Query(`SELECT token_hash FROM user_sessions WHERE user_id = $1`, user)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names := map[string]string{}
		for _, tok := range []string{"site", "app", "phone", "old", "x"} {
			names[hashToken(tok)] = tok
		}
		var out []string
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				t.Fatal(err)
			}
			out = append(out, names[h])
		}
		sort.Strings(out)
		return out
	}
	add(id, "site", "30 days")
	add(id, "app", "30 days")
	add(id, "phone", "30 days")
	add(other, "x", "30 days")

	// The first password closes the sessions opened with the bare HiEarth ID, the caller's stays
	// (audit F31); then the other devices sign in again with the password.
	if err := SetCloudPassword("sess-user", "first-pass", "", "site"); err != nil {
		t.Fatal(err)
	}
	if got := left(id); len(got) != 1 || got[0] != "site" {
		t.Fatalf("first password: sessions left %v, want [site]", got)
	}
	add(id, "app", "30 days")
	add(id, "phone", "30 days")
	// A wrong current password changes nothing.
	if err := SetCloudPassword("sess-user", "second-pass", "wrong", "site"); err == nil {
		t.Fatal("a wrong current password was accepted")
	}
	if got := left(id); len(got) != 3 {
		t.Fatalf("a refused change closed sessions: %v", got)
	}
	// A change keeps only the caller's session; another user's is untouched.
	if err := SetCloudPassword("sess-user", "second-pass", "first-pass", "site"); err != nil {
		t.Fatal(err)
	}
	if got := left(id); len(got) != 1 || got[0] != "site" {
		t.Fatalf("after the change: %v, want [site]", got)
	}
	if got := left(other); len(got) != 1 {
		t.Fatalf("another user's session went: %v", got)
	}
	if _, err := LoginByIDOrUsername("sess-user", "second-pass"); err != nil {
		t.Fatalf("the new password does not log in: %v", err)
	}

	// Logout closes exactly that session; an unknown token is a no-op.
	if err := RevokeUserSession("site"); err != nil {
		t.Fatal(err)
	}
	if err := RevokeUserSession("never-issued"); err != nil {
		t.Fatal(err)
	}
	if got := left(id); len(got) != 0 {
		t.Fatalf("after logout: %v", got)
	}

	// Cleanup removes only expired sessions.
	add(id, "old", "-1 day")
	add(id, "app", "1 day")
	n, err := CleanupExpiredUserSessions(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	if got := left(id); len(got) != 1 || got[0] != "app" {
		t.Fatalf("after cleanup: %v", got)
	}
}
