package biz

import (
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A login name is short and guessable, so the answers must not tell a stranger whether one
// exists: an unknown login name answers exactly like a known one with a wrong or missing
// password (every login name has a password). A HiEarth ID (a random UUID) can't be guessed,
// so «user not found» stays for it — it tells a person who mistyped their ID what's wrong.
func TestLoginAnswersDoNotRevealLoginNames(t *testing.T) {
	conn := testpg.Open(t)
	const id = "7b0d2c1e-9f3a-4c5b-8d6e-1a2b3c4d5e6f"
	testpg.NewUser(t, conn, id)
	if err := SetCloudPassword(id, "right-pass-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetUsername(id, "known-name"); err != nil {
		t.Fatal(err)
	}
	answer := func(login, password string) string {
		t.Helper()
		_, err := LoginByIDOrUsernameFrom(login, password, "198.51.100.7")
		if err == nil {
			return "ok"
		}
		return err.Error()
	}
	for _, c := range []struct{ name, login, password, want string }{
		{"known name, wrong password", "known-name", "nope", "invalid password"},
		{"unknown name, a password", "unknown-name", "nope", "invalid password"},
		{"known name, empty password", "known-name", "", "password required"},
		{"unknown name, empty password", "unknown-name", "", "password required"},
		{"unknown HiEarth ID", "00000000-0000-4000-8000-000000000000", "nope", "user not found"},
		{"known name, right password", "known-name", "right-pass-1", "ok"},
	} {
		if got := answer(c.login, c.password); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Changing the password with a stolen session must not allow guessing the current one:
// wrong current passwords are limited per account, and past the limit even the right one
// is refused until the window passes.
func TestPasswordChangeLimitsWrongCurrentPasswords(t *testing.T) {
	conn := testpg.Open(t)
	const id = "3c2b1a09-8f7e-4d6c-9b5a-0f1e2d3c4b5a"
	testpg.NewUser(t, conn, id)
	if err := SetCloudPassword(id, "right-pass-1", "", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pwChangeFailMax; i++ {
		if err := SetCloudPassword(id, "new-pass-22", "wrong-guess", ""); err == nil || err.Error() != "invalid current password" {
			t.Fatalf("try %d: got %v, want invalid current password", i+1, err)
		}
	}
	err := SetCloudPassword(id, "new-pass-22", "right-pass-1", "")
	if !errors.Is(err, ErrTooManyPasswordAttempts) {
		t.Fatalf("past the limit: got %v, want ErrTooManyPasswordAttempts", err)
	}
}
