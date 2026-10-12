package biz

import (
	"strings"
	"testing"
)

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

// Every query documented as "ordered by effectiveSubOrderBy" must really be: a
// regression to `ORDER BY valid_until DESC` would again judge a paid plan by a
// parallel trial's limit (quota) or route the user by the wrong plan.
func TestEffectiveSubQueries(t *testing.T) {
	tests := []struct {
		name  string
		query string
		alias string
	}{
		{"ListActiveVlessClients best_plan_id", activeVlessClientsQuery, "sub"},
		{"EffectivePlanID (Happ routing deeplink)", effectivePlanIDQuery, "us"},
		{"ListQuotaExceededUserIDs", quotaExceededQuery, "us"},
		{"EffectiveLimit", effectiveLimitQuery, "us"},
		{"BuildTrafficResponse", trafficSubscriptionQuery, "us"},
		{"ValidateHysteria2Auth", hysteria2AuthPlanQuery, "us"},
		{"CanUseHysteria2", hysteria2PlanGateQuery, "us"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := "ORDER BY " + effectiveSubOrderBy(tt.alias)
			if !strings.Contains(tt.query, want) {
				t.Errorf("query does not order by effectiveSubOrderBy(%q):\nwant substring %q\nquery:%s", tt.alias, want, tt.query)
			}
			if bare := "ORDER BY " + tt.alias + ".valid_until"; strings.Contains(tt.query, bare) {
				t.Errorf("query still orders by bare valid_until (%q)", bare)
			}
		})
	}
}
