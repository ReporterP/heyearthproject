package biz

import (
	"strings"
	"testing"
)

// The tunnel monitor's write is conditional on the status it read: a status written meanwhile by
// someone else (the node wizard, a re-setup) is never overwritten by a verdict about old paths.
// The condition reads the column through the same COALESCE as exitNodeColumns.
func TestUpdateTunnelStatusIfQueryIsConditional(t *testing.T) {
	q := normSQL(updateTunnelStatusIfQuery)
	if !strings.Contains(q, normSQL("WHERE id = $4 AND COALESCE(tunnel_status, 'unknown') = $5")) {
		t.Errorf("the monitor's status write is not conditional on the status it read:\n%s", updateTunnelStatusIfQuery)
	}
	if !strings.Contains(normSQL(exitNodeColumns), normSQL("COALESCE(tunnel_status, 'unknown')")) {
		t.Errorf("exitNodeColumns reads tunnel_status differently from the conditional write")
	}
}
