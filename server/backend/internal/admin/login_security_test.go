package admin

import (
	"testing"
	"time"
)

// RFC 6238 appendix B (SHA1), truncated to 6 digits.
func TestTOTPVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, err := totpCode(secret, unix/totpPeriod)
		if err != nil || got != want {
			t.Errorf("t=%d: %q %v, want %q", unix, got, err, want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	cur := now.Unix() / totpPeriod
	code, _ := totpCode(secret, cur)
	if st := verifyTOTP(secret, code, now, 0); st != cur {
		t.Fatalf("current code: step %d", st)
	}
	if st := verifyTOTP(secret, code, now, cur); st != 0 {
		t.Fatal("a used code must be refused")
	}
	prev, _ := totpCode(secret, cur-1)
	if st := verifyTOTP(secret, prev, now, 0); st != cur-1 {
		t.Fatal("the previous step is accepted (clock drift)")
	}
	old, _ := totpCode(secret, cur-3)
	if verifyTOTP(secret, old, now, 0) != 0 || verifyTOTP(secret, "12345", now, 0) != 0 || verifyTOTP(secret, "abcdef", now, 0) != 0 {
		t.Fatal("old / malformed codes must be refused")
	}
}

func TestAdminLockoutPerAddress(t *testing.T) {
	now := time.Now()
	for i := 0; i < adminMaxFailures; i++ {
		adminFailed("Owner", "198.51.100.9", now)
	}
	if adminLockedFor("owner", "198.51.100.9", now) == 0 {
		t.Fatal("the attacking address must be locked")
	}
	if adminLockedFor("owner", "203.0.113.4", now) != 0 {
		t.Fatal("the owner's address must stay open (no account-wide lock, audit F13)")
	}
	adminFailed("nobody-like-this", "198.51.100.10", now)
	adminSucceeded("owner", "198.51.100.9")
	if adminLockedFor("owner", "198.51.100.9", now) != 0 {
		t.Fatal("a success clears the lock")
	}
	if adminLockedFor("owner", "198.51.100.9", now.Add(adminLockout+time.Second)) != 0 {
		t.Fatal("locks expire")
	}
}
