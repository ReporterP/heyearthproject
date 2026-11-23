package db

import (
	"strings"
	"testing"
)

func TestWithSessionLimits(t *testing.T) {
	cases := []struct{ in, want string }{
		{"postgres://u:p@localhost:5432/heyearth?sslmode=disable",
			"postgres://u:p@localhost:5432/heyearth?connect_timeout=10&sslmode=disable&statement_timeout=60000"},
		{"postgres://u:p@localhost:5432/heyearth",
			"postgres://u:p@localhost:5432/heyearth?connect_timeout=10&statement_timeout=60000"},
		// An explicit value in DATABASE_URL wins.
		{"postgres://u:p@localhost/heyearth?statement_timeout=5000",
			"postgres://u:p@localhost/heyearth?connect_timeout=10&statement_timeout=5000"},
		{"host=localhost dbname=heyearth sslmode=disable",
			"host=localhost dbname=heyearth sslmode=disable connect_timeout=10 statement_timeout=60000"},
		{"host=localhost statement_timeout=0",
			"host=localhost statement_timeout=0 connect_timeout=10"},
	}
	for _, c := range cases {
		if got := withSessionLimits(c.in); got != c.want {
			t.Errorf("%q:\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}

func TestWithReadOnly(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:p@h:5432/heyearth?sslmode=disable": "default_transaction_read_only=on",
		"host=h dbname=heyearth":                         "host=h dbname=heyearth default_transaction_read_only=on",
	} {
		if got := withReadOnly(in); !strings.Contains(got, want) {
			t.Errorf("withReadOnly(%q) = %q, want it to carry %q", in, got, want)
		}
	}
}
