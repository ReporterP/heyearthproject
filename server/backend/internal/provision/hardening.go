package provision

import (
	"context"
	"errors"

	"github.com/heyearth/backend/internal/config"
)

// hostHardeningStep: SYN-flood sysctl, security auto-updates, sshd limits and (jail=true, exits)
// fail2ban for SSH — config/node_hardening.go. The entry recipe keeps its own jail (relay_fail2ban).
// On exits the Hub is not exempt from the jail: it only fails a login when somebody brute-forces the
// exit through the Hub, and it authenticates with its key otherwise.
func hostHardeningStep(jail bool) Step {
	return Step{Name: "host_hardening", Apply: hostHardeningApply(jail), Verify: hostHardeningVerify(jail)}
}

func hostHardeningFiles(jail bool, s *State) []File {
	port := 22
	if s != nil && s.Node.SSHPort > 0 {
		port = s.Node.SSHPort
	}
	var fs []File
	for _, f := range config.NodeHardeningFiles(jail, port, "") {
		fs = append(fs, File{f.Path, []byte(f.Data), f.Mode})
	}
	return fs
}

func hostHardeningApply(jail bool) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		for _, f := range hostHardeningFiles(jail, s) {
			if _, err := r.PutFile(ctx, f.Path, f.Data, f.Mode); err != nil {
				return err
			}
		}
		return runPrint(config.NodeHardeningApplyScript(jail))(ctx, r, s)
	}
}

func hostHardeningVerify(jail bool) stepFunc {
	return func(ctx context.Context, r Remote, s *State) error {
		if err := filesMatch(ctx, r, hostHardeningFiles(jail, s)); err != nil {
			return err
		}
		kv, err := runKV(ctx, r, config.NodeHardeningCheckScript)
		if err != nil {
			return err
		}
		if kv["SYN"] != "8192" || kv["PERSOURCE"] != "3" || kv["TIMER"] != "enabled" {
			return errors.New("защита узла не применена (sysctl / sshd / автообновления)")
		}
		return nil
	}
}
