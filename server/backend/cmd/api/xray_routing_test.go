package main

import (
	"reflect"
	"testing"
)

func TestRoutingBuilders(t *testing.T) {
	b := newBalancer("plan-3-balancer", []string{"exit-2"}, "random")
	if b["fallbackTag"] != "exit-2" || b["tag"] != "plan-3-balancer" || !reflect.DeepEqual(b["selector"], []string{"exit-2"}) {
		t.Fatalf("unexpected balancer: %v", b)
	}
	if e := newBalancer("plan-9-balancer", nil, "random"); e["fallbackTag"] != "block" {
		t.Fatalf("empty balancer must fall back to block: %v", e)
	}
	r := noExitBlockRule([]string{"vless-reality", "socks-awg"})
	if r["outboundTag"] != "block" || !reflect.DeepEqual(r["inboundTag"], []string{"vless-reality", "socks-awg"}) {
		t.Fatalf("unexpected no-exit rule: %v", r)
	}
	d := dnsDirectRule()
	if d["outboundTag"] != "direct" || !reflect.DeepEqual(d["inboundTag"], []string{"dns-out"}) {
		t.Fatalf("unexpected dns rule: %v", d)
	}
	inbounds := []map[string]interface{}{
		{"tag": "vless-reality"}, {"tag": "vless-xhttp"}, {"tag": "shadowsocks"},
		{"tag": "vless-xhttp-cdn"}, {"tag": "api-in"}, {"tag": "socks-awg"},
	}
	want := []string{"vless-reality", "vless-xhttp", "shadowsocks", "vless-xhttp-cdn", "socks-awg"}
	if got := userInboundTags(inbounds); !reflect.DeepEqual(got, want) {
		t.Fatalf("userInboundTags = %v, want %v", got, want)
	}
}

func TestCheckNoDirectLeak(t *testing.T) {
	direct := map[string]interface{}{"protocol": "freedom", "tag": "direct"}
	good := []map[string]interface{}{newBlockOutbound(), direct}
	withBal := map[string]interface{}{"balancers": []map[string]interface{}{
		newBalancer("exit-balancer", []string{"exit-2"}, "random"),
	}}
	legacyBal := map[string]interface{}{"balancers": []map[string]interface{}{
		{"tag": "exit-balancer", "selector": []string{"exit-2"}},
	}}
	if err := checkNoDirectLeak(good, withBal); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := checkNoDirectLeak(good, map[string]interface{}{}); err != nil {
		t.Fatalf("no-exit config rejected: %v", err)
	}
	if checkNoDirectLeak([]map[string]interface{}{direct, newBlockOutbound()}, withBal) == nil {
		t.Fatal("direct-first config accepted")
	}
	if checkNoDirectLeak(good, legacyBal) == nil {
		t.Fatal("balancer without fallbackTag accepted")
	}
	directFallback := map[string]interface{}{"balancers": []map[string]interface{}{
		{"tag": "exit-balancer", "selector": []string{"exit-2"}, "fallbackTag": "direct"},
	}}
	if checkNoDirectLeak(good, directFallback) == nil {
		t.Fatal("balancer falling back to direct accepted")
	}
}
