package biz

import "testing"

func TestLoginThrottlePerAddress(t *testing.T) {
	const acc = 424242
	for i := 0; i < loginFailMax; i++ {
		loginFailed(acc, "198.51.100.9")
	}
	if !loginThrottled(acc, "198.51.100.9") {
		t.Fatal("the guessing address must be throttled")
	}
	if loginThrottled(acc, "203.0.113.4") {
		t.Fatal("the owner's address must stay open (no lockout by a stranger)")
	}
	loginSucceeded(acc, "198.51.100.9")
	if loginThrottled(acc, "198.51.100.9") {
		t.Fatal("success clears the address")
	}
	for i := 0; i < loginFailMaxAll; i++ {
		loginFailed(acc, "")
	}
	if !loginThrottled(acc, "203.0.113.4") {
		t.Fatal("the account-wide backstop must stop a distributed guess")
	}
}
