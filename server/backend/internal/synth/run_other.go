//go:build !unix

package synth

import (
	"os"
	"os/exec"
)

// The synthetic client runs on the Hub (Linux). On a developer's machine the core is simply
// killed, and stale cores are never hunted by pid.

func ownProcessGroup(*exec.Cmd) {}

func killProcess(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func runsFrom(int, string) bool { return false }
