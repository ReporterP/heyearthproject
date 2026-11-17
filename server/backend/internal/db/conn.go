package db

import (
	"net/url"
	"strings"
)

// Session limits for the API's pool. Before them a stuck query held its
// connection, and the request waiting on it, forever, and an unreachable
// database hung the dial. Migrations use their own handle without the
// statement limit: some of them rewrite large tables.
const (
	apiStatementTimeoutMs = "60000"
	apiConnectTimeoutSec  = "10"
)

// withSessionLimits adds connect_timeout and statement_timeout to a lib/pq
// connection string (URL or key=value form) unless DATABASE_URL sets them.
// lib/pq sends unknown keys to the server as run-time parameters.
func withSessionLimits(connStr string) string {
	if strings.Contains(connStr, "://") {
		u, err := url.Parse(connStr)
		if err != nil {
			return connStr
		}
		q := u.Query()
		if q.Get("connect_timeout") == "" {
			q.Set("connect_timeout", apiConnectTimeoutSec)
		}
		if q.Get("statement_timeout") == "" {
			q.Set("statement_timeout", apiStatementTimeoutMs)
		}
		u.RawQuery = q.Encode()
		return u.String()
	}
	has := func(key string) bool {
		for _, f := range strings.Fields(connStr) {
			if strings.HasPrefix(f, key+"=") {
				return true
			}
		}
		return false
	}
	out := connStr
	if !has("connect_timeout") {
		out += " connect_timeout=" + apiConnectTimeoutSec
	}
	if !has("statement_timeout") {
		out += " statement_timeout=" + apiStatementTimeoutMs
	}
	return out
}
