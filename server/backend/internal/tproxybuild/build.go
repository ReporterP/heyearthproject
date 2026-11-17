// Package tproxybuild builds the Telegram WEB proxy relay (telegramdesktop/tproxy-server at
// config.TProxyCommit) for the Hub's artifact: the pinned source, its own go test ./... on Linux
// (its tests need Unix file permissions — off Linux they run in a golang container, offline), then
// a static linux/amd64 binary. The result is cached per commit; quick_deploy and
// deploy --ensure-hub-provisioning upload it as artifacts/tproxy-server with its sha256.
package tproxybuild

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// Image is the container the tests and the build run in off Linux.
const Image = "golang:1.25-bookworm"

// Build returns the binary (built or from the cache). logf reports progress.
func Build(logf func(string, ...any)) ([]byte, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	dir := filepath.Join(root, "heyearth", "tproxy-server-"+config.TProxyCommit[:12])
	out := filepath.Join(dir, "out", "tproxy-server")
	if b, err := os.ReadFile(out); err == nil {
		if want, err := os.ReadFile(out + ".sha256"); err == nil && strings.TrimSpace(string(want)) == sum(b) {
			logf("tproxy-server %s из кэша (%s)", config.TProxyCommit[:7], dir)
			return b, nil
		}
	}
	src := filepath.Join(dir, "src")
	if err := checkout(src, logf); err != nil {
		return nil, err
	}
	if err := run(src, nil, "go", "mod", "vendor"); err != nil {
		return nil, fmt.Errorf("go mod vendor: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(out)
	logf("tproxy-server %s: go test ./... и сборка linux/amd64…", config.TProxyCommit[:7])
	if runtime.GOOS == "linux" {
		env := []string{"GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local"}
		if err := run(src, env, "go", "test", "-count=1", "./..."); err != nil {
			return nil, fmt.Errorf("go test ./... tproxy-server: %w", err)
		}
		if err := run(src, append(env, "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "./cmd/tproxy-server"); err != nil {
			return nil, err
		}
	} else {
		// The source goes in on stdin (tar), the binary comes back on stdout: no shared folders
		// (Docker Desktop does not always see files written under %LOCALAPPDATA%). Tests as an
		// unprivileged user (some check that a root-only file is refused), offline.
		var in bytes.Buffer
		if err := tarDir(&in, src); err != nil {
			return nil, err
		}
		script := `set -e; mkdir -p /tmp/w; tar -x -C /tmp/w; useradd -m t >/dev/null 2>&1 || true; chown -R t /tmp/w
su t -c 'export PATH=/usr/local/go/bin:$PATH GOFLAGS=-mod=vendor GOCACHE=/tmp/gc GOPATH=/tmp/gp GOTOOLCHAIN=local; cd /tmp/w && go vet ./... && go test -count=1 ./...' >&2
cd /tmp/w && GOFLAGS=-mod=vendor GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /tmp/tproxy-server ./cmd/tproxy-server >&2
cat /tmp/tproxy-server`
		c := exec.Command("docker", "run", "-i", "--rm", "--network", "none", Image, "sh", "-c", script)
		c.Stdin, c.Stderr = &in, os.Stderr
		bin, err := c.Output()
		if err != nil {
			return nil, fmt.Errorf("тесты и сборка tproxy-server в контейнере %s: %w", Image, err)
		}
		if err := os.WriteFile(out, bin, 0o755); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if len(b) < 1<<20 || string(b[:4]) != "\x7fELF" {
		return nil, errors.New("собранный tproxy-server — не бинарник linux")
	}
	if err := os.WriteFile(out+".sha256", []byte(sum(b)+"\n"), 0o644); err != nil {
		return nil, err
	}
	logf("tproxy-server %s собран: %d байт, sha256 %s", config.TProxyCommit[:7], len(b), sum(b)[:16])
	return b, nil
}

// checkout puts the pinned commit at src (clone once, then only checkout) and checks HEAD.
func checkout(src string, logf func(string, ...any)) error {
	if _, err := os.Stat(filepath.Join(src, ".git")); err != nil {
		logf("клонирую %s…", config.TProxyRepo)
		_ = os.RemoveAll(src)
		if err := run("", nil, "git", "clone", "--quiet", config.TProxyRepo, src); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
	}
	if err := run(src, nil, "git", "checkout", "--quiet", "--force", config.TProxyCommit); err != nil {
		if err := run(src, nil, "git", "fetch", "--quiet", "origin"); err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}
		if err := run(src, nil, "git", "checkout", "--quiet", "--force", config.TProxyCommit); err != nil {
			return fmt.Errorf("git checkout %s: %w", config.TProxyCommit, err)
		}
	}
	head, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(head)) != config.TProxyCommit {
		return fmt.Errorf("в %s HEAD %s, а не %s", src, strings.TrimSpace(string(head)), config.TProxyCommit)
	}
	if out, err := exec.Command("git", "-C", src, "status", "--porcelain", "--untracked-files=no").Output(); err != nil || strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("исходники tproxy-server в %s изменены — удалите каталог и повторите", src)
	}
	return nil
}

// tarDir writes dir (without .git) as a tar stream.
func tarDir(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = rel
		if info.IsDir() {
			h.Name += "/"
			h.Mode = 0o755
		} else {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func run(dir string, env []string, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	return c.Run()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
