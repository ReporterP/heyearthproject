package provision

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/heyearth/backend/internal/config"
)

// The tunnel keys live on the exit (config.ExitKeysPath, 0600): the Reality key pair, the short
// id, the VLESS UUID and the hysteria password. The database keeps the public parts and the
// password (save_tunnel), never the private key. Missing parts are generated here, in Go
// (X25519 through crypto/ecdh — no parsing of `xray x25519`, whose output format changed).

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortIDRe  = regexp.MustCompile(`^([0-9a-f]{2}){1,8}$`)
	tunnelPwRe = regexp.MustCompile(`^[A-Za-z0-9+/=]{16,}$`)
)

// errKeysCorrupt: the keys file is there but is not JSON — never overwritten.
var errKeysCorrupt = errors.New("файл ключей " + config.ExitKeysPath + " не читается как JSON")

// readKeys reads the keys file; ok false when it is absent or empty. It is all secrets and none
// of them is known to the run's redaction before it is parsed, so it goes through readSecretFile.
func readKeys(ctx context.Context, r Remote) (config.ExitRealityKeys, bool, error) {
	var k config.ExitRealityKeys
	out, err := readSecretFile(ctx, r, config.ExitKeysPath, "файл ключей туннеля")
	if err != nil || strings.TrimSpace(out) == "" {
		return k, false, err
	}
	if err := json.Unmarshal([]byte(out), &k); err != nil {
		return config.ExitRealityKeys{}, true, errKeysCorrupt // json's error quotes the content
	}
	return k, true, nil
}

// secretReadKinds are the error kinds a failed read keeps (the runner's inconclusive, the
// bestEffort branches of deprovision); nothing else of the failed Run survives.
var secretReadKinds = []error{context.Canceled, context.DeadlineExceeded, ErrNodeUnreachable, ErrHostKeyNotConfirmed,
	ErrNoCredentials, ErrHostKeyMismatch, ErrConnectionLost}

// readSecretFile prints a file whose content must never reach an error, a step's output or the
// log (config.CatScript; "" when absent). A failed Run's error is dropped, not wrapped: the SSH
// remote quotes the output tail in it — an SSH drop right after `cat` would carry the whole file.
// The returned error names the file and keeps only the kinds of secretReadKinds.
func readSecretFile(ctx context.Context, r Remote, path, what string) (string, error) {
	out, err := r.Run(ctx, config.CatScript(path))
	if err == nil {
		return out, nil
	}
	format, args := "", []any{}
	for _, kind := range secretReadKinds {
		if errors.Is(err, kind) {
			format += "%w: "
			args = append(args, kind)
		}
	}
	if len(args) == 0 {
		format += "команда на ноде завершилась с ошибкой: "
	}
	return "", fmt.Errorf(format+"%s %s не прочитан (вывод не показан: в нём секреты)", append(args, what, path)...)
}

// keysVerify: the file is there with valid keys and is readable by root only. Valid keys are
// taken into the run even when only the mode is wrong, so the later steps (and a verify job)
// compare against the node's real keys.
func keysVerify(ctx context.Context, r Remote, s *State) error {
	k, ok, err := readKeys(ctx, r)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("файла ключей туннеля ещё нет")
	}
	if err := validKeys(k); err != nil {
		return err
	}
	s.Keys = k
	states, err := remoteStates(ctx, r, []File{{Path: config.ExitKeysPath}})
	if err != nil {
		return err
	}
	if st, ok := states[config.ExitKeysPath]; !ok || !privateMode(st.mode) {
		return fmt.Errorf("файл ключей %s доступен не только root (права %s)", config.ExitKeysPath, orNone(st.mode))
	}
	return nil
}

// keysApply keeps every existing key (exit 2 keeps its tunnel) and only adds missing ones; a file
// that cannot be read is never overwritten.
func keysApply(ctx context.Context, r Remote, s *State) error {
	k, _, err := readKeys(ctx, r)
	switch {
	case errors.Is(err, errKeysCorrupt):
		return fmt.Errorf("%w — не перезаписываю, нужна ручная проверка", err)
	case err != nil:
		return err
	}
	if k, err = completeKeys(k); err != nil {
		return err
	}
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	f := File{config.ExitKeysPath, append(b, '\n'), 0o600}
	states, err := remoteStates(ctx, r, []File{f})
	if err != nil {
		return err
	}
	if err := prepareWrites(ctx, r, s, "", []File{f}, states); err != nil {
		return err
	}
	if _, err := r.PutFile(ctx, f.Path, f.Data, f.Mode); err != nil {
		return err
	}
	s.Keys = k
	s.Printf("ключи туннеля на месте (открытый ключ %s…)", k.PublicKey[:8])
	return nil
}

func derivePublic(priv string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil || len(raw) != 32 {
		return "", errors.New("закрытый ключ Reality повреждён")
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", errors.New("закрытый ключ Reality повреждён")
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// completeKeys generates what is missing and derives the public key from the private one.
func completeKeys(k config.ExitRealityKeys) (config.ExitRealityKeys, error) {
	if k.PrivateKey == "" {
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return k, err
		}
		k.PrivateKey = base64.RawURLEncoding.EncodeToString(priv.Bytes())
	}
	pub, err := derivePublic(k.PrivateKey)
	if err != nil {
		return k, err
	}
	k.PublicKey = pub
	if k.ShortID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return k, err
		}
		k.ShortID = hex.EncodeToString(b)
	}
	if k.UUID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return k, err
		}
		b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
		k.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	}
	if k.SSPassword == "" {
		if k.SSPassword, err = randomPassword(); err != nil {
			return k, err
		}
	}
	return k, validKeys(k)
}

func validKeys(k config.ExitRealityKeys) error {
	pub, err := derivePublic(k.PrivateKey)
	switch {
	case err != nil:
		return err
	case pub != k.PublicKey:
		return errors.New("открытый ключ Reality не соответствует закрытому")
	case !uuidRe.MatchString(k.UUID):
		return errors.New("UUID туннеля некорректен")
	case !shortIDRe.MatchString(k.ShortID):
		return errors.New("shortId некорректен")
	case !tunnelPwRe.MatchString(k.SSPassword):
		return errors.New("пароль туннеля некорректен")
	}
	return nil
}
