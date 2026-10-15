package provision

import (
	"encoding/base64"
	"fmt"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestValidFingerprint(t *testing.T) {
	for fp, want := range map[string]bool{
		"SHA256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK4":  true,
		"SHA256:U/h/uPHx124SwCoeOtDvFPFF5pk7XzHQcb3LOs4/jFE":  true,
		"SHA256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK4=": false, // padded
		"SHA256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK":   false, // short
		"sha256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK4":  false,
		" SHA256:0zi2HelHDx9e+XO42StXNstdMAogpgutOWC86PQvaK4": false,
		"MD5:16:27:ac:a5:76:28:2d:36:63:1b:56:4d:eb:df:a6:48": false,
		"": false,
	} {
		if got := ValidFingerprint(fp); got != want {
			t.Errorf("ValidFingerprint(%q) = %v, want %v", fp, got, want)
		}
	}
}

func TestHostKeyAlgorithms(t *testing.T) {
	for keyType, want := range map[string]string{
		ssh.KeyAlgoED25519:                 "[ssh-ed25519]",
		ssh.KeyAlgoECDSA256:                "[ecdsa-sha2-nistp256]",
		ssh.KeyAlgoECDSA384:                "[ecdsa-sha2-nistp384]",
		ssh.KeyAlgoECDSA521:                "[ecdsa-sha2-nistp521]",
		ssh.KeyAlgoRSA:                     "[rsa-sha2-512 rsa-sha2-256]", // never SHA-1 ssh-rsa signatures
		"ssh-dss":                          "error",
		"ssh-ed25519-cert-v01@openssh.com": "error",
		"":                                 "error",
	} {
		algos, err := hostKeyAlgorithms(keyType)
		got := fmt.Sprint(algos)
		if err != nil {
			got = "error"
		}
		if got != want {
			t.Errorf("hostKeyAlgorithms(%q) = %s, want %s", keyType, got, want)
		}
	}
}

func TestStoredHostKeyType(t *testing.T) {
	ec := newSigner(t, "ecdsa256").PublicKey()
	ed := newSigner(t, "ed25519").PublicKey()
	blob := base64.StdEncoding.EncodeToString(ec.Marshal()) // as the old TOFU stored servers.ssh_host_key
	for _, tc := range []struct{ name, blob, fp, want string }{
		{"matching ecdsa", blob, ssh.FingerprintSHA256(ec), ssh.KeyAlgoECDSA256},
		{"matching, surrounding space", " " + blob + "\n", ssh.FingerprintSHA256(ec), ssh.KeyAlgoECDSA256},
		{"blob of another key", blob, ssh.FingerprintSHA256(ed), ""},
		{"not base64", "%%%", ssh.FingerprintSHA256(ec), ""},
		{"not a key", base64.StdEncoding.EncodeToString([]byte("garbage")), ssh.FingerprintSHA256(ec), ""},
		{"empty", "", ssh.FingerprintSHA256(ec), ""},
	} {
		if got := StoredHostKeyType(tc.blob, tc.fp); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
