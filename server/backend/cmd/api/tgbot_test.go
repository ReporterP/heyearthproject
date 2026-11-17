package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/email"
)

func TestParseTGCommand(t *testing.T) {
	cases := []struct{ in, cmd, arg string }{
		{"/start", "start", ""},
		{"/start link_abcDEF-12", "start", "link_abcDEF-12"},
		{"/Status@hiearth_bot", "status", ""},
		{"/start@hiearth_bot ref_FRIEND01", "start", "ref_FRIEND01"},
	}
	for _, c := range cases {
		cmd, arg, ok := parseTGCommand(c.in)
		if !ok || cmd != c.cmd || arg != c.arg {
			t.Errorf("%q → %q %q %v", c.in, cmd, arg, ok)
		}
	}
	for _, in := range []string{"hello", "", "/"} {
		if _, _, ok := parseTGCommand(in); ok {
			t.Errorf("%q must not be a command", in)
		}
	}
}

func TestTGFormats(t *testing.T) {
	for kop, want := range map[int64]string{72000: "720 ₽", 100000: "1000 ₽", 1000000: "10 000 ₽", 1250: "12,50 ₽", -18000: "−180 ₽"} {
		if got := rub(kop); got != want {
			t.Errorf("rub(%d) = %q, want %q", kop, got, want)
		}
	}
	for n, want := range map[int]string{1: "день", 2: "дня", 5: "дней", 11: "дней", 21: "день", 104: "дня"} {
		if got := plural(n, "день", "дня", "дней"); got != want {
			t.Errorf("plural(%d) = %q", n, got)
		}
	}
	d := time.Date(time.Now().Year(), 2, 14, 12, 0, 0, 0, moscowTZ)
	if got := ruDate(d); got != "14 февраля" {
		t.Errorf("ruDate = %q", got)
	}
	if got := ruDate(d.AddDate(1, 0, 0)); got != "14 февраля "+strconv.Itoa(d.Year()+1) {
		t.Errorf("ruDate next year = %q", got)
	}
}

// The bot's reminder texts follow the e-mail's kinds and never say «VPN».
func TestTGReminderMessages(t *testing.T) {
	base := reminder{PlanName: "Полный", PriceRub: 300, ValidUntil: time.Now().Add(60 * time.Hour)}
	fine := base
	fine.AutoRenew, fine.BalanceKop = true, 50000
	short := base
	short.AutoRenew, short.BalanceKop = true, 12000
	off := base
	if fine.kind() != email.ReminderRenewsFine || short.kind() != email.ReminderTopUp || off.kind() != email.ReminderEnds {
		t.Fatal("kinds")
	}
	if m := tgReminderMessage(fine); !strings.Contains(m.Text, "продлится") || !strings.Contains(m.Text, "300 ₽") {
		t.Errorf("fine: %q", m.Text)
	}
	m := tgReminderMessage(short)
	if !strings.Contains(m.Text, "не хватает 180 ₽") || strings.Contains(m.Text, "сайт") {
		t.Errorf("short: %q", m.Text)
	}
	for _, row := range m.Keyboard.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "pay:") || b.URL != "" {
				t.Errorf("no payment outside Telegram may be offered in the bot: %+v", b)
			}
		}
	}
	short.Today = true
	if m := tgReminderMessage(short); !strings.Contains(m.Text, "Сегодня последний день") {
		t.Errorf("today: %q", m.Text)
	}
	for _, r := range []reminder{fine, short, off} {
		if strings.Contains(strings.ToLower(tgReminderMessage(r).Text), "vpn") {
			t.Error("VPN in a bot text")
		}
	}
}
