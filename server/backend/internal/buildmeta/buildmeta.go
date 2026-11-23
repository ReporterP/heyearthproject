// Package buildmeta labels a build of the API with the commit it was made from.
// Both deploy tools (cmd/deploy and quick_deploy.go) pass Ldflags to `go build`;
// cmd/api reads the values from its main.buildSHA and main.buildTime variables.
package buildmeta

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// unknownSHA labels a build whose commit could not be read.
const unknownSHA = "unknown"

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// Ldflags returns the -ldflags value that stamps the commit and the build time
// (UTC, RFC 3339) into cmd/api: sha plus "-dirty" for an uncommitted tree.
func Ldflags(sha string, dirty bool, now time.Time) string {
	if !shaRe.MatchString(sha) {
		sha = unknownSHA
	} else if dirty {
		sha += "-dirty"
	}
	return fmt.Sprintf("-X main.buildSHA=%s -X main.buildTime=%s", sha, now.UTC().Format(time.RFC3339))
}

// GitState reads the commit of the repository holding dir and whether its work
// tree has uncommitted or untracked files. Without git, or outside a repository,
// the commit is empty (Ldflags labels such a build "unknown").
func GitState(dir string) (sha string, dirty bool, err error) {
	rev, err := gitOutput(dir, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "", false, err
	}
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return "", false, err
	}
	sha, dirty = parseGitState(rev, status)
	return sha, dirty, nil
}

func parseGitState(revParse, statusPorcelain string) (sha string, dirty bool) {
	return strings.TrimSpace(revParse), strings.TrimSpace(statusPorcelain) != ""
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// BuildArgs is the `go build` argument list stamping the current commit of dir:
// ["-ldflags", "<Ldflags>"]. A git failure is reported and the build is labelled
// "unknown" — a deploy never fails because of the label.
func BuildArgs(dir string, now time.Time) (args []string, label string, err error) {
	sha, dirty, err := GitState(dir)
	flags := Ldflags(sha, dirty, now)
	label = strings.TrimPrefix(strings.Fields(flags)[1], "main.buildSHA=")
	return []string{"-ldflags", flags}, label, err
}
