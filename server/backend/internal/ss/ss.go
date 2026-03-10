package ss

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const scriptTimeout = 35 * time.Second

// GetOrCreateURI runs add-ss-user.sh with instance (and optional "new") and returns ss:// URI.
func GetOrCreateURI(instance string, forceNew bool) (string, error) {
	appDir := os.Getenv("APP_DIR")
	if appDir == "" {
		appDir = "/opt/vpn-bot"
	}
	script := filepath.Join(appDir, "add-ss-user.sh")
	if _, err := os.Stat(script); err != nil {
		return "", err
	}
	args := []string{script, instance}
	if forceNew {
		args = append(args, "new")
	}
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = "/"
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("timeout running add-ss-user.sh: %w", err)
		}
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	uri := strings.TrimSpace(string(out))
	if !strings.HasPrefix(uri, "ss://") {
		return "", errors.New("invalid SS response")
	}
	return uri, nil
}

// RunWithTimeout runs the script with a timeout (for API/bot).
func RunWithTimeout(instance string, forceNew bool, _ time.Duration) (string, error) {
	return GetOrCreateURI(instance, forceNew)
}
