//go:build unix

package synth

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// ownProcessGroup puts the core into its own process group, so killProcess takes everything
// it may have started with it.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcess ends pid and its process group at once (SIGKILL: a frozen core does not get to
// refuse).
func killProcess(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// runsFrom reports whether pid is a live process started with a path inside dir (Linux: its
// command line). Anything else is not ours.
func runsFrom(pid int, dir string) bool {
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	return bytes.Contains(cmdline, []byte(dir))
}
