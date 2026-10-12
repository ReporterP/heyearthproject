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
		"/usr/local/bin/hysteria.new version",
		"/usr/local/bin/hysteria.bak",
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

func haveBash() bool {
	out, err := exec.Command("bash", "-c", "echo ok").Output()
	return err == nil && strings.TrimSpace(string(out)) == "ok"
}

// fakeHysteria is a stand-in binary whose `version` output mimics the real one (banner,
// tab-separated fields, a LibVersion line that must not be mistaken for Version).
func fakeHysteria(version string) string {
	return "#!/bin/sh\nprintf 'hysteria banner\\n\\nVersion:\\t" + version +
		"\\nBuildType:\\trelease\\nLibVersion:\\tv0.4.5\\n'\n"
}

// TestHysteriaInstallScriptBehaviour runs the installer in a real bash with /usr/local/bin/
// rewritten to a temp ./bin/ and curl replaced by a stub, so nothing outside t.TempDir()
// is touched. It runs under set -e like every production call site.
func TestHysteriaInstallScriptBehaviour(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	script := strings.ReplaceAll(HysteriaInstallScript(), "/usr/local/bin/", "./bin/")
	if strings.Contains(script, "/usr/local/bin") {
		t.Fatal("path rewrite incomplete; refusing to run against the real /usr/local/bin")
	}
	// The stub records its argv and "downloads" ./download (or fails, CURL_FAIL=1).
	const curlStub = `curl() {
  printf '%s\n' "$*" > ./curl.args
  [ "${CURL_FAIL:-}" = 1 ] && return 22
  out=""
  while [ $# -gt 0 ]; do
    case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
  done
  cp ./download "$out"
}
`
	brokenBinary := "#!/bin/sh\nexit 1\n"

	tests := []struct {
		name       string
		current    string // installed binary, "" = none
		download   string
		curlFail   bool
		wantOK     bool
		wantCurl   bool
		wantBinVer string // version of ./bin/hysteria afterwards, "" = absent
		wantBak    string // version in ./bin/hysteria.bak, "" = absent
		wantOut    string
	}{
		{name: "fresh install", download: fakeHysteria(HysteriaVersion), wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantOut: "installed " + HysteriaVersion + " (previous: none"},
		{name: "upgrade keeps bak", current: "v2.6.1", download: fakeHysteria(HysteriaVersion), wantOK: true, wantCurl: true,
			wantBinVer: HysteriaVersion, wantBak: "v2.6.1", wantOut: "(previous: v2.6.1"},
		{name: "already current", current: HysteriaVersion, wantOK: true,
			wantBinVer: HysteriaVersion, wantOut: HysteriaVersion + " already installed"},
		{name: "download fails", current: "v2.6.1", curlFail: true, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: "ERROR: download/verify"},
		{name: "downloaded binary broken", current: "v2.6.1", download: brokenBinary, wantCurl: true,
			wantBinVer: "v2.6.1", wantOut: "ERROR: download/verify"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.current != "" {
				writeExec(t, filepath.Join(bin, "hysteria"), fakeHysteria(tc.current))
			}
			if tc.download != "" {
				writeExec(t, filepath.Join(dir, "download"), tc.download)
			}

			cmd := exec.Command("bash", "-s")
			cmd.Dir = dir
			curlFail := ""
			if tc.curlFail {
				curlFail = "1"
			}
			cmd.Env = append(os.Environ(), "CURL_FAIL="+curlFail)
			cmd.Stdin = strings.NewReader("set -e\n" + curlStub + script + "echo AFTER-INSTALL\n")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			got := out.String()

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
			if _, err := os.Stat(filepath.Join(bin, "hysteria.new")); err == nil {
				t.Error("bin/hysteria.new left behind")
			}
		})
	}
}

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// binVersion reads the version baked into a fakeHysteria file ("" if the file is absent).
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
