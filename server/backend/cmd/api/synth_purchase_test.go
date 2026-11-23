package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSyntheticPurchaseVerdict(t *testing.T) {
	var raised, resolved []string
	v := &purchaseVerdict{raise: func(key string, _ AlertLevel, _, _ string) { raised = append(raised, key) },
		resolve: func(key, _ string) { resolved = append(resolved, key) }}
	providerDown := []purchaseCheck{{Step: "plans", OK: true}, {Step: "provider", OK: false, Detail: "timeout"}, {Step: "wallet-purchase", OK: true}}
	for i := 0; i < 3; i++ {
		v.fold(providerDown)
	}
	if len(raised) != 0 {
		t.Errorf("a provider outage alone is not «покупка не проходит»: %v", raised)
	}
	bad := []purchaseCheck{{Step: "plans", OK: true}, {Step: "provider", OK: true}, {Step: "wallet-purchase", OK: false, Detail: "insufficient"}}
	v.fold(bad)
	v.fold(bad)
	if len(raised) != 0 {
		t.Fatalf("alert after two failures: %v", raised)
	}
	v.fold(bad)
	if !slices.Equal(raised, []string{synthPurchaseAlert}) {
		t.Fatalf("third failure: %v", raised)
	}
	good := []purchaseCheck{{Step: "plans", OK: true}, {Step: "provider", OK: true}, {Step: "wallet-purchase", OK: true}}
	v.fold(good)
	v.fold(good)
	if !slices.Equal(resolved, []string{synthPurchaseAlert}) {
		t.Errorf("two good runs: resolved %v", resolved)
	}
	// a blind run (no test user) changes nothing
	before := v.fails
	v.fold([]purchaseCheck{{Step: "wallet-purchase", Blind: true, Detail: "нет тестового пользователя"}})
	if v.fails != before {
		t.Error("a blind run counted")
	}
}

// The source never reaches what runs after a real purchase's COMMIT.
func TestSyntheticPurchaseCallsNothingAfterCommit(t *testing.T) {
	b, err := os.ReadFile("synth_purchase.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"Commit(", "RequestXraySync", "kickWalletReceipts", "walletPurchaseDone", "NotifySubscriptionChange", "sendActivationEmail", "notifyTG"} {
		if strings.Contains(src, banned) {
			t.Errorf("synth_purchase.go calls %s", banned)
		}
	}
	_ = errors.New
	_ = context.Background
}
