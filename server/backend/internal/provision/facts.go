package provision

// Jobs of the console's «Работы» (plan docs/plan/stability-operations-2.md, C5/C6):
//
//	facts         read-only: kernel, boot time, reboot-required, pending security updates (apt-get -s),
//	              sshd password login, root disk use — in Verify, so nothing is ever applied
//	reboot        `systemctl reboot` 5 s later through systemd-run (the SSH session ends first);
//	              the console's node.reboot waits for the node and runs verify
//	agentrestart  restart node-agent on an exit (rule A12), then check it is active

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KindFacts        = "facts"
	KindReboot       = "reboot"
	KindAgentRestart = "agentrestart"
)

// NodeFacts is what a facts job read on the node.
type NodeFacts struct {
	Kernel          string
	BootAt          time.Time
	RebootRequired  bool
	SecurityUpdates int
	SSHPassword     bool
	DiskPct         int
}

// FactsOptions: where a facts job puts what it read (the API's node_facts).
type FactsOptions struct {
	Record func(NodeFacts) error
}

// FactsScript only reads; one KEY=VALUE per line.
const FactsScript = `echo "KERNEL=$(uname -r)"
echo "BOOT=$(awk '/^btime /{print $2}' /proc/stat)"
if [ -f /var/run/reboot-required ]; then echo REBOOT=1; else echo REBOOT=0; fi
SEC=$(apt-get -s -o Debug::NoLocking=1 upgrade 2>/dev/null | grep -c '^Inst .*[Ss]ecurity' || true)
echo "SECURITY=${SEC:-0}"
PW=$(sshd -T 2>/dev/null | awk '/^passwordauthentication /{print $2}')
if [ "$PW" = yes ]; then echo SSHPW=1; elif [ "$PW" = no ]; then echo SSHPW=0; fi
echo "DISK=$(df -P / | awk 'NR==2{gsub("%","",$5); print $5}')"
`

// ParseFacts reads FactsScript's output; anything missing or malformed is an error, never a zero.
func ParseFacts(out string) (NodeFacts, error) {
	kv := parseKV(out)
	var f NodeFacts
	var bad []string
	if f.Kernel = kv["KERNEL"]; f.Kernel == "" {
		bad = append(bad, "KERNEL")
	}
	if s, err := strconv.ParseInt(kv["BOOT"], 10, 64); err != nil || s <= 0 {
		bad = append(bad, "BOOT")
	} else {
		f.BootAt = time.Unix(s, 0)
	}
	flag := func(k string, dst *bool) {
		switch kv[k] {
		case "1":
			*dst = true
		case "0":
		default:
			bad = append(bad, k)
		}
	}
	flag("REBOOT", &f.RebootRequired)
	flag("SSHPW", &f.SSHPassword)
	if n, err := strconv.Atoi(kv["SECURITY"]); err != nil || n < 0 {
		bad = append(bad, "SECURITY")
	} else {
		f.SecurityUpdates = n
	}
	if n, err := strconv.Atoi(kv["DISK"]); err != nil || n < 0 || n > 100 {
		bad = append(bad, "DISK")
	} else {
		f.DiskPct = n
	}
	if len(bad) > 0 {
		return NodeFacts{}, fmt.Errorf("сведения узла не прочитаны: %s", strings.Join(bad, ", "))
	}
	return f, nil
}

func workingNode(n Node) error {
	switch {
	case n.Lifecycle != "online":
		return fmt.Errorf("нода в состоянии «%s»: только работающая", n.Lifecycle)
	case !n.HostKeyConfirmed:
		return ErrHostKeyNotConfirmed
	}
	return nil
}

func factsRecipe(n Node, st *State) (Recipe, error) {
	if err := workingNode(n); err != nil {
		return nil, err
	}
	if st.Facts == nil || st.Facts.Record == nil {
		return nil, errors.New("сведения некуда записать")
	}
	rec := st.Facts.Record
	return Recipe{{Name: "facts", Verify: func(ctx context.Context, r Remote, s *State) error {
		out, err := r.Run(ctx, FactsScript)
		if err != nil {
			return err
		}
		f, err := ParseFacts(out)
		if err != nil {
			return err
		}
		s.Printf("ядро %s, загружена %s, перезагрузка нужна: %v, обновлений безопасности: %d, диск %d %%",
			f.Kernel, f.BootAt.UTC().Format("02.01 15:04 UTC"), f.RebootRequired, f.SecurityUpdates, f.DiskPct)
		return rec(f)
	}}}, nil
}

// rebootCmd: the reboot starts 5 s after the command returns, so the job's SSH session ends cleanly.
const rebootCmd = `systemd-run --on-active=5 --unit=heyearth-console-reboot --description="console reboot" systemctl reboot`

func rebootRecipe(n Node) (Recipe, error) {
	if err := workingNode(n); err != nil {
		return nil, err
	}
	return Recipe{{Name: "reboot", Apply: func(ctx context.Context, r Remote, s *State) error {
		if out, err := r.Run(ctx, rebootCmd+" 2>&1"); err != nil {
			return fmt.Errorf("%w: перезагрузка не назначена: %w: %s", ErrNoRollback, err, tail(strings.TrimSpace(out), 300))
		}
		s.Printf("перезагрузка через 5 с")
		return nil
	}}}, nil
}

func agentRestartRecipe(n Node) (Recipe, error) {
	if n.Role != "exit" {
		return nil, fmt.Errorf("node-agent есть только на выходе, а нода — «%s»", n.Role)
	}
	if err := workingNode(n); err != nil {
		return nil, err
	}
	// No Verify: an active node-agent that does not answer (A12) must be restarted anyway.
	return Recipe{{Name: "agentrestart", Apply: func(ctx context.Context, r Remote, s *State) error {
		if out, err := r.Run(ctx, "systemctl restart node-agent 2>&1 && sleep 2 && systemctl is-active node-agent"); err != nil {
			return fmt.Errorf("%w: node-agent не поднялся: %w: %s", ErrNoRollback, err, tail(strings.TrimSpace(out), 300))
		}
		s.Printf("node-agent перезапущен и активен")
		return nil
	}}}, nil
}
