// Package shellquote quotes values for a POSIX shell running on a remote node.
package shellquote

import "strings"

// Quote single-quotes s for a POSIX shell: the result is always one literal word, whatever s
// contains ($(...), backticks, quotes, spaces, ;, |, newlines): each embedded single quote closes
// the quoted string, is escaped with a backslash and reopens it.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
