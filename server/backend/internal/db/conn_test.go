package db

import "testing"

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
