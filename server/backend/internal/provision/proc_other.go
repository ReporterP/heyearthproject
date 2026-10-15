//go:build !unix && !windows

package provision

import "os/exec"

// prepareLocalCmd: no process groups here (plan9, js, wasip1 — never the Hub); the default
// cancel kills bash only.
func prepareLocalCmd(*exec.Cmd) {}

// localShellPath is the path as the local bash sees it.
func localShellPath(p string) string { return p }
