package admin

import (
	"math"
	"testing"
	"time"
)

func TestParseNodeSample(t *testing.T) {
	out := `cpu 1000 800
cpu 1100 850
mem 2000000 500000
nproc 2
net  ens3: 1000000 10 0 0 0 0 0 0 2000000 20 0 0 0 0 0 0
ct 1500 262144
tcp 37
`
	at := time.Unix(1000, 0)
	s, err := parseNodeSample(out, at)
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPct != 50 || s.MemPct != 75 || s.Cores != 2 || s.Iface != "ens3" || s.RxBytes != 1000000 || s.TxBytes != 2000000 ||
		s.Conntrack != 1500 || s.ConntrackMax != 262144 || s.TCP != 37 {
		t.Fatalf("%+v", s)
	}
	// A long interface name glued to the first counter.
	s2, err := parseNodeSample("cpu 1 1\ncpu 2 2\nmem 10 10\nnet enp0s31f6:1234 0 0 0 0 0 0 0 5678 0\n", at)
	if err != nil || s2.Iface != "enp0s31f6" || s2.RxBytes != 1234 || s2.TxBytes != 5678 || s2.CPUPct != 0 {
		t.Fatalf("%+v %v", s2, err)
	}
	if _, err := parseNodeSample("garbage", at); err == nil {
		t.Error("garbage accepted")
	}

	next := s
	next.At = at.Add(60 * time.Second)
	next.RxBytes += 75_000_000 // 600 Mbit in 60 s = 10 Mbit/s
	next.TxBytes += 7_500_000
	rx, tx, ok := netRates(s, next)
	if !ok || math.Abs(rx-10) > 1e-9 || math.Abs(tx-1) > 1e-9 {
		t.Errorf("rates %v %v %v", rx, tx, ok)
	}
	reboot := next
	reboot.RxBytes = 5
	if _, _, ok := netRates(next, reboot); ok {
		t.Error("a counter reset gave a rate")
	}
}
