package main

// The deploy guard of cmd/deploy (docs/prd/stability-operations.prd.md, part 4): the modes that
// change the Hub are refused from a dirty tree, from a commit not on main, from a commit that does
// not contain the deployed build, and while another deploy holds the lock; after success the
// deployed build is recorded and the change goes to the ops feed. Rules: internal/deployguard.

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/heyearth/backend/internal/deployguard"
	"golang.org/x/crypto/ssh"
)

// guardMode is what a deploy mode is checked against (component in deployed.json), what it
// records after success, and its line in the feed (kind "" — the API writes its own «start»).
type guardMode struct {
	check   string
	record  []string
	kind    string
	impact  string
	summary string
}

var guardedModes = map[string]guardMode{
	"main":                {check: "api", record: []string{"api", "admin", "site"}},
	"update-main":         {check: "api", record: []string{"api"}},
	"site":                {check: "admin", record: []string{"admin", "site"}, kind: "action", impact: "K0", summary: "выкачены админка и сайт"},
	"admin-web":           {check: "admin", record: []string{"admin"}, kind: "action", impact: "K0", summary: "выкачена админка"},
	"vue-site":            {check: "site", record: []string{"site"}, kind: "action", impact: "K0", summary: "выкачен сайт"},
	"nginx":               {check: "api", kind: "maintenance", impact: "K0", summary: "обновлены конфиг nginx и сертификат (reload)"},
	"setup-xray":          {check: "api", kind: "maintenance", impact: "K3", summary: "выполнен --setup-xray"},
	"setup-hysteria2-hub": {check: "api", kind: "maintenance", impact: "K2", summary: "выполнен --setup-hysteria2-hub (перезапуск сервера Бамбука)"},
	"setup-hygiene":       {check: "api", kind: "maintenance", impact: "K0", summary: "выполнен --setup-hygiene"},
	"setup-watchdog":      {check: "api", kind: "maintenance", impact: "K0", summary: "выполнен --setup-watchdog"},
	"setup-ops":           {check: "api", kind: "maintenance", impact: "K0", summary: "выполнен --setup-ops"},
	"setup-hardening":     {check: "api", kind: "maintenance", impact: "K0", summary: "выполнен --setup-hardening"},
}

// guardLocal is the part decided before any connection: exit 2 on a refusal (unless forced).
func guardLocal(mode, repoRoot, force string) deployguard.Local {
	local := deployguard.LocalState(repoRoot)
	if _, ok := guardedModes[mode]; !ok {
		return local
	}
	v := deployguard.CheckLocal(local)
	if len(v.Refusals) == 0 {
		return local
	}
	if force == "" {
		for _, r := range v.Refusals {
			fmt.Println("ОТКАЗ: " + r)
		}
		fmt.Println(`Обход — только осознанно: --force-deploy "<причина, не короче 10 символов>"`)
		os.Exit(2)
	}
	if _, err := v.Force(force); err != nil {
		fmt.Println("ОТКАЗ: " + err.Error())
		os.Exit(2)
	}
	return local
}

func guardRun(client *ssh.Client) deployguard.Run {
	return func(cmd string, stdin []byte) (string, error) { return runSSHQuiet(client, cmd, string(stdin)) }
}

// guardOpen checks the Hub's side and takes the lock (nil for an unguarded mode); exit 2 on a
// refusal. A fatal error later leaves the lock behind: the next deploy takes it over after
// deployguard.StaleLock with a warning.
func guardOpen(client *ssh.Client, mode string, local deployguard.Local, force string) *deployguard.Gate {
	gm, ok := guardedModes[mode]
	if !ok {
		return nil
	}
	fmt.Println("[guard] Защита выкатки...")
	gate, err := deployguard.Open(guardRun(client), gm.check, deployguard.Holder(time.Now()), local, force, time.Now(), os.Stdout)
	if err != nil {
		fmt.Println(err)
		if errors.Is(err, deployguard.ErrRefused) {
			os.Exit(2)
		}
		os.Exit(1)
	}
	if force != "" {
		if err := deployguard.Feed(guardRun(client), deployguard.Event{Kind: "note", Actor: "deploy", Target: "hub", Impact: "K0", OK: true,
			Summary: "--" + mode + " " + local.SHA + " в обход защиты: " + force}); err != nil {
			fmt.Printf("[guard] WARN: причина обхода не записана в ленту: %v\n", err)
		}
	}
	return gate
}

// guardFinish records what the mode deployed, releases the lock and writes the feed line.
func guardFinish(client *ssh.Client, gate *deployguard.Gate, mode string, local deployguard.Local) {
	gm, ok := guardedModes[mode]
	if !ok || gate == nil {
		return
	}
	sha := local.SHA
	if local.Dirty {
		sha += "-dirty"
	}
	run := guardRun(client)
	_, doc := deployguard.ParseRemote("", mustRead(run), time.Now())
	for _, c := range gm.record {
		next, err := deployguard.MergeDeployed(doc, c, sha, time.Now())
		if err == nil {
			doc = next
		}
	}
	if len(gm.record) > 0 {
		if err := deployguard.Record(run, doc, gm.record[0], sha, time.Now()); err != nil {
			fmt.Printf("[guard] WARN: выкаченная сборка не записана (deployed.json): %v\n", err)
		}
	}
	gate.Release()
	if gm.kind != "" {
		if err := deployguard.Feed(run, deployguard.Event{Kind: gm.kind, Actor: "deploy", Target: "hub", Impact: gm.impact, OK: true,
			Summary: gm.summary + " " + sha}); err != nil {
			fmt.Printf("[guard] WARN: запись в ленту не сделана: %v\n", err)
		}
	}
}

func mustRead(run deployguard.Run) string {
	out, _ := run(deployguard.ReadRemoteCmd(), nil)
	return out
}
