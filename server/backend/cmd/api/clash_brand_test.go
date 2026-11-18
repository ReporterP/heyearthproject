package main

import (
	"strings"
	"testing"
)

// The Clash profile is a product surface like Happ's list: the brand is HiEarth everywhere
// (no «HeYearth» in proxy, group or file names).
func TestClashProfileSaysHiEarth(t *testing.T) {
	useScriptDB(t, goldenScript())
	rec := subscriptionRequest(t, "clash-verge/2.0", "?format=clash")
	body := rec.Body.String()
	if n := strings.Count(body, "HeYearth"); n > 0 {
		t.Fatalf("the Clash profile says «HeYearth» %d times", n)
	}
	if !strings.Contains(body, "HiEarth") {
		t.Fatalf("no HiEarth in the Clash profile:\n%s", body)
	}
}
