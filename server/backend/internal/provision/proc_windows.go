//go:build windows

package provision

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// prepareLocalCmd: Windows is only the developer's machine running the tests under Git Bash,
// whose exec runs the script in a child of the bash process — kill the whole tree.
func prepareLocalCmd(c *exec.Cmd) {
	c.Cancel = func() error {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(c.Process.Pid)).Run()
		return c.Process.Kill()
	}
}

// localShellPath is the path as Git Bash sees it: C:\x\y → /c/x/y.
func localShellPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}
