package main

import "testing"

// D2: the mTLS certificate monitor raises an alarm instead of only logging.
func TestCertAlert(t *testing.T) {
	for _, c := range []struct {
		days  int
		level AlertLevel
	}{{-1, AlertCritical}, {0, AlertCritical}, {6, AlertCritical}, {7, AlertWarn}, {29, AlertWarn}, {30, ""}, {365, ""}} {
		if l, title := certAlert("ca-cert.pem", c.days); l != c.level || (l != "" && title == "") {
			t.Errorf("%d days: %q %q", c.days, l, title)
		}
	}
}
