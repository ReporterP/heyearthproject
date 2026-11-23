package main

import (
	"strings"
	"testing"
)

const testPulseURL = "https://pulse.example.test/ping/0f0f0f0f-1111-2222-3333-444444444444"

// The Hub's pulse: a timer opens the outside watchdog's address once a minute. The address is
// a secret (whoever knows it can keep the pulse alive): it lives in pulse.env, mode 600.
func TestPulseUnits(t *testing.T) {
	if !strings.Contains(pulseTimerUnit, "OnCalendar=minutely") || !strings.Contains(pulseTimerUnit, "WantedBy=timers.target") {
		t.Errorf("timer: %q", pulseTimerUnit)
	}
	if !strings.Contains(pulseServiceUnit, "Type=oneshot") || !strings.Contains(pulseServiceUnit, "ExecStart=/opt/heyearth/bin/heyearth-pulse.sh") {
		t.Errorf("service: %q", pulseServiceUnit)
	}
	for _, want := range []string{"curl -fsS -m 10 --retry 2", ". /etc/heyearth/pulse.env", `"$PULSE_URL"`} {
		if !strings.Contains(pulseScript, want) {
			t.Errorf("pulse script lacks %q", want)
		}
	}
	s := renderPulseInstallScript(testPulseURL)
	for path, content := range map[string]string{
		"/etc/heyearth/pulse.env":                    "PULSE_URL='" + testPulseURL + "'\n",
		"/opt/heyearth/bin/heyearth-pulse.sh":        pulseScript,
		"/etc/systemd/system/heyearth-pulse.service": pulseServiceUnit,
		"/etc/systemd/system/heyearth-pulse.timer":   pulseTimerUnit,
	} {
		if got := payloadFrom(t, s, path); got != content {
			t.Errorf("payload of %s does not round-trip: %q", path, got)
		}
	}
	if !strings.Contains(s, "put_file /etc/heyearth/pulse.env 600 ") {
		t.Error("pulse.env must be mode 600")
	}
	// the address itself is nowhere but the env payload: not in the units, not in the script,
	// not in anything the install prints
	for name, text := range map[string]string{"timer": pulseTimerUnit, "service": pulseServiceUnit, "script": pulseScript, "install script": s} {
		if strings.Contains(text, "pulse.example.test") || strings.Contains(text, "0f0f0f0f") {
			t.Errorf("the pulse address is readable in the %s", name)
		}
	}
	for _, want := range []string{
		"systemd-analyze verify /etc/systemd/system/heyearth-pulse.service /etc/systemd/system/heyearth-pulse.timer",
		"systemctl daemon-reload", "systemctl enable --now heyearth-pulse.timer", "systemctl start heyearth-pulse.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	assertNoVPNRestart(t, "hub pulse", s)
	if haveBash() {
		if out, exit := runBashStdin(t, s, "-n"); exit != 0 {
			t.Errorf("bash -n failed (exit %d): %s", exit, out)
		}
		if out, exit := runBashStdin(t, pulseScript, "-n"); exit != 0 {
			t.Errorf("pulse script: bash -n failed (exit %d): %s", exit, out)
		}
	}
}

// Without WATCHDOG_PULSE_URL there is nothing to install: setup-ops has no pulse step.
func TestSetupOpsSkipsPulseWithoutURL(t *testing.T) {
	if s := renderPulseInstallScript(""); strings.Contains(s, "heyearth-pulse") || s != "" {
		t.Errorf("an install script without an address: %q", s)
	}
	if _, ok := pulseStep(""); ok {
		t.Error("a pulse step without an address")
	}
	if _, ok := pulseStep("   "); ok {
		t.Error("a pulse step for a blank address")
	}
	if script, ok := pulseStep(testPulseURL); !ok || !strings.Contains(script, "heyearth-pulse") {
		t.Error("no pulse step although the address is set")
	}
}

func TestValidatePulseURL(t *testing.T) {
	if err := validatePulseURL(testPulseURL); err != nil {
		t.Errorf("a good address: %v", err)
	}
	for _, bad := range []string{"http://pulse.example.test/ping/x", "pulse.example.test/ping", "https://pulse.example.test/ping/a b", "https://pulse.example.test/'; rm -rf /", "https://pulse.example.test/$(id)", "https://x"} {
		if err := validatePulseURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
