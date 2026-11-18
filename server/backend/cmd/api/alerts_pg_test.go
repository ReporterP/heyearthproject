package main

import (
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

// The alert pipeline runs on package-level goroutines that outlive any test: the delivery
// worker reads the Telegram settings and the exit list through db.DB, the journal writes
// alert_events through it. testpg swaps db.DB for every PG test and waits for them first.
func init() { testpg.BeforeSwap(drainAlerts) }

// drainAlerts waits until every alert queued so far is delivered and its journal row written.
func drainAlerts() {
	alerts.flush()
	alertJournalQueue.flush()
}

// lockedLog is a log destination other goroutines may write to while the test reads it.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A PG test that raises an alert and returns at once (TestWalletTopupWithPurchase) leaves the
// delivery and the journal row to the pipeline's goroutines. The journal used to write after
// testpg had pointed db.DB back: into the next test's database, into a closed handle, or — when
// the swap to nil fell between recordAlertEvent's nil check and its use — through a nil *sql.DB
// (panic in (*sql.DB).conn, the whole package FAILed about one run in three).
func TestPGTestEndWaitsForAlertJournal(t *testing.T) {
	outer := testpg.Open(t)
	drainAlerts()
	var out lockedLog
	prevOut := log.Writer()
	log.SetOutput(&out)
	release := make(chan struct{})
	prevSend := alerts.send
	alerts.send = func(string) error { <-release; return nil } // set while the worker is idle (drained)
	const key = "pg-journal-drain-probe"
	t.Cleanup(func() {
		drainAlerts()
		alerts.send = prevSend
		alerts.mu.Lock()
		delete(alerts.state, key)
		alerts.mu.Unlock()
		log.SetOutput(prevOut)
	})

	t.Run("raise and return", func(t *testing.T) {
		testpg.Open(t)
		alerts.Raise(key, AlertWarn, "Проверка журнала", "")
		// Delivery stays blocked until this test has ended and a moment longer: without the
		// wait in testpg, db.DB points at outer long before the journal writes the row.
		t.Cleanup(func() { time.AfterFunc(50*time.Millisecond, func() { close(release) }) })
	})
	drainAlerts()

	var n int
	if err := outer.QueryRow(`SELECT COUNT(*) FROM alert_events WHERE alert_key = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d alert_events row(s) of the finished subtest landed in the next test's database: the journal wrote after testpg swapped db.DB", n)
	}
	logged := out.String()
	if !strings.Contains(logged, "raise "+key) {
		t.Fatalf("the alert was not delivered; log:\n%s", logged)
	}
	if strings.Contains(logged, "запись в alert_events") {
		t.Fatalf("the journal row was not written into the subtest's database; log:\n%s", logged)
	}
}
