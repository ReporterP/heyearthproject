package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// «Запустить сейчас» used to answer "triggered" while no goroutine read the channel (the tuner
// starts only if it was enabled at API start). Now it says so.
func TestAutoTunerRunNowIsHonest(t *testing.T) {
	oldSetting := autoTunerSetting
	t.Cleanup(func() { autoTunerSetting = oldSetting; SetAutoTunerLoopRunning(false) })
	drain := func() {
		select {
		case <-AutoTunerRunChan:
		default:
		}
	}
	drain()
	run := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		AutoTunerRunNowHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/auto-tuner/run-now", nil))
		return rec
	}

	// Switched on after the API started: enabled, but no goroutine reads the channel.
	autoTunerSetting = func(string, string) string { return "true" }
	SetAutoTunerLoopRunning(false)
	if rec := run(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "не запущен") || len(AutoTunerRunChan) != 0 {
		t.Fatalf("no tuner goroutine: %d %s (queued %d)", rec.Code, rec.Body.String(), len(AutoTunerRunChan))
	}
	SetAutoTunerLoopRunning(true)
	autoTunerSetting = func(string, string) string { return "false" }
	if rec := run(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "выключен") || len(AutoTunerRunChan) != 0 {
		t.Fatalf("tuner switched off: %d %s", rec.Code, rec.Body.String())
	}
	autoTunerSetting = func(string, string) string { return "true" }
	if rec := run(); rec.Code != http.StatusOK || len(AutoTunerRunChan) != 1 {
		t.Fatalf("running and enabled: %d %s (queued %d)", rec.Code, rec.Body.String(), len(AutoTunerRunChan))
	}
	drain()
}
