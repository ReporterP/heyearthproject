package main

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/yookassa"
)

func TestBoardStaleIsUnknown(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	now := t0
	b := newBoard(map[string]time.Duration{"hub": time.Minute})
	b.now = func() time.Time { return now }
	b.Set("hub", "Хаб", "ok", "")
	if got := b.Snapshot(t0.Add(2 * time.Minute)); len(got) != 1 || got[0].State != "ok" || !got[0].CheckedAt.Equal(t0) {
		t.Fatalf("two intervals later: %+v, want ok", got)
	}
	got := b.Snapshot(t0.Add(4 * time.Minute))
	if len(got) != 1 || got[0].State != "unknown" || !strings.Contains(got[0].Detail, "нет свежей проверки") {
		t.Fatalf("four intervals later: %+v, want unknown — a check that stopped running proves nothing", got)
	}
	// a fresh result makes it known again
	now = t0.Add(5 * time.Minute)
	b.Set("hub", "Хаб", "down", "сайт не открывается")
	if got := b.Snapshot(now); got[0].State != "down" || got[0].Detail != "сайт не открывается" {
		t.Fatalf("after a fresh check: %+v", got)
	}
}

func TestBoardOrderAndIntervals(t *testing.T) {
	b := newBoard(boardIntervals)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return now }
	for _, id := range []string{"backup", "exit:3", "db", "entry:5", "hub", "subscription", "exit:2", "cert", "payments", "bot", "dns", "watchdog"} {
		b.Set(id, id, "ok", "")
	}
	var ids []string
	for _, p := range b.Snapshot(now) {
		ids = append(ids, p.ID)
	}
	want := []string{"hub", "entry:5", "exit:2", "exit:3", "subscription", "payments", "bot", "dns", "cert", "backup", "db", "watchdog"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	// the domain is looked at once a day: a result three hours old is fresh
	if got := b.Snapshot(now.Add(3 * time.Hour)); stateOf(got, "dns") != "ok" || stateOf(got, "hub") != "unknown" {
		t.Fatalf("three hours later: dns %q (want ok), hub %q (want unknown)", stateOf(got, "dns"), stateOf(got, "hub"))
	}
}

func stateOf(paths []pathState, id string) string {
	for _, p := range paths {
		if p.ID == id {
			return p.State
		}
	}
	return ""
}

func TestDomainExpiryThresholds(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	days := func(n int) time.Time { return now.Add(time.Duration(n) * 24 * time.Hour) }
	for _, c := range []struct {
		left      int
		wantState string
		wantLevel AlertLevel
		wantAlert bool
	}{
		{400, "ok", "", false}, {61, "ok", "", false}, {59, "warn", AlertWarn, true}, {29, "warn", AlertWarn, true},
		{13, "down", AlertCritical, true}, {-1, "down", AlertCritical, true},
	} {
		state, level, _, alert := domainExpiryVerdict("site.example.test", days(c.left), now)
		if state != c.wantState || alert != c.wantAlert || (alert && level != c.wantLevel) {
			t.Errorf("%d days left: state %q level %q alert %v; want %q %q %v", c.left, state, level, alert, c.wantState, c.wantLevel, c.wantAlert)
		}
	}
	// the check itself: 59 days raises, a renewal resolves, a failed lookup does neither
	captureLog(t)
	var raised, resolved []string
	b := newBoard(boardIntervals)
	check := domainCheck{
		host: "site.example.test", board: b, now: func() time.Time { return now },
		raise:   func(key string, _ AlertLevel, _, _ string) { raised = append(raised, key) },
		resolve: func(key, _ string) { resolved = append(resolved, key) },
	}
	check.paidTill = func(string) (time.Time, error) { return days(59), nil }
	check.run()
	if !reflect.DeepEqual(raised, []string{"domain-expiry"}) || stateOf(b.Snapshot(now), "dns") != "warn" {
		t.Fatalf("59 days: raised %v, dns %q", raised, stateOf(b.Snapshot(now), "dns"))
	}
	check.paidTill = func(string) (time.Time, error) { return time.Time{}, errWhois }
	check.run()
	if len(raised) != 1 || len(resolved) != 0 || stateOf(b.Snapshot(now), "dns") != "unknown" {
		t.Fatalf("a failed lookup: raised %v, resolved %v, dns %q; want nothing new and unknown", raised, resolved, stateOf(b.Snapshot(now), "dns"))
	}
	check.paidTill = func(string) (time.Time, error) { return days(365), nil }
	check.run()
	if !reflect.DeepEqual(resolved, []string{"domain-expiry"}) || stateOf(b.Snapshot(now), "dns") != "ok" {
		t.Fatalf("after the renewal: resolved %v, dns %q", resolved, stateOf(b.Snapshot(now), "dns"))
	}
}

func TestParseWhoisPaidTill(t *testing.T) {
	out := "% TCI Whois Service. Terms of use:\n\ndomain:        SITE.EXAMPLE.TEST\nstate:         REGISTERED, DELEGATED, VERIFIED\ncreated:       2025-03-01T10:00:00Z\npaid-till:     2027-03-01T10:00:00Z\nfree-date:     2027-04-01\n"
	got, err := parseWhoisPaidTill(out)
	if err != nil || !got.Equal(time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("paid-till = %v, %v", got, err)
	}
	// registries of other zones call it differently
	if got, err := parseWhoisPaidTill("Domain Name: example.test\nRegistry Expiry Date: 2027-05-06T00:00:00Z\n"); err != nil || got.Year() != 2027 || got.Month() != 5 {
		t.Fatalf("Registry Expiry Date = %v, %v", got, err)
	}
	if _, err := parseWhoisPaidTill("No entries found for the selected source(s).\n"); err == nil {
		t.Fatal("an answer without a date was parsed")
	}
}

func TestRebootPendingVerdict(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if state, _, alert := rebootPendingVerdict(time.Time{}, now); state != "ok" || alert {
		t.Errorf("no reboot-required file: %q, alert %v", state, alert)
	}
	if state, detail, alert := rebootPendingVerdict(now.Add(-3*24*time.Hour), now); state != "ok" || alert || !strings.Contains(detail, "3") {
		t.Errorf("3 days: %q %q alert %v; want ok with the age and no alert", state, detail, alert)
	}
	state, detail, alert := rebootPendingVerdict(now.Add(-15*24*time.Hour), now)
	if state != "warn" || !alert || !strings.Contains(detail, "нужна перезагрузка 15") {
		t.Errorf("15 days: %q %q alert %v; want warn «нужна перезагрузка 15 дн.» with the alert", state, detail, alert)
	}
}

func TestBackupVerifyVerdict(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	stamp := func(age time.Duration, result string) string {
		return strconv.FormatInt(now.Add(-age).Unix(), 10) + " " + result + "\n"
	}
	if failed, _ := backupVerifyVerdict(stamp(2*24*time.Hour, "success"), now); failed {
		t.Error("a drill that passed two days ago reported as failed")
	}
	if failed, detail := backupVerifyVerdict(stamp(2*24*time.Hour, "exit-code"), now); !failed || !strings.Contains(detail, "не прошло") {
		t.Errorf("a failed drill: failed %v, %q", failed, detail)
	}
	// no drill yet, or a file nobody can read: nothing to say
	for _, body := range []string{"", "garbage", "abc success"} {
		if failed, _ := backupVerifyVerdict(body, now); failed {
			t.Errorf("%q reported as a failed drill", body)
		}
	}
}

type downGetter struct{ err error }

func (g downGetter) GetPayment(string) (*yookassa.Payment, error) { return nil, g.err }

// The «Оплаты» cell says when the payment provider did not answer in the last pass; a pass
// that reached it (or had nothing to ask) is ok. A payment the provider does not know (404)
// is the provider answering.
func TestPaymentsCellReflectsProvider(t *testing.T) {
	d := newPEDB(&peRow{id: 1, ykID: "p-1", status: "pending", created: time.Now().Add(-10 * time.Minute)})
	usePaymentFakes(t, d)
	prevClient, prevProcess := newPaymentSyncClient, syncProcessPayment
	t.Cleanup(func() { newPaymentSyncClient, syncProcessPayment = prevClient, prevProcess })
	syncProcessPayment = func(*yookassa.Payment) error { return nil }
	cell := func() pathState {
		for _, p := range healthBoard.Snapshot(time.Now()) {
			if p.ID == "payments" {
				return p
			}
		}
		t.Fatal("no payments cell")
		return pathState{}
	}
	for _, tc := range []struct {
		name   string
		getter paymentGetter
		state  string
		detail string
	}{
		{"transport error", downGetter{errors.New("http do: dial tcp: i/o timeout")}, "warn", "провайдер оплат не отвечает"},
		{"provider 5xx", downGetter{&yookassa.APIError{HTTPStatus: 502}}, "warn", "провайдер оплат не отвечает"},
		// a revoked or wrong key: nobody can top up — the provider answers, but refuses us
		{"provider refuses the key", downGetter{&yookassa.APIError{HTTPStatus: 401, Code: "invalid_credentials"}}, "warn", "провайдер оплат отказывает в доступе"},
		{"provider forbids", downGetter{&yookassa.APIError{HTTPStatus: 403}}, "warn", "провайдер оплат отказывает в доступе"},
		{"provider answers", fakeGetter{}, "ok", "сверка платежей с ЮKassa прошла"},
		{"unknown payment", fakeGetter{gone: map[string]bool{"p-1": true}}, "ok", "сверка платежей с ЮKassa прошла"},
	} {
		g := tc.getter
		newPaymentSyncClient = func() (paymentGetter, error) { return g, nil }
		setPaymentsCell(syncPendingPayments())
		if got := cell(); got.State != tc.state || !strings.Contains(got.Detail, tc.detail) {
			t.Errorf("%s: cell %s %q, want %s %q", tc.name, got.State, got.Detail, tc.state, tc.detail)
		}
	}
}
