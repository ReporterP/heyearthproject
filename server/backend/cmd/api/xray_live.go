package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────── Live (no-restart) user changes via the xray API ───────────────
// xraydiff.UserOnly diffs are pushed into the running xray through api-in
// (127.0.0.1:10085) with the xray CLI (Xray-core 26.3.27):
//   rmu  → HandlerService.AlterInbound(RemoveUser), one call per inbound
//   adrules WITHOUT -append → RoutingService.AddRule: replaces ALL rules+balancers
//   adu  → HandlerService.AlterInbound(AddUser), one call for all inbounds
//   lsrules / inboundusercount → verification
// Any error or mismatch is returned and SyncXrayConfig falls back to restart.

const (
	// xrayUnitBinary is the binary the systemd unit runs (ExecStart). The CLI
	// output this file parses was verified on that binary (26.3.27) only.
	xrayUnitBinary = "/opt/heyearth/xray/xray"
	// xrayStaleMarkerPath persists "the running xray may differ from config.json"
	// across API restarts (see xrayRuntimeState).
	xrayStaleMarkerPath = "/opt/heyearth/state/xray-runtime-stale"
	// xrayLiveTempPattern names the payload files of runXrayAPIWithFile; they may
	// hold the Reality private key (adu carries the full inbound).
	xrayLiveTempPattern = "live-api-*.json"
	// xrayLiveAlertKey — alert "a user-only change could not be applied live".
	xrayLiveAlertKey = "xray-live-apply"
)

// xrayBinary returns the binary for `xray run -test` and `xray api`: the one the
// systemd unit runs; LookPath only when it is absent.
func xrayBinary() string {
	if _, err := os.Stat(xrayUnitBinary); err == nil {
		return xrayUnitBinary
	}
	if p, err := exec.LookPath("xray"); err == nil {
		return p
	}
	return xrayUnitBinary
}

// truncateAlertDetails caps CLI output quoted in an alert (Telegram: 4096 chars).
func truncateAlertDetails(s string) string {
	r := []rune(s)
	if len(r) > xraySyncAlertDetailsMax {
		return string(r[:xraySyncAlertDetailsMax]) + "…"
	}
	return s
}

// ─────────────── Runtime state: may the running xray differ from config.json? ───────────────

// xrayRuntimeState is the stale flag of the running xray. stale (in memory) is a
// cache of the marker file:
//   - begin writes the marker BEFORE the runtime is touched (live apply or
//     rename + restart), so an API process dying midway leaves it behind;
//   - markStale / markStaleCause set the flag (+ marker): failed live apply,
//     rename or restart, or a revoked user still online — the next sync
//     restarts unconditionally; the cause labels that restart (logs and the
//     restart-storm alert, xray_restarts.go);
//   - liveApplied removes the marker after a fully verified live apply, unless
//     the runtime was marked stale meanwhile; untouched does the same when the
//     rename before a restart failed (file and runtime unchanged);
//   - restarted clears both: the runtime equals the file again;
//   - load (startup) sets the flag when a previous process left the marker.
type xrayRuntimeState struct {
	mu     sync.Mutex
	stale  bool
	cause  string // short label of what made it stale (latest wins); "" = unlabeled
	marker string // "" = memory only
}

// Restart causes set by the applier (see also revokedRestartCause).
const (
	xrayCauseLiveFailed    = "live-apply-failed"
	xrayCauseRenameFailed  = "rename-failed"
	xrayCauseRestartFailed = "restart-failed"
	xrayCausePrevProcess   = "previous-process"
	xrayCauseStale         = "stale"
)

var xrayStale = &xrayRuntimeState{marker: xrayStaleMarkerPath}

func (s *xrayRuntimeState) load() bool {
	if s.marker == "" {
		return false
	}
	if _, err := os.Stat(s.marker); err != nil {
		return false
	}
	s.mu.Lock()
	s.stale, s.cause = true, xrayCausePrevProcess
	s.mu.Unlock()
	return true
}

func (s *xrayRuntimeState) isStale() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stale
}

func (s *xrayRuntimeState) begin(what string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeMarkerLocked("in progress: " + what)
}

func (s *xrayRuntimeState) markStale(reason string) { s.markStaleCause("", reason) }

// markStaleCause is markStale with a short cause label for the restart it forces.
func (s *xrayRuntimeState) markStaleCause(cause, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale = true
	if cause != "" {
		s.cause = cause
	}
	s.writeMarkerLocked("stale: " + reason)
}

// staleCause is the label of the restart a stale runtime forces.
func (s *xrayRuntimeState) staleCause() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cause == "" {
		return xrayCauseStale
	}
	return s.cause
}

func (s *xrayRuntimeState) liveApplied() { s.settledUnlessStale() }

// untouched: begin was called but the runtime was not changed (rename failed
// before the restart) — same as a verified live apply for the marker.
func (s *xrayRuntimeState) untouched() { s.settledUnlessStale() }

func (s *xrayRuntimeState) settledUnlessStale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stale {
		s.removeMarkerLocked()
	}
}

func (s *xrayRuntimeState) restarted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale, s.cause = false, ""
	s.removeMarkerLocked()
}

func (s *xrayRuntimeState) writeMarkerLocked(reason string) {
	if s.marker == "" {
		return
	}
	line := time.Now().Format(time.RFC3339) + " " + reason + "\n"
	if err := os.WriteFile(s.marker, []byte(line), 0o600); err != nil {
		log.Printf("[xray-sync] WARN: stale marker %s: %v (state kept in memory only)", s.marker, err)
	}
}

func (s *xrayRuntimeState) removeMarkerLocked() {
	if s.marker == "" {
		return
	}
	if err := os.Remove(s.marker); err != nil && !os.IsNotExist(err) {
		log.Printf("[xray-sync] WARN: remove stale marker %s: %v", s.marker, err)
	}
}

// prepareXrayLiveApply runs once at API startup, before the first sync: a stale
// marker left by the previous process forces a restart; leftover payload files
// (may contain the Reality private key) are removed.
func prepareXrayLiveApply() {
	if xrayStale.load() {
		log.Printf("[xray-sync] stale marker %s found — first sync restarts xray", xrayStaleMarkerPath)
	}
	if n, err := revokedUsers.load(); err != nil {
		log.Printf("[xray-live] WARN: список отозванных %s не прочитан: %v (наблюдение начинается заново)", revokedUsers.path, err)
	} else if n > 0 {
		log.Printf("[xray-live] восстановлено наблюдение за %d отозванными пользователями из %s", n, revokedUsers.path)
	}
	if n := cleanupXrayLiveTempFiles(filepath.Dir(xrayConfigPath)); n > 0 {
		log.Printf("[xray-sync] removed %d leftover %s file(s)", n, xrayLiveTempPattern)
	}
}

func cleanupXrayLiveTempFiles(dir string) int {
	files, _ := filepath.Glob(filepath.Join(dir, xrayLiveTempPattern))
	n := 0
	for _, f := range files {
		if err := os.Remove(f); err == nil {
			n++
		} else {
			log.Printf("[xray-sync] WARN: remove %s: %v", f, err)
		}
	}
	return n
}

// ─────────────── xray api CLI ───────────────

// xrayCLI runs one `xray api <sub> <args>` call; a non-nil payload is written to
// a temp file whose path is appended as the last argument. Injectable for tests.
type xrayCLI func(sub string, args []string, payload []byte) (string, error)

func xrayCLIRunner(xrayBin string) xrayCLI {
	return func(sub string, args []string, payload []byte) (string, error) {
		if payload == nil {
			return runXrayAPI(xrayBin, sub, args...)
		}
		return runXrayAPIWithFile(xrayBin, sub, payload, args...)
	}
}

// runXrayAPI runs `xray api <sub> --server=127.0.0.1:10085 -timeout=10 <args>` and
// returns stdout. xray exits 1 (base.Fatalf) on dial/decode/build errors.
func runXrayAPI(xrayBin, sub string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := append([]string{"api", sub, "--server=" + xrayStatsAPIAddr, "-timeout=10"}, args...)
	cmd := exec.CommandContext(ctx, xrayBin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("xray api %s: %w (%s %s)", sub, err,
			strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
	}
	return stdout.String(), nil
}

// runXrayAPIWithFile writes payload to a 0600 temp file next to the config,
// runs `xray api <sub> <args> <file>` and removes the file.
func runXrayAPIWithFile(xrayBin, sub string, payload []byte, args ...string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(xrayConfigPath), xrayLiveTempPattern)
	if err != nil {
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	return runXrayAPI(xrayBin, sub, append(append([]string{}, args...), f.Name())...)
}

// applyUserOnlyDiff applies a UserOnly diff to the running xray and verifies it.
// Order: rmu → adrules → adu, so a new user exists in its plan rule before it
// can connect (no detour via the catch-all) and a revoked one is refused first.
func applyUserOnlyDiff(run xrayCLI, d xraydiff.Result) error {
	// 1. Remove deleted/modified users ("--" ends flag parsing before emails).
	for _, ic := range d.Inbounds {
		if len(ic.Removed) == 0 {
			continue
		}
		out, err := run("rmu", append([]string{"-tag=" + ic.Tag, "--"}, ic.Removed...), nil)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseRemovedTotal(out); n != len(ic.Removed) {
			return fmt.Errorf("rmu %s: removed %d of %d: %s", ic.Tag, n, len(ic.Removed), strings.TrimSpace(out))
		}
	}
	// 2. Routing: replace all rules + balancers, then live order must equal config order.
	if d.RoutingChanged {
		rp, err := d.RoutingPayload()
		if err != nil {
			return err
		}
		if _, err := run("adrules", nil, rp); err != nil {
			return err
		}
		out, err := run("lsrules", nil, nil)
		if err != nil {
			return err
		}
		live, err := xraydiff.ParseRuleTags(out)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(live, d.RuleTags) {
			return fmt.Errorf("lsrules order mismatch: live %v, want %v", live, d.RuleTags)
		}
	}
	// 3. Add new/modified users: one adu call, one full inbound object per tag.
	payload, total, err := d.AddUsersPayload()
	if err != nil {
		return fmt.Errorf("adu payload: %w", err)
	}
	if total > 0 {
		out, err := run("adu", nil, payload)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseAddedTotal(out); n != total {
			return fmt.Errorf("adu: added %d of %d: %s", n, total, strings.TrimSpace(out))
		}
	}
	// 4. Every inbound with clients must hold exactly the configured number of users.
	tags := make([]string, 0, len(d.UserCounts))
	for tag := range d.UserCounts {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		out, err := run("inboundusercount", []string{"-tag=" + tag}, nil)
		if err != nil {
			return err
		}
		got, err := xraydiff.ParseUserCount(out)
		if err != nil {
			return err
		}
		if got != d.UserCounts[tag] {
			return fmt.Errorf("inboundusercount %s = %d, want %d", tag, got, d.UserCounts[tag])
		}
	}
	return nil
}

// restartXray runs `systemctl restart xray` (Xray-core has no reload).
func restartXray() error {
	if output, err := exec.Command("systemctl", "restart", "xray").CombinedOutput(); err != nil {
		return fmt.Errorf("restart xray: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ─────────────── Applying a validated config: unchanged / live / restart ───────────────

// xrayApplyAction is how a validated config was put into service.
type xrayApplyAction int

const (
	xrayApplyUnchanged xrayApplyAction = iota // the running xray already matches; tmp discarded
	xrayApplyLive                             // user-only diff pushed via the API, file renamed, no restart
	xrayApplyRestart                          // file renamed + systemctl restart xray
)

// xrayApplier holds the side effects of applying a config that is already
// written to the tmp file and passed `xray run -test` (and the leak guard).
// Injectable for tests.
type xrayApplier struct {
	readRunning func() ([]byte, error)      // the config file the running xray matches
	applyLive   func(xraydiff.Result) error // push a UserOnly diff (applyUserOnlyDiff)
	install     func() error                // atomic rename tmp → config path
	discard     func()                      // remove the tmp file
	restart     func() error                // systemctl restart xray
	onRestart   func(cause string)          // after a successful restart, before the stale state is cleared; may be nil
}

// apply compares newJSON with the running config (canonically, xraydiff) and
// either does nothing, applies a user-only diff live, or renames + restarts.
// While st is stale the runtime may differ from the file, so the config is
// installed and xray restarted even if the file is unchanged (see
// xrayRuntimeState for when it is set and cleared).
//
// liveErr reports a failed live apply that fell back to the restart path (the
// caller alerts on it); err is the error of the whole apply. onRestart gets the
// cause of a restart: the stale cause, "structural (<first difference>)",
// live-apply-failed, unreadable-config or diff-error.
func (a xrayApplier) apply(newJSON []byte, st *xrayRuntimeState) (action xrayApplyAction, diff xraydiff.Result, liveErr, err error) {
	var cause string
	if st.isStale() {
		cause = st.staleCause()
		log.Printf("[xray-sync] runtime stale (%s: previous apply or restart failed, or revoked user online) — restart", cause)
	} else if running, rerr := a.readRunning(); rerr != nil {
		cause = "unreadable-config"
		log.Printf("[xray-sync] read running config: %v — restart", rerr)
	} else {
		d, derr := xraydiff.Classify(running, newJSON)
		switch {
		case derr != nil:
			cause = "diff-error"
			log.Printf("[xray-sync] diff error: %v — restart", derr)
		case d.Kind == xraydiff.None:
			a.discard()
			return xrayApplyUnchanged, d, nil, nil
		case d.Kind == xraydiff.UserOnly:
			st.begin("live apply")
			if liveErr = a.applyLive(d); liveErr != nil {
				// partially applied: the restart below heals it
				st.markStaleCause(xrayCauseLiveFailed, "live apply failed: "+liveErr.Error())
				cause = xrayCauseLiveFailed
				log.Printf("[xray-sync] user-only apply via API failed: %v — fallback to restart", liveErr)
				break
			}
			if ierr := a.install(); ierr != nil {
				a.discard()
				// runtime has the new users, the file does not
				st.markStaleCause(xrayCauseRenameFailed, "rename after live apply failed")
				return xrayApplyLive, d, nil, fmt.Errorf("rename xray config after API apply: %w", ierr)
			}
			st.liveApplied()
			return xrayApplyLive, d, nil, nil
		default:
			cause = "structural (" + d.Reason + ")"
			log.Printf("[xray-sync] structural change (%s) — restart", d.Reason)
		}
		diff = d
	}

	// Restart path: structural change, failed live apply, stale runtime.
	st.begin("restart")
	if ierr := a.install(); ierr != nil {
		a.discard()
		st.untouched() // file and runtime unchanged: drop the marker unless already stale
		return xrayApplyRestart, diff, liveErr, fmt.Errorf("rename xray config: %w", ierr)
	}
	if rerr := a.restart(); rerr != nil {
		// the new file is in place but xray may still run the old config
		st.markStaleCause(xrayCauseRestartFailed, "restart failed")
		return xrayApplyRestart, diff, liveErr, rerr
	}
	if a.onRestart != nil {
		a.onRestart(cause)
	}
	st.restarted()
	return xrayApplyRestart, diff, liveErr, nil
}
