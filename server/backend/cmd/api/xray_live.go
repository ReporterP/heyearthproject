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
	"time"

	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────── Live (no-restart) user changes via the xray API ───────────────
// xraydiff.UserOnly diffs are pushed into the running xray through api-in
// (127.0.0.1:10085) with the xray CLI (Xray-core 26.3.27):
//   rmu  → HandlerService.AlterInbound(RemoveUser), one call per inbound
//   adu  → HandlerService.AlterInbound(AddUser), one call for all inbounds
//   adrules WITHOUT -append → RoutingService.AddRule: replaces ALL rules+balancers
//   lsrules / inboundusercount → verification
// Any error or mismatch is returned and SyncXrayConfig falls back to restart.

// xrayRuntimeStale: the running xray may differ from the config file (failed API
// apply / restart / rename) → next SyncXrayConfig restarts unconditionally.
// Guarded by xraySyncMu. In-memory only: an API process restart forgets it.
var xrayRuntimeStale bool

// xrayLiveAlertKey — alert "a user-only change could not be applied live".
const xrayLiveAlertKey = "xray-live-apply"

// truncateAlertDetails caps CLI output quoted in an alert (Telegram: 4096 chars).
func truncateAlertDetails(s string) string {
	r := []rune(s)
	if len(r) > xraySyncAlertDetailsMax {
		return string(r[:xraySyncAlertDetailsMax]) + "…"
	}
	return s
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
// runs `xray api <sub> [flags] <file>` and removes the file.
func runXrayAPIWithFile(xrayBin, sub string, payload []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(xrayConfigPath), "live-api-*.json")
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
	return runXrayAPI(xrayBin, sub, f.Name())
}

// applyUserOnlyDiff applies a UserOnly diff to the running xray and verifies it.
func applyUserOnlyDiff(xrayBin string, d xraydiff.Result) error {
	// 1. Remove deleted/modified users ("--" ends flag parsing before emails).
	for _, ic := range d.Inbounds {
		if len(ic.Removed) == 0 {
			continue
		}
		out, err := runXrayAPI(xrayBin, "rmu", append([]string{"-tag=" + ic.Tag, "--"}, ic.Removed...)...)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseRemovedTotal(out); n != len(ic.Removed) {
			return fmt.Errorf("rmu %s: removed %d of %d: %s", ic.Tag, n, len(ic.Removed), strings.TrimSpace(out))
		}
	}
	// 2. Add new/modified users: one adu call, one full inbound object per tag.
	payload, total, err := d.AddUsersPayload()
	if err != nil {
		return fmt.Errorf("adu payload: %w", err)
	}
	if total > 0 {
		out, err := runXrayAPIWithFile(xrayBin, "adu", payload)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseAddedTotal(out); n != total {
			return fmt.Errorf("adu: added %d of %d: %s", n, total, strings.TrimSpace(out))
		}
	}
	// 3. Routing: replace all rules + balancers, then live order must equal config order.
	if d.RoutingChanged {
		rp, err := d.RoutingPayload()
		if err != nil {
			return err
		}
		if _, err := runXrayAPIWithFile(xrayBin, "adrules", rp); err != nil {
			return err
		}
		out, err := runXrayAPI(xrayBin, "lsrules")
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
	// 4. Every inbound with clients must hold exactly the configured number of users.
	tags := make([]string, 0, len(d.UserCounts))
	for tag := range d.UserCounts {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		out, err := runXrayAPI(xrayBin, "inboundusercount", "-tag="+tag)
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
}

// apply compares newJSON with the running config (canonically, xraydiff) and
// either does nothing, applies a user-only diff live, or renames + restarts.
//
// *stale is xrayRuntimeStale. It is set by a failed live apply, a failed rename
// after a successful live apply and a failed restart, and cleared only by a
// successful restart. While it is set the runtime may differ from the file, so
// the config is installed and xray restarted even if the file is unchanged.
//
// liveErr reports a failed live apply that fell back to the restart path (the
// caller alerts on it); err is the error of the whole apply.
func (a xrayApplier) apply(newJSON []byte, stale *bool) (action xrayApplyAction, diff xraydiff.Result, liveErr, err error) {
	if *stale {
		log.Println("[xray-sync] runtime stale (previous apply or restart failed) — restart")
	} else if running, rerr := a.readRunning(); rerr != nil {
		log.Printf("[xray-sync] read running config: %v — restart", rerr)
	} else {
		d, derr := xraydiff.Classify(running, newJSON)
		switch {
		case derr != nil:
			log.Printf("[xray-sync] diff error: %v — restart", derr)
		case d.Kind == xraydiff.None:
			a.discard()
			return xrayApplyUnchanged, d, nil, nil
		case d.Kind == xraydiff.UserOnly:
			if liveErr = a.applyLive(d); liveErr != nil {
				*stale = true // partially applied: the restart below heals it
				log.Printf("[xray-sync] user-only apply via API failed: %v — fallback to restart", liveErr)
				break
			}
			if ierr := a.install(); ierr != nil {
				a.discard()
				*stale = true // runtime has the new users, the file does not
				return xrayApplyLive, d, nil, fmt.Errorf("rename xray config after API apply: %w", ierr)
			}
			return xrayApplyLive, d, nil, nil
		default:
			log.Printf("[xray-sync] structural change (%s) — restart", d.Reason)
		}
		diff = d
	}

	// Restart path: structural change, failed live apply, stale runtime.
	if ierr := a.install(); ierr != nil {
		a.discard()
		return xrayApplyRestart, diff, liveErr, fmt.Errorf("rename xray config: %w", ierr)
	}
	if rerr := a.restart(); rerr != nil {
		*stale = true // the new file is in place but xray may still run the old config
		return xrayApplyRestart, diff, liveErr, rerr
	}
	*stale = false
	return xrayApplyRestart, diff, liveErr, nil
}
