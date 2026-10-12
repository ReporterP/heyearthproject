package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHysteriaDownloadURL(t *testing.T) {
	const want = "https://github.com/HyNetworks/hysteria/releases/download/app%2Fv2.13.0/hysteria-linux-amd64"
	if u := HysteriaDownloadURL(); u != want {
		t.Fatalf("HysteriaDownloadURL() = %q, want %q", u, want)
	}
}

func TestHysteriaSHA256Shape(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(HysteriaSHA256) {
		t.Fatalf("HysteriaSHA256 %q is not 64 lowercase hex chars", HysteriaSHA256)
	}
}

func TestHysteriaInstallScript(t *testing.T) {
	s := HysteriaInstallScript()
	for _, want := range []string{
		`HY_WANT="` + HysteriaVersion + `"`,
		`HY_SHA256="` + HysteriaSHA256 + `"`, // production rendering pins the constant
		HysteriaDownloadURL(),
		"--connect-timeout 15 --max-time 300 --retry 3 --retry-delay 2",
		`echo "$HY_SHA256  /usr/local/bin/hysteria.new" | sha256sum -c --quiet`,
		`[ "$(/usr/local/bin/hysteria.new version 2>/dev/null | awk '$1=="Version:"{print $2}')" = "$HY_WANT" ]`,
		`|| HY_CUR=""`,
		"HY_REPLACED=0\n",
		"HY_REPLACED=1\n",
		"mv -f /usr/local/bin/hysteria.bak.tmp /usr/local/bin/hysteria.bak",
		"mv -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// Integrity first: nothing may make the download executable or run it before the hash check.
	iSHA := strings.Index(s, "sha256sum -c")
	iChmod := strings.Index(s, "chmod +x /usr/local/bin/hysteria.new")
	iExec := strings.Index(s, "/usr/local/bin/hysteria.new version")
	if iSHA < 0 || iSHA > iChmod || iChmod > iExec {
		t.Errorf("order sha256sum(%d) < chmod(%d) < exec(%d) violated", iSHA, iChmod, iExec)
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
	if !strings.HasPrefix(s, "if [ -x /usr/local/bin/hysteria ] && /usr/local/bin/hysteria version >/dev/null 2>&1; then\n") {
		t.Error("ensure script must keep only a binary that exists and runs")
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
		{"error line", "[hysteria] ERROR: download/sha256/verify of " + HysteriaVersion + " failed, binary unchanged\n", false},
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

// markingHysteria is fakeHysteria that also leaves ./download-executed when it is run, so a
// test can tell whether the installer executed the download.
func markingHysteria(version string) string {
	return strings.Replace(fakeHysteria(version), "#!/bin/sh\n", "#!/bin/sh\ntouch ./download-executed\n", 1)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// installerCase is one run of an installer script against a temp ./bin/ (see runInstaller).
type installerCase struct {
	name         string
	ensure       bool   // run the ensure script instead of the install script
	pipefail     bool   // run under set -eo pipefail instead of set -e
	current      string // installed bin/hysteria content, "" = none
	bak          string // pre-existing bin/hysteria.bak content, "" = none
	download     string // what the curl stub "downloads", "" = curl fails
	wrongSHA     bool   // render the installer with a hash that does not match the download
	wantOK       bool
	wantCurl     bool
	wantBinVer   string // version of bin/hysteria afterwards ("" absent, "?" not a fake)
	wantBak      string // version in bin/hysteria.bak afterwards ("" absent, "?" not a fake)
	wantReplaced string // HY_REPLACED after a successful run ("unset" when ensure kept the binary)
	wantMarker   string // "yes"/"no": was the download executed; "" = don't care
	wantOut      string
}

// TestHysteriaInstallScriptBehaviour runs the installers in a real bash with /usr/local/bin/
// rewritten to a temp ./bin/ and curl replaced by a stub, so nothing outside t.TempDir()
// is touched. Every run is under set -e like the production call sites. The installer is
// rendered with the sha256 of the stub download (production pins HysteriaSHA256).
func TestHysteriaInstallScriptBehaviour(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	want := fakeHysteria(HysteriaVersion)
	old := fakeHysteria("v2.6.1")
	unparseable := "#!/bin/sh\necho 'hysteria " + HysteriaVersion + " (new output format)'\n"
	broken := "#!/bin/sh\nexit 1\n"
	keep := "keeping existing Hub binary"
	failed := "ERROR: download/sha256/verify"

	tests := []installerCase{
		{name: "fresh install", download: markingHysteria(HysteriaVersion), wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantReplaced: "1", wantMarker: "yes", wantOut: "installed " + HysteriaVersion + " (previous: none"},
		{name: "fresh install under pipefail", pipefail: true, download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantReplaced: "1", wantOut: "installed " + HysteriaVersion + " (previous: none"},
		{name: "upgrade keeps bak", current: old, bak: fakeHysteria("v2.5.0"), download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantBak: "v2.6.1", wantReplaced: "1", wantOut: "(previous: v2.6.1"},
		{name: "already current", pipefail: true, current: want, wantOK: true,
			wantBinVer: HysteriaVersion, wantReplaced: "0", wantOut: HysteriaVersion + " already installed"},
		{name: "download fails", current: old, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: failed},
		// Integrity before execution: a download with the wrong hash is never chmod'ed or run.
		{name: "sha256 mismatch never executes the download", current: old, bak: fakeHysteria("v2.5.0"),
			download: markingHysteria(HysteriaVersion), wrongSHA: true, wantCurl: true,
			wantBinVer: "v2.6.1", wantBak: "v2.5.0", wantMarker: "no", wantOut: failed},
		{name: "downloaded binary broken", current: old, download: broken, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: failed},
		{name: "downloaded version mismatch", current: old, download: fakeHysteria("v2.12.0"), wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: failed},
		// A changed `version` format must fail loudly, not replace the binary (and .bak) on every run.
		{name: "version output unparseable", current: old, bak: fakeHysteria("v2.5.0"), download: unparseable, wantCurl: true,
			wantBinVer: "v2.6.1", wantBak: "v2.5.0", wantOut: failed},

		{name: "ensure keeps old Hub binary", ensure: true, pipefail: true, current: old, download: want, wantOK: true,
			wantBinVer: "v2.6.1", wantReplaced: "unset", wantOut: keep},
		{name: "ensure keeps current Hub binary", ensure: true, current: want, wantOK: true,
			wantBinVer: HysteriaVersion, wantReplaced: "unset", wantOut: keep},
		{name: "ensure installs when absent", ensure: true, pipefail: true, download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantReplaced: "1", wantOut: "installed " + HysteriaVersion},
		{name: "ensure replaces a binary that does not run", ensure: true, current: broken, download: want, wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantBak: "?", wantReplaced: "1", wantOut: "installed " + HysteriaVersion},
		{name: "ensure fails when absent and download fails", ensure: true, wantCurl: true,
			wantOut: failed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { runInstaller(t, tc) })
	}
}

func runInstaller(t *testing.T, tc installerCase) {
	sha := sha256Hex(tc.download)
	if tc.wrongSHA {
		sha = sha256Hex(tc.download + "tampered")
	}
	script := hysteriaInstallScript(sha)
	if tc.ensure {
		script = hysteriaEnsureScript(script)
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
	cmd.Stdin = strings.NewReader(prelude + curlStub + script +
		"echo \"HY_REPLACED=${HY_REPLACED:-unset}\"\necho AFTER-INSTALL\n")
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
	if tc.wantOK && !strings.Contains(got, "HY_REPLACED="+tc.wantReplaced+"\n") {
		t.Errorf("want HY_REPLACED=%s:\n%s", tc.wantReplaced, got)
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
	if tc.wantMarker != "" {
		_, statErr := os.Stat(filepath.Join(dir, "download-executed"))
		if executed := statErr == nil; executed != (tc.wantMarker == "yes") {
			t.Errorf("download executed=%v, want %s", executed, tc.wantMarker)
		}
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
