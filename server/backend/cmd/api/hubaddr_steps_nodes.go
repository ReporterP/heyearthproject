package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/hubnet"
	"github.com/heyearth/backend/internal/provision"
)

// Step 3 «Подготовить узлы» (additive): every online exit and entry is reprovisioned (the exit
// firewall and agent guard pick the new address up from hubOwnIPs; an entry's DNAT and stream
// stay on the current address), then the SSH allow for the new address is added next to the
// existing Hub-address SSH rules (the recipes never touch an SSH port that already has a rule),
// and SSH / node-agent are dialled from the new address.

const (
	hubNodeOK       = "ok"
	hubNodeFailed   = "failed"
	hubNodeNotReady = "not_ready"
	hubNodeExcluded = "excluded"

	hubJobPoll = 3 * time.Second
	hubJobCap  = 15 * time.Minute
)

type hubAddrNodeResult struct {
	ServerID int    `json:"server_id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Role     string `json:"role"`
	JobID    int64  `json:"job_id"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	SSHAllow string `json:"ssh_allow,omitempty"`
}

type hubAddrNodesChecks struct {
	Nodes    []hubAddrNodeResult `json:"nodes"`
	Excluded []int               `json:"excluded"`
}

// reprovisionNode starts a reprovision job and waits for it (polling, capped).
func (e *hubAddrEngine) reprovisionNode(ctx context.Context, id int) (int64, error) {
	return e.reprovisionNodeWith(ctx, id, nil)
}

// reprovisionNodeWith is reprovisionNode calling onPoll at every poll (an error stops waiting).
func (e *hubAddrEngine) reprovisionNodeWith(ctx context.Context, id int, onPoll func() error) (int64, error) {
	return e.runNodeJob(ctx, id, provision.KindReprovision, onPoll)
}

// runNodeJob starts a job of kind on node id and waits for it (polling, capped).
func (e *hubAddrEngine) runNodeJob(ctx context.Context, id int, kind string, onPoll func() error) (int64, error) {
	if e.env.Nodes == nil || e.env.Jobs == nil {
		return 0, fmt.Errorf("провижн не подключён")
	}
	job, err := e.env.Nodes.Start(ctx, id, kind)
	if err != nil {
		return job, err
	}
	deadline := e.env.Now().Add(hubJobCap)
	for {
		v, err := e.env.Jobs.JobView(ctx, job)
		if err != nil {
			return job, err
		}
		switch v.Status {
		case provision.JobSucceeded:
			return job, nil
		case provision.JobFailed, provision.JobInterrupted:
			msg := v.Error
			if msg == "" {
				msg = v.Status
			}
			return job, fmt.Errorf("задача %d: %s", job, msg)
		}
		if e.env.Now().After(deadline) {
			return job, fmt.Errorf("задача %d не закончилась за %s", job, hubJobCap)
		}
		if onPoll != nil {
			if err := onPoll(); err != nil {
				return job, err
			}
		}
		if err := e.env.Sleep(ctx, hubJobPoll); err != nil {
			return job, err
		}
	}
}

func prevNodesChecks(prev hubAddrStep) hubAddrNodesChecks {
	var ch hubAddrNodesChecks
	if len(prev.Checks) > 0 {
		_ = json.Unmarshal(prev.Checks, &ch)
	}
	return ch
}

func (e *hubAddrEngine) stepNodes(ctx context.Context, c hubAddrChange, prev hubAddrStep, lg *stepLog) (any, error) {
	nw := mustAddr(c.NewIP)
	ch := hubAddrNodesChecks{Excluded: prevNodesChecks(prev).Excluded}
	st, err := e.readHub(ctx)
	if err != nil {
		return ch, err
	}
	var hubAddrs []netip.Addr
	for _, p := range st.addrs {
		if p.Addr() != nw {
			hubAddrs = append(hubAddrs, p.Addr())
		}
	}
	nodes, err := e.env.ListNodes(ctx)
	if err != nil {
		return ch, err
	}
	// The exits that run the Hub watcher: it is reinstalled with the new address (a failure is
	// only reported — the watcher also looks at the Hub by the site's name).
	watched := map[int]bool{}
	if csv, err := e.env.Setting(watchdogExitsSetting, ""); err == nil {
		watched = parseWatchdogExits(csv)
	}
	var bad []string
	for _, n := range nodes {
		r := hubAddrNodeResult{ServerID: n.ID, Name: n.Name, IP: n.IP, Role: n.Role}
		switch {
		case containsInt(ch.Excluded, n.ID):
			r.Status = hubNodeExcluded
			lg.printf("%s: исключён владельцем", n.Name)
		case !n.Online || !n.HostKeyConfirmed:
			r.Status, r.Error = hubNodeNotReady, "не в сети или ключ хоста не подтверждён — исключите узел или верните его"
			bad = append(bad, n.Name+": "+r.Error)
		default:
			e.prepareNode(ctx, n, nw, hubAddrs, &r, lg)
			if r.Status != hubNodeOK {
				bad = append(bad, n.Name+": "+r.Error)
			} else if n.Role == "exit" && watched[n.ID] {
				if _, err := e.runNodeJob(withWatchdogHubAddr(ctx, nw.String()), n.ID, provision.KindWatchdog, nil); err != nil {
					lg.printf("%s: сторож не переустановлен на новый адрес: %v (он продолжает смотреть на хаб по имени сайта; повторите «Поставить сторожа» после смены)", n.Name, err)
				} else {
					lg.printf("%s: сторож переустановлен на новый адрес", n.Name)
				}
			}
		}
		ch.Nodes = append(ch.Nodes, r)
	}
	if len(bad) > 0 {
		if cur, err := e.env.Repo.Get(ctx, c.ID); err == nil && cur.State == hubAddrNodesReady {
			_ = e.env.Repo.SetState(ctx, c.ID, hubAddrNodesReady, hubAddrChecked, nil)
		}
		return ch, fmt.Errorf("узлы не готовы: %s", strings.Join(bad, "; "))
	}
	lg.printf("все узлы пускают новый адрес")
	return ch, e.advance(ctx, c, hubAddrNodesReady, nil)
}

func (e *hubAddrEngine) prepareNode(ctx context.Context, n hubAddrNode, nw netip.Addr, hubAddrs []netip.Addr, r *hubAddrNodeResult, lg *stepLog) {
	fail := func(format string, a ...any) {
		r.Status, r.Error = hubNodeFailed, fmt.Sprintf(format, a...)
		lg.printf("%s: %s", n.Name, r.Error)
	}
	lg.printf("%s (%s): переустанавливаю", n.Name, n.IP)
	job, err := e.reprovisionNode(ctx, n.ID)
	r.JobID = job
	if err != nil {
		fail("переустановка: %v", err)
		return
	}
	rem, err := e.env.DialNode(ctx, n.ID)
	if err != nil {
		fail("SSH: %v", err)
		return
	}
	out, err := rem.Run(ctx, hubnet.NodeSSHAllowScript(n.SSHPort, hubAddrs, nw))
	if err != nil {
		fail("разрешение SSH: %v: %s", err, strings.TrimSpace(out))
		return
	}
	r.SSHAllow = lastLine(out)
	ports := []int{n.SSHPort}
	if n.Role == "exit" {
		ports = append(ports, 50052)
	}
	for _, p := range ports {
		addr := net.JoinHostPort(n.IP, strconv.Itoa(p))
		if hubnet.ProbeTCP(ctx, e.env.Bound(nw), addr, 3) == 0 {
			fail("с нового адреса не открывается %s", addr)
			return
		}
	}
	r.Status = hubNodeOK
	lg.printf("%s: готов (%s)", n.Name, r.SSHAllow)
}

// ExcludeNode leaves serverID out of steps 3 and 5 (an offline node the owner accepts to lose).
func (e *hubAddrEngine) ExcludeNode(ctx context.Context, id int64, serverID int) error {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if !containsStr([]string{hubAddrChecked, hubAddrNodesReady}, c.State) {
		return fmt.Errorf("узел исключают на шаге «Подготовить узлы», сейчас «%s»", c.State)
	}
	s := c.Steps[hubStepNodes]
	ch := prevNodesChecks(s)
	if !containsInt(ch.Excluded, serverID) {
		ch.Excluded = append(ch.Excluded, serverID)
	}
	s.Checks, _ = json.Marshal(ch)
	if s.Status == "" {
		s.Status = hubStepPending
	}
	return e.env.Repo.SaveStep(ctx, id, hubStepNodes, s)
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
