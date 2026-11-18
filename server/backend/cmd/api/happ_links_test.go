package main

import (
	"strings"
	"testing"
)

// Happ is gone from Google Play (the listing answers 404 on 2026-11-18, only clones remain):
// the bot sends Android and desktop users to the official site happ.info, never to Google Play.
// The site's list is site/src/lib/apps.js — keep the two in sync.
func TestHappLinksAvoidGooglePlay(t *testing.T) {
	for platform, s := range happStores {
		if strings.Contains(s[1], "play.google.com") {
			t.Errorf("%s: %s points at Google Play", platform, s[1])
		}
	}
	for _, p := range []string{"android", "pc"} {
		if !strings.HasPrefix(happStores[p][1], "https://happ.info/") {
			t.Errorf("%s: %q, want the official site happ.info", p, happStores[p][1])
		}
	}
}
