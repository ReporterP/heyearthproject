// Package xraydiff classifies the difference between two generated xray configs
// and builds/parses the `xray api` CLI payloads used to apply user-only changes.
//
// UserOnly: only inbounds[*].settings.clients and routing rules whose ruleTag
// starts with "plan-" differ — applied live via adu/rmu/adrules, no restart.
// Structural: anything else differs — `systemctl restart xray`.
// Pure (no I/O), fully unit-tested.
package xraydiff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PlanRulePrefix marks routing rules whose content depends only on the user list.
const PlanRulePrefix = "plan-"

type Kind int

const (
	None       Kind = iota // running xray needs no change
	UserOnly               // only clients and plan-* rules differ
	Structural             // restart required
)

func (k Kind) String() string { return [...]string{"none", "user-only", "structural"}[k] }

// InboundChange lists the user operations for one inbound.
type InboundChange struct {
	Tag     string
	Removed []string         // emails to remove (deleted + modified clients), sorted
	Added   []map[string]any // clients to add (new + modified), sorted by email
	inbound map[string]any   // inbound object of the new config, without clients
}

type Result struct {
	Kind           Kind
	Reason         string          // first structural difference
	Inbounds       []InboundChange // inbounds with user operations, sorted by tag
	RoutingChanged bool            // routing.rules differ (plan-* content or position)
	ChangedRules   []string        // plan-* ruleTags added/removed/modified (for logs)
	UserCounts     map[string]int  // inbound tag -> clients in the new config
	RuleTags       []string        // ruleTag ("" if none) of every new rule, in order
	routing        any             // routing object of the new config
}

// UserTotals returns the number of add and remove operations.
func (r Result) UserTotals() (added, removed int) {
	for _, ic := range r.Inbounds {
		added, removed = added+len(ic.Added), removed+len(ic.Removed)
	}
	return added, removed
}

type parts struct {
	rest      map[string]any                       // config without clients and plan-* rules
	clients   map[string]map[string]map[string]any // inbound tag -> lower(email) -> client
	inbounds  map[string]map[string]any            // inbound tag -> inbound (without clients)
	planRules map[string]any                       // ruleTag -> rule
	ruleTags  []string
}

// Classify compares the config xray runs from (oldJSON) with the new one.
func Classify(oldJSON, newJSON []byte) (Result, error) {
	var oldCfg, newCfg map[string]any
	if err := json.Unmarshal(oldJSON, &oldCfg); err != nil {
		return Result{}, fmt.Errorf("parse old config: %w", err)
	}
	if err := json.Unmarshal(newJSON, &newCfg); err != nil {
		return Result{}, fmt.Errorf("parse new config: %w", err)
	}
	if reflect.DeepEqual(oldCfg, newCfg) {
		return Result{Kind: None}, nil
	}
	oldP, err := split(oldCfg)
	if err != nil {
		return Result{Kind: Structural, Reason: "old config: " + err.Error()}, nil
	}
	newP, err := split(newCfg)
	if err != nil {
		return Result{Kind: Structural, Reason: "new config: " + err.Error()}, nil
	}
	if key := firstDiff(oldP.rest, newP.rest); key != "" {
		return Result{Kind: Structural, Reason: "changed " + key}, nil
	}
	res := Result{Kind: UserOnly, UserCounts: map[string]int{}, RuleTags: newP.ruleTags, routing: newCfg["routing"]}
	for _, tag := range unionKeys(oldP.clients, newP.clients) {
		oldC, newC := oldP.clients[tag], newP.clients[tag]
		ic := InboundChange{Tag: tag, inbound: newP.inbounds[tag]}
		if ic.inbound == nil {
			ic.inbound = oldP.inbounds[tag]
		}
		for _, k := range sortedKeys(oldC) {
			if nc, ok := newC[k]; !ok || !reflect.DeepEqual(oldC[k], nc) {
				ic.Removed = append(ic.Removed, oldC[k]["email"].(string))
			}
		}
		for _, k := range sortedKeys(newC) {
			if oc, ok := oldC[k]; !ok || !reflect.DeepEqual(oc, newC[k]) {
				ic.Added = append(ic.Added, newC[k])
			}
		}
		res.UserCounts[tag] = len(newC)
		if len(ic.Removed)+len(ic.Added) > 0 {
			res.Inbounds = append(res.Inbounds, ic)
		}
	}
	for _, tag := range unionKeys(oldP.planRules, newP.planRules) {
		if !reflect.DeepEqual(oldP.planRules[tag], newP.planRules[tag]) {
			res.ChangedRules = append(res.ChangedRules, tag)
		}
	}
	res.RoutingChanged = !reflect.DeepEqual(rulesOf(oldCfg), rulesOf(newCfg))
	if len(res.Inbounds) == 0 && !res.RoutingChanged {
		return Result{Kind: None}, nil // only client order / formatting differs
	}
	return res, nil
}

func rulesOf(cfg map[string]any) any {
	routing, _ := cfg["routing"].(map[string]any)
	return routing["rules"]
}

// split separates the user-dependent parts of a config from the rest.
func split(cfg map[string]any) (parts, error) {
	p := parts{rest: clone(cfg).(map[string]any), clients: map[string]map[string]map[string]any{},
		inbounds: map[string]map[string]any{}, planRules: map[string]any{}}
	ins, _ := p.rest["inbounds"].([]any)
	for i, raw := range ins {
		in, ok := raw.(map[string]any)
		if !ok {
			return p, fmt.Errorf("inbounds[%d] is not an object", i)
		}
		settings, _ := in["settings"].(map[string]any)
		rawClients, has := settings["clients"]
		if !has {
			continue
		}
		tag, _ := in["tag"].(string)
		if _, dup := p.clients[tag]; tag == "" || dup {
			return p, fmt.Errorf("inbounds[%d]: empty or duplicate tag %q", i, tag)
		}
		list, _ := rawClients.([]any)
		byEmail := map[string]map[string]any{}
		for j, rc := range list {
			c, _ := rc.(map[string]any)
			email, _ := c["email"].(string)
			if email == "" {
				return p, fmt.Errorf("inbound %s: client %d has no email", tag, j)
			}
			key := strings.ToLower(email) // xray matches emails case-insensitively
			if _, dup := byEmail[key]; dup {
				return p, fmt.Errorf("inbound %s: duplicate email %s", tag, email)
			}
			byEmail[key] = c
		}
		delete(settings, "clients")
		p.clients[tag], p.inbounds[tag] = byEmail, in
	}
	routing, _ := p.rest["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	if rules == nil {
		return p, nil
	}
	kept := make([]any, 0, len(rules))
	for i, raw := range rules {
		r, ok := raw.(map[string]any)
		if !ok {
			return p, fmt.Errorf("routing.rules[%d] is not an object", i)
		}
		tag, _ := r["ruleTag"].(string)
		p.ruleTags = append(p.ruleTags, tag)
		if !strings.HasPrefix(tag, PlanRulePrefix) {
			kept = append(kept, r)
			continue
		}
		if _, dup := p.planRules[tag]; dup {
			return p, fmt.Errorf("duplicate ruleTag %s", tag)
		}
		p.planRules[tag] = r
	}
	routing["rules"] = kept
	return p, nil
}

// firstDiff returns the dotted path of the first differing key ("" if equal).
func firstDiff(a, b map[string]any) string {
	for _, k := range unionKeys(a, b) {
		if reflect.DeepEqual(a[k], b[k]) {
			continue
		}
		am, aok := a[k].(map[string]any)
		bm, bok := b[k].(map[string]any)
		if sub := firstDiff(am, bm); aok && bok && sub != "" {
			return k + "." + sub
		}
		return k
	}
	return ""
}

func clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = clone(x)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, x := range t {
			s[i] = clone(x)
		}
		return s
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func unionKeys[V any](a, b map[string]V) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	return sortedKeys(set)
}

// ───── `xray api` CLI payloads and output (Xray-core 26.3.27 main/commands/all/api) ─────

// AddUsersPayload returns the `xray api adu` input: {"inbounds":[...]} with, per
// inbound, the full inbound object of the new config (InboundDetourConfig.Build
// needs listen/port/streamSettings) whose clients are only the users to add.
// total = number of users added (0 => nil payload).
func (r Result) AddUsersPayload() (payload []byte, total int, err error) {
	var ins []any
	for _, ic := range r.Inbounds {
		if len(ic.Added) == 0 {
			continue
		}
		in := clone(ic.inbound).(map[string]any)
		settings, _ := in["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
			in["settings"] = settings
		}
		clients := make([]any, 0, len(ic.Added))
		for _, c := range ic.Added {
			clients = append(clients, c)
		}
		settings["clients"] = clients
		ins = append(ins, in)
		total += len(ic.Added)
	}
	if total == 0 {
		return nil, 0, nil
	}
	payload, err = json.Marshal(map[string]any{"inbounds": ins})
	return payload, total, err
}

// RoutingPayload returns the `xray api adrules` input (used WITHOUT -append): the
// whole routing of the new config. Without -append xray replaces ALL rules and
// balancers (app/router/router.go ReloadRules), so live order == config order.
func (r Result) RoutingPayload() ([]byte, error) {
	if r.routing == nil {
		return nil, fmt.Errorf("new config has no routing")
	}
	return json.Marshal(map[string]any{"routing": r.routing})
}

var (
	reAdded   = regexp.MustCompile(`Added (\d+) user\(s\) in total`)
	reRemoved = regexp.MustCompile(`Removed (\d+) user\(s\) in total`)
	reCount   = regexp.MustCompile(`"count":\s*"?(\d+)`)
)

// ParseAddedTotal: N from "Added N user(s) in total." (adu exits 0 even when some
// users fail; failures are only printed). -1 if the line is missing.
func ParseAddedTotal(out string) int { return firstInt(reAdded, out) }

// ParseRemovedTotal: N from "Removed N user(s) in total." (rmu). -1 if missing.
func ParseRemovedTotal(out string) int { return firstInt(reRemoved, out) }

// ParseUserCount parses inboundusercount output {"count": N}; the field is
// omitted when N == 0, so "{}" means 0.
func ParseUserCount(out string) (int, error) {
	if n := firstInt(reCount, out); n >= 0 {
		return n, nil
	}
	if strings.Contains(out, "{") {
		return 0, nil
	}
	return 0, fmt.Errorf("inboundusercount: unexpected output %q", strings.TrimSpace(out))
}

// ParseRuleTags parses lsrules output {"rules":[{"tag":..,"ruleTag":..}]} into
// the ordered ruleTags ("" for rules without one).
func ParseRuleTags(out string) ([]string, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		return nil, fmt.Errorf("lsrules: no JSON in %q", strings.TrimSpace(out))
	}
	var v struct {
		Rules []struct {
			RuleTag string `json:"ruleTag"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out[i:j+1]), &v); err != nil {
		return nil, fmt.Errorf("lsrules: %w", err)
	}
	tags := make([]string, 0, len(v.Rules))
	for _, r := range v.Rules {
		tags = append(tags, r.RuleTag)
	}
	return tags, nil
}

func firstInt(re *regexp.Regexp, out string) int {
	m := re.FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}
