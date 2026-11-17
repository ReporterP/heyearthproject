//go:build ignore

// Деплой admin_web или сайта одним архивом через SSH pipe (SFTP по многим мелким файлам обрывается).
// go run quick_admin_web.go         — админка (admin.hiearth.ru)
// go run quick_admin_web.go -site   — сайт и кабинет (hiearth.ru)
//
// Собирает (npm run build), отправляет dist одним tar.gz в stdin SSH-сессии, распаковывает
// рядом и меняет каталог атомарно: при обрыве или ошибке сборки старая версия остаётся на месте.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	site := flag.Bool("site", false, "deploy the site (site/) instead of the admin (admin_web/)")
	flag.Parse()
	appDir, webRoot, vhost := "admin_web", "/var/www/heyearth", "admin.hiearth.ru"
	if *site {
		appDir, webRoot, vhost = "site", "/var/www/heyearth-site", "hiearth.ru"
	}

	_, thisFile, _, _ := runtime.Caller(0)
	backendRoot := filepath.Dir(thisFile)
	serverRoot := filepath.Dir(backendRoot)
	repoRoot := filepath.Dir(serverRoot)
	env := readEnv(filepath.Join(serverRoot, ".env"))
	if (env["DEPLOY_SSH_PASSWORD"] == "" && env["DEPLOY_SSH_KEY_FILE"] == "") || env["DEPLOY_HOST"] == "" {
		log.Fatal("DEPLOY_SSH_KEY_FILE or DEPLOY_SSH_PASSWORD, and DEPLOY_HOST, must be in server/.env")
	}
	auth := []ssh.AuthMethod{}
	if kf := strings.TrimSpace(env["DEPLOY_SSH_KEY_FILE"]); kf != "" {
		// The deploy key (outside the repo) is tried before the password.
		data, err := os.ReadFile(kf)
		if err != nil {
			log.Fatalf("DEPLOY_SSH_KEY_FILE: %v", err)
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			log.Fatalf("DEPLOY_SSH_KEY_FILE %s: %v (passphrase-protected keys are not supported)", kf, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if env["DEPLOY_SSH_PASSWORD"] != "" {
		auth = append(auth, ssh.Password(env["DEPLOY_SSH_PASSWORD"]))
	}
	// The root password never goes to an unverified host (same pin as quick_deploy.go).
	fp := env["DEPLOY_HOST_KEY_FINGERPRINT"]
	if !strings.HasPrefix(fp, "SHA256:") {
		log.Fatal("DEPLOY_HOST_KEY_FINGERPRINT (SHA256:...) not found in server/.env")
	}
	port := env["DEPLOY_PORT"]
	if port == "" {
		port = "22"
	}

	fmt.Printf("[1/3] Сборка %s...\n", appDir)
	webDir := filepath.Join(repoRoot, appDir)
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	cmd := exec.Command(npm, "run", "build")
	cmd.Dir = webDir
	cmd.Env = filterEnv(os.Environ(), "VITE_API_BASE") // both apps use relative /api
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("npm run build: %v", err)
	}
	archive, n, err := tarGz(filepath.Join(webDir, "dist"))
	if err != nil {
		log.Fatalf("archive dist: %v", err)
	}
	fmt.Printf("    %d файлов, архив %d КБ\n", n, len(archive)/1024)

	fmt.Println("[2/3] Загрузка через SSH pipe...")
	client, err := dialRetry(net.JoinHostPort(env["DEPLOY_HOST"], port), &ssh.ClientConfig{
		User: "root",
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != fp {
				return fmt.Errorf("host key mismatch: got %s, want %s", got, fp)
			}
			return nil
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()
	stamp := time.Now().Unix()
	newDir := fmt.Sprintf("%s.new-%d", webRoot, stamp)
	script := fmt.Sprintf(`set -e
rm -rf %[1]s.new-*
mkdir -p %[2]s
tar -xz -C %[2]s
[ -f %[2]s/index.html ] || { echo "в архиве нет index.html"; rm -rf %[2]s; exit 3; }
chmod -R 755 %[2]s
rm -rf %[1]s.old
[ -d %[1]s ] && mv %[1]s %[1]s.old
mv %[2]s %[1]s
nginx -t >/dev/null 2>&1 && systemctl reload nginx
echo "files: $(find %[1]s -type f | wc -l)"`, webRoot, newDir)
	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("SSH session: %v", err)
	}
	session.Stdin = bytes.NewReader(archive)
	out, err := session.CombinedOutput(script)
	session.Close()
	if err != nil {
		log.Fatalf("upload: %v\n%s", err, out)
	}
	fmt.Print("    ", string(out))

	fmt.Println("[3/3] Проверка...")
	check, _ := client.NewSession()
	res, _ := check.CombinedOutput("curl -s -o /dev/null -w '%{http_code}' -H 'Host: " + vhost + "' -k https://127.0.0.1:8443/")
	check.Close()
	fmt.Printf("    %s: HTTP %s\n", vhost, strings.TrimSpace(string(res)))
	fmt.Println("Готово.")
}

func dialRetry(addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	var last error
	for i := 0; i < 4; i++ {
		c, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			return c, nil
		}
		last = err
		fmt.Printf("    SSH: %v — повтор через 5 с\n", err)
		time.Sleep(5 * time.Second)
	}
	return nil, last
}

func filterEnv(env []string, drop string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, drop+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func readEnv(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// tarGz packs dir (paths relative to it, forward slashes).
func tarGz(dir string) ([]byte, int, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		n++
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	return buf.Bytes(), n, err
}
