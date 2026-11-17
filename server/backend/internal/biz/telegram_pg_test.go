package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// TG2: a link token links once; an expired or someone else's token, a second account and a
// Telegram account already linked elsewhere are refused; unlinking frees both sides.
func TestTelegramLinkRules(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	a := testpg.NewUser(t, conn, "tg-a")
	b := testpg.NewUser(t, conn, "tg-b")
	alice := TGAccount{ID: 1001, Username: "alice"}
	bob := TGAccount{ID: 1002, Username: "bob"}

	tok, _, err := CreateTGLinkToken(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if uid, already, err := ConsumeTGLinkToken(ctx, tok, alice); err != nil || uid != a || already {
		t.Fatalf("first use: uid=%d already=%v err=%v", uid, already, err)
	}
	if _, _, err := ConsumeTGLinkToken(ctx, tok, alice); !errors.Is(err, ErrTGLinkInvalid) {
		t.Fatalf("a used token must be refused: %v", err)
	}
	if _, _, err := ConsumeTGLinkToken(ctx, "not-a-token", alice); !errors.Is(err, ErrTGLinkInvalid) {
		t.Fatalf("an unknown token must be refused: %v", err)
	}

	// expired
	tok2, _, _ := CreateTGLinkToken(ctx, b)
	testpg.MustExec(t, conn, `UPDATE tg_link_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE user_id = $1`, b)
	if _, _, err := ConsumeTGLinkToken(ctx, tok2, bob); !errors.Is(err, ErrTGLinkInvalid) {
		t.Fatalf("an expired token must be refused: %v", err)
	}

	// alice's Telegram is linked to a: b's token cannot take it
	tok3, _, _ := CreateTGLinkToken(ctx, b)
	if _, _, err := ConsumeTGLinkToken(ctx, tok3, alice); !errors.Is(err, ErrTGTakenByOther) {
		t.Fatalf("a Telegram linked to another user must be refused: %v", err)
	}
	// a already has alice: linking bob to a is refused
	if _, err := LinkTelegram(ctx, a, bob); !errors.Is(err, ErrTGUserHasOther) {
		t.Fatalf("a second Telegram on one user must be refused: %v", err)
	}
	// the same pair again is fine
	if already, err := LinkTelegram(ctx, a, alice); err != nil || !already {
		t.Fatalf("relinking the same pair: already=%v err=%v", already, err)
	}

	u, err := UserByTelegramID(ctx, alice.ID)
	if err != nil || u == nil || u.DBID != a || !u.NotifyNews || !u.NotifyReminders {
		t.Fatalf("lookup: %+v %v", u, err)
	}
	if none, err := UserByTelegramID(ctx, 999); err != nil || none != nil {
		t.Fatalf("unknown Telegram must be nil: %+v %v", none, err)
	}

	// blocked the bot → no chat; writing again unblocks
	_ = SetTGBlocked(ctx, alice.ID, true)
	if chat, _ := TelegramChatOf(ctx, a); chat != 0 {
		t.Fatalf("a person who blocked the bot gets no messages: %d", chat)
	}
	_ = SetTGBlocked(ctx, alice.ID, false)
	if chat, _ := TelegramChatOf(ctx, a); chat != alice.ID {
		t.Fatalf("chat after unblock: %d", chat)
	}

	// unlink frees both sides
	old, err := UnlinkTelegram(ctx, a)
	if err != nil || old != alice.ID {
		t.Fatalf("unlink: old=%d err=%v", old, err)
	}
	if _, err := LinkTelegram(ctx, b, alice); err != nil {
		t.Fatalf("after unlink alice may link to b: %v", err)
	}
	l, err := GetTGLink(ctx, a)
	if err != nil || l.Linked {
		t.Fatalf("a must be unlinked: %+v %v", l, err)
	}
}

// TG3: a magic sign-in works once, never after 5 minutes, and keeps the redirect on the site.
func TestMagicTokenOneTime(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	a := testpg.NewUser(t, conn, "magic-a")
	tok, err := CreateMagicToken(ctx, a, "/wallet?x=1")
	if err != nil {
		t.Fatal(err)
	}
	uid, userID, next, err := RedeemMagicToken(ctx, tok)
	if err != nil || uid != a || userID != "magic-a" || next != "/wallet?x=1" {
		t.Fatalf("redeem: %d %q %q %v", uid, userID, next, err)
	}
	if _, _, _, err := RedeemMagicToken(ctx, tok); !errors.Is(err, ErrMagicInvalid) {
		t.Fatalf("second use must fail: %v", err)
	}
	tok2, _ := CreateMagicToken(ctx, a, "https://evil.example/")
	testpg.MustExec(t, conn, `UPDATE auth_magic_tokens SET expires_at = NOW() - INTERVAL '1 second' WHERE used_at IS NULL`)
	if _, _, _, err := RedeemMagicToken(ctx, tok2); !errors.Is(err, ErrMagicInvalid) {
		t.Fatalf("expired must fail: %v", err)
	}
	tok3, _ := CreateMagicToken(ctx, a, "//evil.example/")
	if _, _, next, _ := RedeemMagicToken(ctx, tok3); next != "/dashboard" {
		t.Fatalf("an outside redirect must fall back to /dashboard: %q", next)
	}
	testpg.MustExec(t, conn, `UPDATE users SET is_blocked = TRUE WHERE id = $1`, a)
	tok4, _ := CreateMagicToken(ctx, a, "/wallet")
	if _, _, _, err := RedeemMagicToken(ctx, tok4); !errors.Is(err, ErrUserBlocked) {
		t.Fatalf("a blocked account must not sign in: %v", err)
	}
}

func TestSafeNextPath(t *testing.T) {
	for in, want := range map[string]string{
		"/wallet": "/wallet", "/payment?plan=x": "/payment?plan=x", "": "/dashboard", "//x.org": "/dashboard",
		"https://x.org": "/dashboard", "/\\x.org": "/dashboard", "/api/v1/x": "/dashboard", "wallet": "/dashboard",
	} {
		if got := SafeNextPath(in); got != want {
			t.Errorf("SafeNextPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTGStartRefAndChatState(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	if err := SaveTGStartRef(ctx, 5, "FRIEND01"); err != nil {
		t.Fatal(err)
	}
	if c := TakeTGStartRef(ctx, 5); c != "FRIEND01" {
		t.Fatalf("code: %q", c)
	}
	if c := TakeTGStartRef(ctx, 5); c != "" {
		t.Fatalf("taken once: %q", c)
	}
	_ = SetTGChatState(ctx, 5, "support", 60e9)
	if m := GetTGChatState(ctx, 5); m != "support" {
		t.Fatalf("mode: %q", m)
	}
	_ = SetTGChatState(ctx, 5, "", 0)
	if m := GetTGChatState(ctx, 5); m != "" {
		t.Fatalf("cleared: %q", m)
	}
}
