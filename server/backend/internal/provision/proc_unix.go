//go:build !windows

package provision

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// prepareLocalCmd puts the local bash into its own process group and makes a cancelled Run kill
// the whole group: the script and everything it started (a step cancelled on the Hub must not
// keep writing while Rollback or Resume already run).
func prepareLocalCmd(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}

// localShellPath is the path as the local bash sees it.
func localShellPath(p string) string { return p }
