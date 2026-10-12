package biz

import "testing"

// effectiveSubOrderBy is the single definition of "the user's effective
// subscription" shared by the plan routing (ListActiveVlessClients, alias "sub"),
// the quota cutoff and the cabinet traffic card (alias "us"). Both aliases must
// produce the same ordering, tie-breaker included.
func TestEffectiveSubOrderBy(t *testing.T) {
	tests := []struct {
		alias string
		want  string
	}{
		{"us", "(us.plan_id IS NOT NULL) DESC, us.valid_until DESC, us.id DESC"},
		{"sub", "(sub.plan_id IS NOT NULL) DESC, sub.valid_until DESC, sub.id DESC"},
	}
	for _, tt := range tests {
		if got := effectiveSubOrderBy(tt.alias); got != tt.want {
			t.Errorf("effectiveSubOrderBy(%q) = %q, want %q", tt.alias, got, tt.want)
		}
	}
}
