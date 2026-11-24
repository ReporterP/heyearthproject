package xraydiff

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func vless(email string, level int, flow bool) map[string]any {
	c := map[string]any{"id": "uuid-" + email, "email": email, "level": level}
	if flow {
		c["flow"] = "xtls-rprx-vision"
	}
	return c
}

func inbound(tag string, port int, flow bool) map[string]any {
	return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "vless",
		"settings": map[string]any{"decryption": "none", "clients": []any{vless("alice", 1, flow), vless("bob", 1, flow)}}}
}

func rule(tag string, kv ...any) map[string]any {
	r := map[string]any{"type": "field"}
	if tag != "" {
		r["ruleTag"] = tag
	}
	for i := 0; i < len(kv); i += 2 {
		r[kv[i].(string)] = kv[i+1]
	}
	return r
}

// baseConfig mirrors the shape SyncXrayConfig generates (Task 7c).
func baseConfig() map[string]any {
	return map[string]any{
		"api": map[string]any{"tag": "api", "services": []any{"HandlerService", "RoutingService", "StatsService"}},
		"inbounds": []any{inbound("vless-reality", 10443, true), inbound("vless-xhttp", 2443, false),
			map[string]any{"tag": "socks-local", "port": 10808, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{"tag": "block", "protocol": "blackhole"}, map[string]any{"tag": "exit-1", "protocol": "socks"}},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch",
			"balancers": []any{map[string]any{"tag": "exit-balancer", "selector": []any{"exit-1"}},
				map[string]any{"tag": "plan-3-balancer", "selector": []any{"exit-1"}}},
			"rules": []any{
				rule("", "inboundTag", []any{"api-in"}, "outboundTag", "api"),
				rule("", "ip", []any{"geoip:private"}, "outboundTag", "block"),
				rule("plan-3-direct-domains", "user", []any{"alice", "bob"}, "domain", []any{"domain:ya.ru"}, "outboundTag", "direct-freedom"),
				rule("plan-3-users", "user", []any{"alice", "bob"}, "balancerTag", "plan-3-balancer"),
				rule("", "network", "tcp,udp", "balancerTag", "exit-balancer"),
			}},
	}
}

func findInbound(cfg map[string]any, tag string) map[string]any {
	for _, raw := range cfg["inbounds"].([]any) {
		if in := raw.(map[string]any); in["tag"] == tag {
			return in
		}
	}
	panic(tag)
}

func setClients(cfg map[string]any, tag string, cs ...any) {
	findInbound(cfg, tag)["settings"].(map[string]any)["clients"] = cs
}

func routing(cfg map[string]any) map[string]any { return cfg["routing"].(map[string]any) }
func rules(cfg map[string]any) []any            { return routing(cfg)["rules"].([]any) }
func setRules(cfg map[string]any, rs ...any)    { routing(cfg)["rules"] = rs }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ops renders the user operations as "tag+email" / "tag-email" for comparison.
func ops(r Result) []string {
	var out []string
	for _, ic := range r.Inbounds {
		for _, e := range ic.Removed {
			out = append(out, ic.Tag+"-"+e)
		}
		for _, c := range ic.Added {
			out = append(out, ic.Tag+"+"+c["email"].(string))
		}
	}
	return out
}

func TestClassify(t *testing.T) {
	addCarol := func(c map[string]any) {
		setClients(c, "vless-reality", vless("alice", 1, true), vless("bob", 1, true), vless("carol", 1, true))
		setClients(c, "vless-xhttp", vless("alice", 1, false), vless("bob", 1, false), vless("carol", 1, false))
		rules(c)[3].(map[string]any)["user"] = []any{"alice", "bob", "carol"}
	}
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		kind      Kind
		ops       []string // user-only: expected operations
		rules     []string // user-only: expected ChangedRules
		routing   bool     // user-only: expected RoutingChanged
		reasonHas string   // structural: substring of Reason
	}{
		{name: "identical", mutate: func(map[string]any) {}, kind: None},
		{name: "client order only", kind: None, mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("bob", 1, true), vless("alice", 1, true))
		}},
		{name: "user added", kind: UserOnly, mutate: addCarol, routing: true, rules: []string{"plan-3-users"},
			ops: []string{"vless-reality+carol", "vless-xhttp+carol"}},
		{name: "quota cutoff", kind: UserOnly, routing: true, rules: []string{"plan-3-users"},
			ops: []string{"vless-reality-bob", "vless-xhttp-bob"}, mutate: func(c map[string]any) {
				setClients(c, "vless-reality", vless("alice", 1, true))
				setClients(c, "vless-xhttp", vless("alice", 1, false))
				rules(c)[3].(map[string]any)["user"] = []any{"alice"}
			}},
		{name: "modified client = remove+add", kind: UserOnly, ops: []string{"vless-reality-alice", "vless-reality+alice"},
			mutate: func(c map[string]any) { setClients(c, "vless-reality", vless("alice", 0, true), vless("bob", 1, true)) }},
		{name: "new plan rule", kind: UserOnly, routing: true, rules: []string{"plan-5-direct-ip"}, mutate: func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[2], rule("plan-5-direct-ip", "user", []any{"alice"}, "ip", []any{"5.255.255.0/24"}, "outboundTag", "direct-freedom"), r[3], r[4])
		}},
		{name: "plan rule moved", kind: UserOnly, routing: true, mutate: func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[2], r[4], r[3])
		}},
		{name: "plan rule removed", kind: UserOnly, routing: true, rules: []string{"plan-3-direct-domains"}, mutate: func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[3], r[4])
		}},
		{name: "catch-all changed", kind: Structural, reasonHas: "routing.rules", mutate: func(c map[string]any) {
			rules(c)[4].(map[string]any)["balancerTag"] = "plan-3-balancer"
		}},
		// adrules without -append replaces every balancer too (spike C4): an exit leaving the
		// selectors is applied live, open connections stay.
		{name: "balancer selector changed", kind: UserOnly, routing: true, mutate: func(c map[string]any) {
			routing(c)["balancers"].([]any)[0].(map[string]any)["selector"] = []any{"exit-1", "exit-2"}
		}},
		{name: "plan balancer added", kind: UserOnly, routing: true, mutate: func(c map[string]any) {
			b := routing(c)["balancers"].([]any)
			routing(c)["balancers"] = append(b, map[string]any{"tag": "plan-9-balancer", "selector": []any{"exit-2."}})
		}},
		{name: "users + balancer", kind: UserOnly, routing: true, rules: []string{"plan-3-users"},
			ops: []string{"vless-reality+carol", "vless-xhttp+carol"}, mutate: func(c map[string]any) {
				addCarol(c)
				routing(c)["balancers"].([]any)[0].(map[string]any)["selector"] = []any{"exit-2."}
			}},
		{name: "balancer + exit outbound", kind: Structural, reasonHas: "outbounds", mutate: func(c map[string]any) {
			routing(c)["balancers"].([]any)[0].(map[string]any)["selector"] = []any{"exit-1", "exit-2"}
			c["outbounds"] = append(c["outbounds"].([]any), map[string]any{"tag": "exit-2"})
		}},
		{name: "exit outbound added", kind: Structural, reasonHas: "outbounds", mutate: func(c map[string]any) {
			c["outbounds"] = append(c["outbounds"].([]any), map[string]any{"tag": "exit-2"})
		}},
		{name: "api services changed", kind: Structural, reasonHas: "api.services", mutate: func(c map[string]any) {
			c["api"].(map[string]any)["services"] = []any{"StatsService"}
		}},
		{name: "users + port change", kind: Structural, reasonHas: "inbounds", mutate: func(c map[string]any) {
			addCarol(c)
			findInbound(c, "vless-xhttp")["port"] = 2444
		}},
		{name: "client without email", kind: Structural, reasonHas: "no email", mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 1, true), map[string]any{"id": "uuid-x"})
		}},
		{name: "duplicate email", kind: Structural, reasonHas: "duplicate email", mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("bob", 1, true), vless("Bob", 0, true))
		}},
		{name: "duplicate plan ruleTag", kind: Structural, reasonHas: "duplicate ruleTag", mutate: func(c map[string]any) {
			rules(c)[2].(map[string]any)["ruleTag"] = "plan-3-users"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCfg := baseConfig()
			tt.mutate(newCfg)
			got, err := Classify(mustJSON(t, baseConfig()), mustJSON(t, newCfg))
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got.Kind != tt.kind {
				t.Fatalf("kind = %v (%s), want %v", got.Kind, got.Reason, tt.kind)
			}
			if tt.kind == Structural {
				if !strings.Contains(got.Reason, tt.reasonHas) {
					t.Errorf("reason = %q, want substring %q", got.Reason, tt.reasonHas)
				}
				return
			}
			if g := ops(got); strings.Join(g, ",") != strings.Join(tt.ops, ",") {
				t.Errorf("ops = %v, want %v", g, tt.ops)
			}
			if strings.Join(got.ChangedRules, ",") != strings.Join(tt.rules, ",") {
				t.Errorf("ChangedRules = %v, want %v", got.ChangedRules, tt.rules)
			}
			if got.RoutingChanged != tt.routing {
				t.Errorf("RoutingChanged = %v, want %v", got.RoutingChanged, tt.routing)
			}
		})
	}
}

// The file on disk is MarshalIndent output; formatting must never cause an apply.
func TestClassifyIgnoresFormatting(t *testing.T) {
	indented, err := json.MarshalIndent(baseConfig(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Classify(indented, mustJSON(t, baseConfig()))
	if err != nil || got.Kind != None {
		t.Fatalf("Classify(indented, compact) = %v (%s), %v; want none", got.Kind, got.Reason, err)
	}
}

func TestClassifyExpectationsAndPayloads(t *testing.T) {
	newCfg := baseConfig()
	setClients(newCfg, "vless-reality", vless("alice", 1, true), vless("bob", 1, true), vless("carol", 1, true))
	got, err := Classify(mustJSON(t, baseConfig()), mustJSON(t, newCfg))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"vless-reality": 3, "vless-xhttp": 2}; !reflect.DeepEqual(got.UserCounts, want) {
		t.Errorf("UserCounts = %v, want %v", got.UserCounts, want)
	}
	if want := []string{"", "", "plan-3-direct-domains", "plan-3-users", ""}; !reflect.DeepEqual(got.RuleTags, want) {
		t.Errorf("RuleTags = %v, want %v", got.RuleTags, want)
	}
	if a, r := got.UserTotals(); a != 1 || r != 0 {
		t.Errorf("UserTotals = +%d/-%d, want +1/-0", a, r)
	}
	payload, total, err := got.AddUsersPayload()
	if err != nil || total != 1 {
		t.Fatalf("AddUsersPayload total=%d err=%v", total, err)
	}
	var adu struct {
		Inbounds []struct {
			Tag      string
			Port     int
			Settings struct{ Clients []map[string]any }
		}
	}
	if err := json.Unmarshal(payload, &adu); err != nil {
		t.Fatal(err)
	}
	if len(adu.Inbounds) != 1 || adu.Inbounds[0].Tag != "vless-reality" || adu.Inbounds[0].Port != 10443 {
		t.Fatalf("adu inbounds = %+v", adu.Inbounds)
	}
	if cs := adu.Inbounds[0].Settings.Clients; len(cs) != 1 || cs[0]["email"] != "carol" || cs[0]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("adu clients = %v", cs)
	}
	rp, err := got.RoutingPayload()
	if err != nil || !strings.Contains(string(rp), `"plan-3-balancer"`) || !strings.HasPrefix(string(rp), `{"routing":`) {
		t.Fatalf("RoutingPayload = %s, %v", rp, err)
	}
	if _, err := Classify([]byte("{"), payload); err == nil {
		t.Error("invalid JSON: want error")
	}
}

func TestParseCLIOutput(t *testing.T) {
	cases := []struct {
		got, want int
	}{
		{ParseAddedTotal("processing inbound: vless-reality\nadd user: carol\nresult: ok\nAdded 1 user(s) in total.\n"), 1},
		{ParseAddedTotal("add user: bob\nUser bob already exists.\nAdded 0 user(s) in total.\n"), 0},
		{ParseAddedTotal("failed to dial 127.0.0.1:10085\n"), -1},
		{ParseRemovedTotal("remove user: bob\nRemoved 1 user(s) in total.\n"), 1},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %d, want %d", i, c.got, c.want)
		}
	}
	counts := []struct {
		out  string
		want int
		ok   bool
	}{
		{"{\n    \"count\": 42\n}\n", 42, true},     // JSON number
		{"{\n    \"count\": \"42\"\n}\n", 42, true}, // protojson int64 = string
		{"{}\n", 0, true},                           // count omitted when 0
		{"failed to get inbound user count\n", 0, false},
		{"{\"count\": \"many\"}", 0, false},
		{"{\"count\": -1}", 0, false},
	}
	for _, c := range counts {
		n, err := ParseUserCount(c.out)
		if (err == nil) != c.ok || (c.ok && n != c.want) {
			t.Errorf("ParseUserCount(%q) = %d, %v; want %d ok=%v", c.out, n, err, c.want, c.ok)
		}
	}
	tags, err := ParseRuleTags("{\n \"rules\": [\n  {\"tag\": \"api\"},\n  {\"ruleTag\": \"plan-3-users\"},\n  {}\n ]\n}\n")
	if err != nil || !reflect.DeepEqual(tags, []string{"", "plan-3-users", ""}) {
		t.Errorf("ParseRuleTags = %v, %v", tags, err)
	}
}

// roundtrip turns a test config into what json.Unmarshal yields (float64 numbers).
func roundtrip(t *testing.T, v any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(mustJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func inboundsByTag(cfg map[string]any) map[string]map[string]any {
	m := map[string]map[string]any{}
	for _, raw := range cfg["inbounds"].([]any) {
		in := raw.(map[string]any)
		m[in["tag"].(string)] = in
	}
	return m
}

// normalizeClients sorts every clients array by lower(email): client order is
// not part of the comparison (xray keeps users in a map).
func normalizeClients(cfg map[string]any) map[string]any {
	for _, in := range inboundsByTag(cfg) {
		settings, _ := in["settings"].(map[string]any)
		if _, has := settings["clients"]; !has {
			continue
		}
		cs, _ := settings["clients"].([]any)
		if cs == nil {
			cs = []any{}
		}
		sort.Slice(cs, func(i, j int) bool {
			return strings.ToLower(cs[i].(map[string]any)["email"].(string)) <
				strings.ToLower(cs[j].(map[string]any)["email"].(string))
		})
		settings["clients"] = cs
	}
	return cfg
}

// replayLive does to the OLD config what the live path does to the running xray:
// rmu (case-insensitive emails) → adrules without -append (routing replaced by
// RoutingPayload) → adu (clients of AddUsersPayload added to the inbound).
func replayLive(t *testing.T, oldCfg map[string]any, r Result) map[string]any {
	t.Helper()
	cfg := clone(oldCfg).(map[string]any)
	live := inboundsByTag(cfg)
	for _, ic := range r.Inbounds {
		settings := live[ic.Tag]["settings"].(map[string]any)
		gone := map[string]bool{}
		for _, e := range ic.Removed {
			gone[strings.ToLower(e)] = true
		}
		cs, _ := settings["clients"].([]any)
		kept := []any{}
		for _, c := range cs {
			if !gone[strings.ToLower(c.(map[string]any)["email"].(string))] {
				kept = append(kept, c)
			}
		}
		settings["clients"] = kept
	}
	if r.RoutingChanged {
		rp, err := r.RoutingPayload()
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Routing map[string]any `json:"routing"`
		}
		if err := json.Unmarshal(rp, &v); err != nil {
			t.Fatal(err)
		}
		cfg["routing"] = v.Routing
	}
	payload, total, err := r.AddUsersPayload()
	if err != nil {
		t.Fatal(err)
	}
	if total > 0 {
		var v struct {
			Inbounds []map[string]any `json:"inbounds"`
		}
		if err := json.Unmarshal(payload, &v); err != nil {
			t.Fatal(err)
		}
		for _, in := range v.Inbounds {
			tag := in["tag"].(string)
			added := in["settings"].(map[string]any)["clients"].([]any)
			// adu builds the whole inbound: apart from clients it must equal the running one.
			want, got := clone(live[tag]).(map[string]any), clone(in).(map[string]any)
			delete(want["settings"].(map[string]any), "clients")
			delete(got["settings"].(map[string]any), "clients")
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("adu inbound %s differs from the config inbound:\n got %v\nwant %v", tag, got, want)
			}
			settings := live[tag]["settings"].(map[string]any)
			cs, _ := settings["clients"].([]any)
			settings["clients"] = append(cs, added...)
		}
	}
	return cfg
}

func TestUserOnlyOpsReproduceNewConfig(t *testing.T) {
	setPlanUsers := func(c map[string]any, users ...any) { rules(c)[3].(map[string]any)["user"] = users }
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		revoked []string
	}{
		{"user added", func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 1, true), vless("bob", 1, true), vless("carol", 1, true))
			setClients(c, "vless-xhttp", vless("alice", 1, false), vless("bob", 1, false), vless("carol", 1, false))
			setPlanUsers(c, "alice", "bob", "carol")
		}, nil},
		{"quota cutoff", func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 1, true))
			setClients(c, "vless-xhttp", vless("alice", 1, false))
			setPlanUsers(c, "alice")
		}, []string{"bob"}},
		{"modified client", func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 0, true), vless("bob", 1, true))
		}, nil},
		{"new plan rule", func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[2], rule("plan-5-direct-ip", "user", []any{"alice"}, "ip", []any{"5.255.255.0/24"}, "outboundTag", "direct-freedom"), r[3], r[4])
		}, nil},
		{"plan rule moved", func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[3], r[2], r[4])
		}, nil},
		{"plan rule removed", func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[3], r[4])
		}, nil},
		{"last users leave", func(c map[string]any) {
			setClients(c, "vless-reality")
			setClients(c, "vless-xhttp")
			r := rules(c)
			setRules(c, r[0], r[1], r[4])
		}, []string{"alice", "bob"}},
		{"mixed: add, revoke, modify", func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 0, true), vless("carol", 1, true))
			setClients(c, "vless-xhttp", vless("alice", 0, false), vless("carol", 1, false))
			setPlanUsers(c, "alice", "carol")
			rules(c)[2].(map[string]any)["user"] = []any{"alice", "carol"}
		}, []string{"bob"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg, newCfg := roundtrip(t, baseConfig()), baseConfig()
			tc.mutate(newCfg)
			want := roundtrip(t, newCfg)
			got, err := Classify(mustJSON(t, oldCfg), mustJSON(t, want))
			if err != nil || got.Kind != UserOnly {
				t.Fatalf("Classify = %v (%s), %v; want user-only", got.Kind, got.Reason, err)
			}
			replayed := normalizeClients(replayLive(t, oldCfg, got))
			if !reflect.DeepEqual(replayed, normalizeClients(want)) {
				t.Fatalf("live ops on the old config do not give the new config:\n got %s\nwant %s",
					mustJSON(t, replayed), mustJSON(t, want))
			}
			if r := got.Revoked(); strings.Join(r, ",") != strings.Join(tc.revoked, ",") {
				t.Errorf("Revoked = %v, want %v", r, tc.revoked)
			}
		})
	}
}

func TestRevokedIsCaseInsensitive(t *testing.T) {
	oldCfg := baseConfig()
	setClients(oldCfg, "vless-reality", vless("alice", 1, true), vless("Bob", 1, true))
	newCfg := baseConfig()
	setClients(newCfg, "vless-reality", vless("alice", 1, true))
	got, err := Classify(mustJSON(t, oldCfg), mustJSON(t, newCfg))
	if err != nil || got.Kind != UserOnly {
		t.Fatalf("Classify = %v (%s), %v", got.Kind, got.Reason, err)
	}
	// bob stays in vless-xhttp: not revoked.
	if r := got.Revoked(); len(r) != 0 {
		t.Fatalf("Revoked = %v, want none (bob is still in vless-xhttp)", r)
	}
	setClients(newCfg, "vless-xhttp", vless("alice", 1, false))
	got, _ = Classify(mustJSON(t, oldCfg), mustJSON(t, newCfg))
	if r := got.Revoked(); strings.Join(r, ",") != "bob" {
		t.Fatalf("Revoked = %v, want [bob] (lower-case)", r)
	}
}
