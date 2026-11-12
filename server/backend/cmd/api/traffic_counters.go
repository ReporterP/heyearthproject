package main

// counterPair is a cumulative byte pair from a source whose counters only grow
// until that source restarts (Hysteria trafficStats, `awg show transfer`).
// Up = bytes the user sent, Down = bytes the user received.
type counterPair struct{ Up, Down int64 }

// cumulativeDeltas turns two cumulative snapshots into per-key growth.
//
// primed=false is the first read since this process started: the previous
// snapshot is unknown, so the read only becomes the baseline. Diffing it
// against zero booked everything the source had counted since it started -
// every API restart charged users their whole history again (2026-11-12).
//
// A key whose counter went down in either direction means the source
// restarted: it is re-baselined and books nothing for this read. A key absent
// from prev appeared after the last read, so all of its value is new.
func cumulativeDeltas(prev, cur map[string]counterPair, primed bool) map[string]counterPair {
	out := map[string]counterPair{}
	if !primed {
		return out
	}
	for k, c := range cur {
		p := prev[k]
		d := counterPair{Up: c.Up - p.Up, Down: c.Down - p.Down}
		if d.Up < 0 || d.Down < 0 || (d.Up == 0 && d.Down == 0) {
			continue
		}
		out[k] = d
	}
	return out
}
