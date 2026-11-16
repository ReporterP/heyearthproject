package biz

import (
	"sync"
	"time"
)

// Path states of an exit as the tunnel monitor (cmd/api) judges them.
const (
	PathHealthy   = "healthy"
	PathUnhealthy = "unhealthy"
	PathUnknown   = "unknown"
	PathAbsent    = "absent" // the exit has no such path (protocol / credentials)
)

// ExitPaths is the monitor's last view of one online exit's two paths: Hysteria (hy, the exit's
// hysteria2-client SOCKS) and TCP (vr, the backup: ShadowTLS v3 through the Hub's
// heyearth-stls-client SOCKS; VLESS Reality for a legacy VLESS exit). In memory only:
// empty after an API restart until the first monitor tick (~10 s), dropped once the exit is no
// longer online. Errors name loopback addresses only, never credentials.
type ExitPaths struct {
	Hy        string    `json:"hy"`
	VR        string    `json:"vr"`
	HyRTTms   int       `json:"hy_rtt_ms,omitempty"`
	VRRTTms   int       `json:"vr_rtt_ms,omitempty"`
	HyError   string    `json:"hy_error,omitempty"`
	VRError   string    `json:"vr_error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

var exitPaths sync.Map // server id → ExitPaths

// SetExitPaths records the monitor's view of exit id.
func SetExitPaths(id int, p ExitPaths) { exitPaths.Store(id, p) }

// GetExitPaths returns the last view of exit id; false before the monitor has seen it.
func GetExitPaths(id int) (ExitPaths, bool) {
	v, ok := exitPaths.Load(id)
	if !ok {
		return ExitPaths{}, false
	}
	return v.(ExitPaths), true
}

// ForgetExitPaths drops the view of exit id: the monitor calls it once the exit has left the
// online list, so a stale view never stands for a deprovisioned or re-provisioned exit.
func ForgetExitPaths(id int) { exitPaths.Delete(id) }
