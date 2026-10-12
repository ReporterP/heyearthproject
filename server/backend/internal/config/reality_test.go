package config

import (
	"os"
	"regexp"
	"testing"
)

// biz.DefaultRealitySNI and config.DefaultRealitySNI must agree: if the bootstrap xray config and
// the subscription disagree on the SNI, every Reality handshake fails. Read as source, so this
// test does not depend on internal/biz compiling.
func TestDefaultRealitySNIMatchesBiz(t *testing.T) {
	b, err := os.ReadFile("../biz/settings.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const DefaultRealitySNI\s*=\s*"([^"]+)"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("biz.DefaultRealitySNI not found in internal/biz/settings.go")
	}
	if string(m[1]) != DefaultRealitySNI {
		t.Errorf("config.DefaultRealitySNI = %q, biz.DefaultRealitySNI = %q", DefaultRealitySNI, m[1])
	}
	if DefaultRealitySNI == "apple.com" {
		t.Error("apple.com is a bad SNI for the RU Hub")
	}
}
