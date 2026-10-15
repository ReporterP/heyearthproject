package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

// EnsureHubKey creates the Hub's ed25519 key once (on the Hub, through hub: the API passes
// LocalRemote, the CLI its SSH session to the Hub) and returns its authorized_keys line,
// re-marshalled without a comment ("ssh-ed25519 AAAA…", safe inside single quotes). The private
// key never leaves the Hub: the script prints only the public line.
func EnsureHubKey(ctx context.Context, hub Remote) (string, error) {
	out, err := hub.Run(ctx, config.HubSSHKeyEnsureScript)
	if err != nil {
		return "", fmt.Errorf("ключ Hub: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(lines[len(lines)-1]))
	if err != nil {
		return "", fmt.Errorf("публичный ключ Hub не читается: %w", err)
	}
	if pub.Type() != ssh.KeyAlgoED25519 {
		return "", fmt.Errorf("ключ Hub должен быть ed25519, а не %s", pub.Type())
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), nil
}

// HubKeyFingerprint is the SHA256 fingerprint ("SHA256:…") of an authorized_keys line, as shown
// on «SSH-ключи»; "" when the line is not a public key.
func HubKeyFingerprint(line string) string {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pub)
}
