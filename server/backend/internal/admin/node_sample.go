package admin

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Node load (cleanup plan B3): the Hub reads /proc on every server — exits and RU entries
// over SSH with its own key, itself locally — once a minute. One shell script, one parser,
// the same numbers for every role. CPU is busy time over a 1 s window (load average is not
// a percentage and ignores the number of cores).

// nodeSampleScript prints, one item per line:
//
//	cpu <total jiffies> <idle jiffies>   (twice, 1 s apart)
//	mem <MemTotal kB> <MemAvailable kB>
//	nproc <cores>
//	net <iface>: <the /proc/net/dev counters>
//	ct <nf_conntrack_count> <nf_conntrack_max>
//	tcp <established TCP sockets>
const nodeSampleScript = `cpuline() { awk '/^cpu /{t=0; for(i=2;i<=NF;i++) t+=$i; print "cpu", t, $5+$6}' /proc/stat; }
cpuline; sleep 1; cpuline
awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{print "mem", t, a}' /proc/meminfo
echo "nproc $(nproc 2>/dev/null || echo 1)"
dev=$(ip route show default 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}')
[ -n "$dev" ] && grep -E "^[[:space:]]*${dev}:" /proc/net/dev | sed 's/^[[:space:]]*/net /'
echo "ct $(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null || echo 0) $(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null || echo 0)"
echo "tcp $(ss -Htn state established 2>/dev/null | wc -l)"
`

// nodeSample is one reading of a server.
type nodeSample struct {
	CPUPct       float64
	MemPct       float64
	Cores        int
	Iface        string
	RxBytes      uint64
	TxBytes      uint64
	Conntrack    int
	ConntrackMax int
	TCP          int
	At           time.Time
}

// parseNodeSample reads the script's output.
func parseNodeSample(out string, at time.Time) (nodeSample, error) {
	s := nodeSample{At: at}
	var cpu [][2]uint64
	var memTotal, memAvail uint64
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 0 {
			continue
		}
		seen[f[0]] = true
		switch f[0] {
		case "cpu":
			if len(f) >= 3 {
				t, e1 := strconv.ParseUint(f[1], 10, 64)
				i, e2 := strconv.ParseUint(f[2], 10, 64)
				if e1 == nil && e2 == nil {
					cpu = append(cpu, [2]uint64{t, i})
				}
			}
		case "mem":
			if len(f) >= 3 {
				memTotal, _ = strconv.ParseUint(f[1], 10, 64)
				memAvail, _ = strconv.ParseUint(f[2], 10, 64)
			}
		case "nproc":
			if len(f) >= 2 {
				s.Cores, _ = strconv.Atoi(f[1])
			}
		case "net":
			// "net eth0: 123 456 ..." or "net eth0:123 456 ..." (no space after a long name)
			rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "net"))
			name, counters, ok := strings.Cut(rest, ":")
			if !ok {
				continue
			}
			c := strings.Fields(counters)
			if len(c) < 9 {
				continue
			}
			s.Iface = strings.TrimSpace(name)
			s.RxBytes, _ = strconv.ParseUint(c[0], 10, 64)
			s.TxBytes, _ = strconv.ParseUint(c[8], 10, 64)
		case "ct":
			if len(f) >= 3 {
				s.Conntrack, _ = strconv.Atoi(f[1])
				s.ConntrackMax, _ = strconv.Atoi(f[2])
			}
		case "tcp":
			if len(f) >= 2 {
				s.TCP, _ = strconv.Atoi(f[1])
			}
		}
	}
	if len(cpu) < 2 || !seen["mem"] {
		return s, fmt.Errorf("incomplete sample: %q", firstLine(out))
	}
	dt := float64(cpu[1][0]) - float64(cpu[0][0])
	di := float64(cpu[1][1]) - float64(cpu[0][1])
	if dt > 0 {
		s.CPUPct = clampPct(100 * (dt - di) / dt)
	}
	if memTotal > 0 {
		s.MemPct = clampPct(100 * float64(memTotal-min(memAvail, memTotal)) / float64(memTotal))
	}
	if s.Cores < 1 {
		s.Cores = 1
	}
	return s, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// netRates is the interface throughput between two samples, Mbit/s; a counter reset
// (reboot, new interface) gives no rate.
func netRates(prev, cur nodeSample) (rxMbps, txMbps float64, ok bool) {
	dt := cur.At.Sub(prev.At).Seconds()
	if dt <= 0 || prev.Iface != cur.Iface || cur.RxBytes < prev.RxBytes || cur.TxBytes < prev.TxBytes {
		return 0, 0, false
	}
	rx := float64(cur.RxBytes-prev.RxBytes) * 8 / dt / 1e6
	tx := float64(cur.TxBytes-prev.TxBytes) * 8 / dt / 1e6
	return rx, tx, true
}
