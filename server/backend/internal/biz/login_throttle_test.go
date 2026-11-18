package biz

import (
	"sync"
	"testing"
)

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

// Parallel password-change requests can't run past the limit: an attempt is reserved under one
// lock before the slow bcrypt check (review of QA 2026-11-18 fixes).
func TestPasswordChangeAttemptsAreReservedAtomically(t *testing.T) {
	const acc = "a7a7a7a7-7777-4777-8777-777777777777"
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 3*pwChangeFailMax; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pwChangeReserve(acc) {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != pwChangeFailMax {
		t.Fatalf("%d attempts granted in parallel, want %d", granted, pwChangeFailMax)
	}
	pwChangeSucceeded(acc)
	if !pwChangeReserve(acc) {
		t.Fatal("a success must clear the account's attempts")
	}
}
