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

// normSQL collapses whitespace and upper-cases a query, so the filter checks
// below do not depend on its layout or keyword case.
func normSQL(q string) string {
	return strings.ToUpper(strings.Join(strings.Fields(q), " "))
}

// The Express (Bamboo) plan queries switched to effectiveSubOrderBy only because
// their inner JOIN plans makes its first key constant; the filters must stay:
// a LEFT JOIN would let plan-less rows win the ordering, and dropping
// status = 'active' would authorize paused/cancelled subscriptions. The auth
// query also keeps the legacy traffic_used filter (documented in
// hysteria2AuthPlanQuery).
func TestHysteria2PlanQueryFilters(t *testing.T) {
	const innerJoin = "JOIN plans p ON p.id = us.plan_id"
	tests := []struct {
		name  string
		query string
		must  []string
	}{
		{"ValidateHysteria2Auth", hysteria2AuthPlanQuery, []string{
			innerJoin,
			"us.status = 'active'",
			"us.valid_until > NOW()",
			"(us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)",
		}},
		{"CanUseHysteria2", hysteria2PlanGateQuery, []string{
			innerJoin,
			"us.status = 'active'",
			"us.valid_until > NOW()",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := normSQL(tt.query)
			for _, m := range tt.must {
				if !strings.Contains(q, normSQL(m)) {
					t.Errorf("query lost the filter %q:\n%s", m, tt.query)
				}
			}
			for _, outer := range []string{"LEFT", "RIGHT", "FULL", "OUTER", "CROSS"} {
				if strings.Contains(q, outer+" JOIN PLANS") {
					t.Errorf("plans must be joined with an inner JOIN, found %s JOIN:\n%s", outer, tt.query)
				}
			}
		})
	}
}
