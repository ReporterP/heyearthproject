package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// doNginx's remote checks fail closed: an SSH error or an unexpected answer is an error, never a
// "no" (which would write an HTTP-only config or delete the stream config).
func TestParseYesNo(t *testing.T) {
	for _, c := range []struct {
		out    string
		err    error
		want   bool
		wantOK bool
	}{
		{"yes\n", nil, true, true},
		{"no\n", nil, false, true},
		{"", errors.New("ssh: connection lost"), false, false},
		{"", nil, false, false},
		{"enabled\n", nil, false, false},
		{"yes\nno\n", nil, false, false},
	} {
		got, err := parseYesNo(c.out, c.err)
		if (err == nil) != c.wantOK || got != c.want {
			t.Errorf("parseYesNo(%q, %v) = %v, %v", c.out, c.err, got, err)
		}
	}
	for _, cmd := range []string{xrayInstalledCmd} {
		if !strings.Contains(cmd, "echo yes") || !strings.Contains(cmd, "echo no") {
			t.Errorf("%q must answer yes or no", cmd)
		}
	}
}

// doNginx: errors of its checks stop it, and nginx is validated and reloaded, never restarted
// (a restart drops every connection behind the 443 stream).
func TestDoNginxFailsClosedAndReloads(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func doNginx(")
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	for _, w := range []string{
		"xrayActive, err := isXrayInstalled(client)\n\tif err != nil {\n\t\tlog.Fatalf(",
		"\"\"))\n\tif err != nil {\n\t\tlog.Fatalf(\"nginx: проверка сертификата", // ssl, err := parseYesNo(...)
		"if _, err := runSSH(client, nginxApplyCmd, true); err != nil {\n\t\tlog.Fatalf(",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("doNginx must stop on a failed check / apply: lacks %q", w)
		}
	}
	if !strings.Contains(body, "ssl, err := parseYesNo(") {
		t.Error("the certificate check must go through parseYesNo")
	}
	if strings.Contains(body, "restart nginx") {
		t.Error("doNginx restarts nginx")
	}
	if !strings.Contains(nginxApplyCmd, "nginx -t && ") || !strings.Contains(nginxApplyCmd, "systemctl reload nginx") || strings.Contains(nginxApplyCmd, "restart") {
		t.Errorf("nginxApplyCmd: %q", nginxApplyCmd)
	}
}
