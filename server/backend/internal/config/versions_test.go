package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHysteriaDownloadURL(t *testing.T) {
	u := HysteriaDownloadURL()
	if !strings.Contains(u, "app%2Fv2.13.0") {
		t.Fatalf("URL %q lacks the encoded tag app%%2Fv2.13.0", u)
	}
	if !strings.HasPrefix(u, "https://github.com/apernet/hysteria/releases/download/") ||
		!strings.HasSuffix(u, "/hysteria-linux-amd64") {
		t.Fatalf("unexpected URL shape: %q", u)
	}
}

func TestHysteriaInstallScript(t *testing.T) {
	s := HysteriaInstallScript()
	for _, want := range []string{
		`HY_WANT="` + HysteriaVersion + `"`,
		HysteriaDownloadURL(),
		"--connect-timeout 15 --max-time 300 --retry 3 --retry-delay 2",
		`[ "$(/usr/local/bin/hysteria.new version 2>/dev/null | awk '$1=="Version:"{print $2}')" = "$HY_WANT" ]`,
		`|| HY_CUR=""`,
		"mv -f /usr/local/bin/hysteria.bak.tmp /usr/local/bin/hysteria.bak",
		"mv -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// The script is spliced into fmt.Sprintf output and into plain strings: it must be
	// the final text, never a format string ("%%" would mean someone double-escaped it).
	if strings.Contains(s, "%%") {
		t.Error("install script contains %% (format-escaped text)")
	}
	if !strings.HasSuffix(s, "\n") {
		t.Error("install script must end with a newline so the next line starts cleanly")
	}
}

func TestHysteriaEnsureScript(t *testing.T) {
	s := HysteriaEnsureScript()
	if n := strings.Count(s, HysteriaInstallScript()); n != 1 {
		t.Fatalf("ensure script embeds the install script %d times, want 1", n)
	}
	if !strings.HasPrefix(s, "if [ -x /usr/local/bin/hysteria ]; then\n") {
		t.Error("ensure script must gate the install on the binary being absent")
	}
	if !strings.Contains(s, "keeping existing Hub binary (upgrade: deploy --setup-hysteria2-hub)") {
		t.Error("ensure script lacks the keep message")
	}
}

func TestHysteriaInstallError(t *testing.T) {
	tests := []struct {
		name   string
		out    string
		wantOK bool
	}{
		{"installed", "[xray] ok\n[hysteria] installed " + HysteriaVersion + " (previous: v2.6.1 -> /usr/local/bin/hysteria.bak)\n[hy2] ok\n", true},
		{"already current", "[hysteria] " + HysteriaVersion + " already installed\n", true},
		{"error line", "[hysteria] ERROR: download/verify of " + HysteriaVersion + " failed, binary unchanged\n", false},
		{"error line wins over success", "[hysteria] " + HysteriaVersion + " already installed\n[hysteria] ERROR: x\n", false},
		{"stopped before install", "[xray] Сервис запущен\n", false},
		{"other version", "[hysteria] installed v2.13.01 (previous: none -> /usr/local/bin/hysteria.bak)\n", false},
		{"ensure kept Hub binary", "[hysteria] keeping existing Hub binary (upgrade: deploy --setup-hysteria2-hub)\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := HysteriaInstallError(tc.out); (err == nil) != tc.wantOK {
				t.Fatalf("HysteriaInstallError = %v, want ok=%v", err, tc.wantOK)
			}
		})
	}
}

func haveBash() bool {
	out, err := exec.Command("bash", "-c", "set -o pipefail; echo ok").Output()
	return err == nil && strings.TrimSpace(string(out)) == "ok"
}

// fakeHysteria is a stand-in binary whose `version` output mimics the real one (banner,
// tab-separated fields, a LibVersion line that must not be mistaken for Version).
func fakeHysteria(version string) string {
	return "#!/bin/sh\nprintf 'hysteria banner\\n\\nVersion:\\t" + version +
		"\\nBuildType:\\trelease\\nLibVersion:\\tv0.4.5\\n'\n"
}

// installerCase is one run of an installer script against a temp ./bin/ (see runInstaller).
type installerCase struct {
	name       string
	ensure     bool   // run HysteriaEnsureScript instead of HysteriaInstallScript
	pipefail   bool   // run under set -eo pipefail instead of set -e
	current    string // installed bin/hysteria content, "" = none
	bak        string // pre-existing bin/hysteria.bak content, "" = none
	download   string // what the curl stub "downloads", "" = curl fails
	wantOK     bool
	wantCurl   bool
	wantBinVer string // version of bin/hysteria afterwards ("" absent, "?" unparseable)
	wantBak    string // version in bin/hysteria.bak afterwards ("" absent)
	wantOut    string
}

// TestHysteriaInstallScriptBehaviour runs the installers in a real bash with /usr/local/bin/
// rewritten to a temp ./bin/ and curl replaced by a stub, so nothing outside t.TempDir()
// is touched. Every run is under set -e like the production call sites.
func TestHysteriaInstallScriptBehaviour(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	want := fakeHysteria(HysteriaVersion)
	old := fakeHysteria("v2.6.1")
	unparseable := "#!/bin/sh\necho 'hysteria " + HysteriaVersion + " (new output format)'\n"
	broken := "#!/bin/sh\nexit 1\n"
	keep := "keeping existing Hub binary"

	tests := []installerCase{
		{name: "fresh install", download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantOut: "installed " + HysteriaVersion + " (previous: none"},
		{name: "fresh install under pipefail", pipefail: true, download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantOut: "installed " + HysteriaVersion + " (previous: none"},
		{name: "upgrade keeps bak", current: old, bak: fakeHysteria("v2.5.0"), download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantBak: "v2.6.1", wantOut: "(previous: v2.6.1"},
		{name: "already current", pipefail: true, current: want, wantOK: true,
			wantBinVer: HysteriaVersion, wantOut: HysteriaVersion + " already installed"},
		{name: "download fails", current: old, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: "ERROR: download/verify"},
		{name: "downloaded binary broken", current: old, download: broken, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: "ERROR: download/verify"},
		{name: "downloaded version mismatch", current: old, download: fakeHysteria("v2.12.0"), wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: "ERROR: download/verify"},
		// A changed `version` format must fail loudly, not replace the binary (and .bak) on every run.
		{name: "version output unparseable", current: old, bak: fakeHysteria("v2.5.0"), download: unparseable, wantCurl: true,
			wantBinVer: "v2.6.1", wantBak: "v2.5.0", wantOut: "ERROR: download/verify"},

		{name: "ensure keeps old Hub binary", ensure: true, pipefail: true, current: old, download: want, wantOK: true,
			wantBinVer: "v2.6.1", wantOut: keep},
		{name: "ensure keeps current Hub binary", ensure: true, current: want, wantOK: true,
			wantBinVer: HysteriaVersion, wantOut: keep},
		{name: "ensure installs when absent", ensure: true, pipefail: true, download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantOut: "installed " + HysteriaVersion},
		{name: "ensure fails when absent and download fails", ensure: true, wantCurl: true,
			wantOut: "ERROR: download/verify"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { runInstaller(t, tc) })
	}
}

func runInstaller(t *testing.T, tc installerCase) {
	script := HysteriaInstallScript()
	if tc.ensure {
		script = HysteriaEnsureScript()
	}
	script = strings.ReplaceAll(script, "/usr/local/bin/", "./bin/")
	if strings.Contains(script, "/usr/local/bin") {
		t.Fatal("path rewrite incomplete; refusing to run against the real /usr/local/bin")
	}
	// The stub records its argv and "downloads" ./download, or fails like curl -f when absent.
	const curlStub = `curl() {
  printf '%s\n' "$*" > ./curl.args
  out=""
  while [ $# -gt 0 ]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
  done
  [ -f ./download ] || return 22
  cp ./download "$out"
}
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(bin, "hysteria"):     tc.current,
		filepath.Join(bin, "hysteria.bak"): tc.bak,
		filepath.Join(dir, "download"):     tc.download,
	} {
		if content != "" {
			if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	prelude := "set -e\n"
	if tc.pipefail {
		prelude = "set -eo pipefail\n"
	}
	cmd := exec.Command("bash", "-s")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prelude + curlStub + script + "echo AFTER-INSTALL\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	got := stdout.String() + stderr.String()

	if ok := err == nil; ok != tc.wantOK {
		t.Fatalf("exit ok=%v, want %v (err %v)\n%s", ok, tc.wantOK, err, got)
	}
	// Under set -e a failed install must abort the surrounding provisioning script.
	if after := strings.Contains(got, "AFTER-INSTALL"); after != tc.wantOK {
		t.Errorf("script continued after install=%v, want %v\n%s", after, tc.wantOK, got)
	}
	if !strings.Contains(got, tc.wantOut) {
		t.Errorf("output lacks %q:\n%s", tc.wantOut, got)
	}
	// What setupExitNodeDeploy sees: stdout only (+ the exit status).
	if !tc.ensure {
		if e := HysteriaInstallError(stdout.String()); (e == nil) != tc.wantOK {
			t.Errorf("HysteriaInstallError(stdout) = %v, want ok=%v", e, tc.wantOK)
		}
	}
	args, argsErr := os.ReadFile(filepath.Join(dir, "curl.args"))
	if called := argsErr == nil; called != tc.wantCurl {
		t.Errorf("curl called=%v, want %v", called, tc.wantCurl)
	}
	if argsErr == nil && !strings.Contains(string(args), HysteriaDownloadURL()) {
		t.Errorf("curl args %q lack %s", args, HysteriaDownloadURL())
	}
	if v := binVersion(t, filepath.Join(bin, "hysteria")); v != tc.wantBinVer {
		t.Errorf("bin/hysteria version %q, want %q", v, tc.wantBinVer)
	}
	if v := binVersion(t, filepath.Join(bin, "hysteria.bak")); v != tc.wantBak {
		t.Errorf("bin/hysteria.bak version %q, want %q", v, tc.wantBak)
	}
	for _, leftover := range []string{"hysteria.new", "hysteria.bak.tmp"} {
		if _, err := os.Stat(filepath.Join(bin, leftover)); err == nil {
			t.Errorf("bin/%s left behind", leftover)
		}
	}
}

// binVersion reads the version baked into a fakeHysteria file ("" if the file is absent,
// "?" if it is not a fakeHysteria file).
func binVersion(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "Version:\\t")
	if i < 0 {
		return "?"
	}
	s = s[i+len("Version:\\t"):]
	return s[:strings.Index(s, "\\n")]
}
