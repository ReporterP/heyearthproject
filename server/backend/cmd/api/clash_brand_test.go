package main

import (
	"regexp"
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

// The Clash Express proxy checks the Hub certificate by the same name Happ's Бамбук line uses
// (the certificate's domain), never by the connect address, which may be an IP (QA 2026-11-18).
func TestClashExpressSNIMatchesHappBamboo(t *testing.T) {
	useScriptDB(t, goldenScript())
	happ := subscriptionRequest(t, "Happ/3.1 Android", "").Body.String()
	m := regexp.MustCompile(`hysteria2://[^\s]*[?&]sni=([^&#\s]+)`).FindStringSubmatch(happ)
	if m == nil {
		t.Fatalf("no Бамбук line with sni in the Happ subscription:\n%s", happ)
	}
	clash := subscriptionRequest(t, "clash-verge/2.0", "?format=clash").Body.String()
	c := regexp.MustCompile(`name: "HiEarth-Express"\n(?:\s+[a-z-]+: [^\n]*\n)*?\s+sni: ([^\s]+)`).FindStringSubmatch(clash)
	if c == nil {
		t.Fatalf("no HiEarth-Express sni in the Clash profile:\n%s", clash)
	}
	if strings.Trim(c[1], `"`) != m[1] {
		t.Fatalf("Clash Express sni %q, Happ Бамбук sni %q", c[1], m[1])
	}
}
