// Package deployguard decides whether a deploy may go ahead (docs/prd/stability-operations.prd.md,
// part 4 «Защита выкатки») and keeps the Hub's side of it: a lock, the record of what is deployed,
// and the deploy's line in the ops feed. Used by quick_deploy.go, quick_admin_web.go and cmd/deploy.
//
// A deploy is refused when the work tree is dirty, the commit is not on main, the commit does not
// contain the deployed one (it would roll back someone else's fix) or another deploy holds the
// lock. Nothing here locks a deploy out for good: a lock older than StaleLock, an unknown deployed
// build or a git error are warnings, and --force-deploy "<reason>" turns refusals into warnings.
package deployguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/buildmeta"
)

// Paths on the Hub.
const (
	LockDir       = "/opt/heyearth/state/deploy.lock.d" // created with mkdir (atomic); holds the file holder
	DeployedFile  = "/opt/heyearth/state/deployed.json" // {"api":{"sha","at"},"admin":{…},"site":{…}}
	IntakeKeyFile = "/opt/heyearth/state/ops-intake.key"
	IntakeURL     = "http://127.0.0.1:8001/internal/ops/event"
)

// StaleLock: a lock older than this was left by a dropped session; it is taken over with a warning.
const StaleLock = 30 * time.Minute

// MinForceReason is the shortest reason --force-deploy accepts.
const MinForceReason = 10

// Local is the state of the commit being deployed.
type Local struct {
	SHA    string // short SHA of HEAD
	Dirty  bool   // git status --porcelain is not empty
	OnMain bool   // HEAD is reachable from main
	// Contains tells whether HEAD contains the commit sha (git merge-base --is-ancestor).
	Contains func(sha string) (bool, error)
}

// Remote is what the Hub says.
type Remote struct {
	DeployedSHA string // "" or "dev" or "unknown" — not known
	LockHolder  string // "" — no lock
	LockAge     time.Duration
}

// Verdict lists why the deploy stops (Refusals) and what the owner should know (Warnings).
type Verdict struct{ Refusals, Warnings []string }

// CheckLocal is the part of Check decided before any connection to the Hub.
func CheckLocal(l Local) Verdict {
	var v Verdict
	if l.Dirty {
		v.Refusals = append(v.Refusals, "рабочее дерево не чистое: выкатывать можно только закоммиченное")
	}
	if !l.OnMain {
		v.Refusals = append(v.Refusals, "коммит не из main: выкатывать можно только коммит ветки main")
	}
	return v
}

// Check decides whether a deploy of l may go ahead over what the Hub reports in r.
func Check(l Local, r Remote) Verdict {
	v := CheckLocal(l)
	if r.LockHolder != "" {
		if r.LockAge < StaleLock {
			v.Refusals = append(v.Refusals, fmt.Sprintf("идёт другая выкатка: замок держит %s (%s назад)", r.LockHolder, r.LockAge.Round(time.Second)))
		} else {
			v.Warnings = append(v.Warnings, fmt.Sprintf("замок выкатки старше %s (держит %s) — оставлен оборванной сессией, снимается", StaleLock, r.LockHolder))
		}
	}
	sha := strings.TrimSpace(r.DeployedSHA)
	if base, dirty := strings.CutSuffix(sha, "-dirty"); dirty {
		sha = base
		v.Warnings = append(v.Warnings, "на хабе сборка из грязного дерева ("+r.DeployedSHA+"): сверка идёт по её коммиту")
	}
	switch {
	case sha == "" || sha == "dev" || sha == "unknown":
		v.Warnings = append(v.Warnings, "выкаченная сборка неизвестна: не проверить, что новая её содержит")
	case l.Contains == nil:
		v.Warnings = append(v.Warnings, "не проверить, содержит ли коммит выкаченную сборку "+sha)
	default:
		ok, err := l.Contains(sha)
		switch {
		case err != nil:
			v.Warnings = append(v.Warnings, fmt.Sprintf("не проверить, содержит ли %s выкаченную сборку %s: %v", l.SHA, sha, err))
		case !ok:
			v.Refusals = append(v.Refusals, fmt.Sprintf("коммит %s не содержит выкаченную сборку %s: выкатка откатит чужую правку — сначала влейте её в main", l.SHA, sha))
		}
	}
	return v
}

// Force turns the refusals into warnings when a reason of MinForceReason characters is given.
func (v Verdict) Force(reason string) (Verdict, error) {
	if len([]rune(strings.TrimSpace(reason))) < MinForceReason {
		return v, fmt.Errorf("--force-deploy: причина не короче %d символов", MinForceReason)
	}
	out := Verdict{Warnings: append([]string{}, v.Warnings...)}
	for _, r := range v.Refusals {
		out.Warnings = append(out.Warnings, "обойдено (--force-deploy): "+r)
	}
	return out, nil
}

// LocalState reads the commit of the repository holding dir: its short SHA, whether the tree is
// dirty, whether it is on main, and a Contains that asks git.
func LocalState(dir string) Local {
	sha, dirty, _ := buildmeta.GitState(dir)
	l := Local{SHA: sha, Dirty: dirty}
	l.OnMain = exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", "HEAD", "main").Run() == nil
	l.Contains = func(other string) (bool, error) {
		err := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", other, "HEAD").Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			return true, nil
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return false, nil
		default:
			return false, fmt.Errorf("git merge-base %s: %w", other, err)
		}
	}
	return l
}

// Run executes a shell command on the Hub as root, stdin may be nil.
type Run func(cmd string, stdin []byte) (string, error)

// ReadRemoteCmd prints what ParseRemote reads: deployed.json, the lock and the API's last
// «[boot] сборка» journal line.
func ReadRemoteCmd() string {
	return `printf 'DEPLOYED '; cat ` + DeployedFile + ` 2>/dev/null | tr -d '\n'; echo
if [ -d ` + LockDir + ` ]; then printf 'LOCK '; head -c 200 ` + LockDir + `/holder 2>/dev/null | tr -d '\n'; echo; echo "LOCKAGE $(( $(date +%s) - $(stat -c %Y ` + LockDir + `) ))"; fi
printf 'BOOT '; journalctl -u heyearth-api --no-pager -n 3000 2>/dev/null | grep -F '[boot] сборка' | tail -n 1`
}

// ParseRemote reads the output of ReadRemoteCmd for one component (api | admin | site); it also
// returns deployed.json as it is on the Hub (for MergeDeployed).
func ParseRemote(component, out string, now time.Time) (Remote, []byte) {
	var r Remote
	var doc []byte
	var boot string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "DEPLOYED "):
			doc = []byte(strings.TrimSpace(strings.TrimPrefix(line, "DEPLOYED ")))
		case strings.HasPrefix(line, "LOCK "):
			r.LockHolder = strings.TrimSpace(strings.TrimPrefix(line, "LOCK "))
			if r.LockHolder == "" {
				r.LockHolder = "неизвестно кто"
			}
		case strings.HasPrefix(line, "LOCKAGE "):
			if s, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "LOCKAGE "))); err == nil {
				r.LockAge = time.Duration(s) * time.Second
			}
		case strings.HasPrefix(line, "BOOT "):
			boot = line
		}
	}
	var m map[string]struct{ SHA string }
	if len(doc) > 0 && json.Unmarshal(doc, &m) == nil {
		r.DeployedSHA = m[component].SHA
	}
	if r.DeployedSHA == "" && component == "api" {
		if _, rest, ok := strings.Cut(boot, "[boot] сборка "); ok {
			r.DeployedSHA = strings.Fields(rest + " ")[0]
		}
	}
	_ = now
	return r, doc
}

// MergeDeployed writes component = {sha, at} into deployed.json, keeping the other components.
// A file that cannot be read is replaced: a deploy never fails because of its own record.
func MergeDeployed(prev []byte, component, sha string, at time.Time) ([]byte, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(prev)) > 0 {
		if err := json.Unmarshal(prev, &m); err != nil {
			m = map[string]any{}
		}
	}
	m[component] = map[string]string{"sha": sha, "at": at.UTC().Format(time.RFC3339)}
	return json.Marshal(m)
}

// Lock takes the deploy lock for holder; takeOver removes a lock left behind (Check said stale).
func Lock(run Run, holder string, takeOver bool) error {
	cmd := ""
	if takeOver {
		cmd = "rm -rf " + LockDir + "; "
	}
	cmd += "mkdir -p $(dirname " + LockDir + ") && if mkdir " + LockDir + " 2>/dev/null; then cat > " + LockDir + "/holder && echo LOCKED; else echo BUSY; fi"
	out, err := run(cmd, []byte(holder))
	if strings.Contains(out, "LOCKED") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("замок выкатки: %v %s", err, strings.TrimSpace(out))
	}
	return errors.New("замок выкатки занят: другая выкатка началась только что")
}

// Unlock releases the deploy lock.
func Unlock(run Run) error {
	_, err := run("rm -rf "+LockDir, nil)
	return err
}

// Record stores the deployed build of component in deployed.json (temp file + mv).
func Record(run Run, prev []byte, component, sha string, at time.Time) error {
	doc, err := MergeDeployed(prev, component, sha, at)
	if err != nil {
		return err
	}
	out, err := run("cat > "+DeployedFile+".tmp && mv -f "+DeployedFile+".tmp "+DeployedFile+" && echo WRITTEN", doc)
	if !strings.Contains(out, "WRITTEN") {
		return fmt.Errorf("deployed.json: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Event is one line of the ops feed sent by a deploy tool (POST /internal/ops/event).
type Event struct {
	Kind    string `json:"kind"`   // action | maintenance | note
	Actor   string `json:"actor"`  // deploy
	Target  string `json:"target"` // api | admin | site | hub
	Summary string `json:"summary"`
	Impact  string `json:"impact"` // K0–K3
	OK      bool   `json:"ok"`
}

// Feed sends e to the API's loopback intake. The key is read on the Hub by the command itself
// (a builtin printf, process substitution): it is never an argument of a process nor in the
// tool's output.
func Feed(run Run, e Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	cmd := `bash -c 'k=$(cat ` + IntakeKeyFile + ` 2>/dev/null) || exit 7; curl -fsS -m 10 -o /dev/null -w "%{http_code}" -H @<(printf "X-Ops-Key: %s\n" "$k") -H "Content-Type: application/json" --data-binary @- ` + IntakeURL + `'`
	out, err := run(cmd, body)
	if err != nil || strings.TrimSpace(out) != "200" {
		return fmt.Errorf("запись в ленту: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}
