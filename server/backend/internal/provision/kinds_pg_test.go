package provision

import (
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// Every job kind the runner accepts is allowed by the database (a kind missing from
// provision_jobs_kind_check fails only on prod, at the first job of that kind).
func TestPGEveryKindAllowed(t *testing.T) {
	conn := testpg.Open(t)
	var def string
	if err := conn.QueryRow(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'provision_jobs_kind_check'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{KindProvision, KindReprovision, KindDeprovision, KindVerify, KindMTProxy, KindTGMTProxy, KindPathCheck, KindTunnelCheck, KindTunnelOn, KindTunnelOff, KindWatchdog} {
		if !ValidKind(k) {
			t.Fatalf("%s is not a valid kind", k)
		}
		if !strings.Contains(def, "'"+k+"'") {
			t.Errorf("provision_jobs_kind_check does not allow %q: %s", k, def)
		}
	}
}
