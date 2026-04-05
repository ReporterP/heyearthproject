package admin

import (
	"encoding/base64"
	"fmt"
	"net"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

// toFUHostKeyCallback returns a host-key callback implementing Trust-On-First-Use.
//
// First connection to a server (ssh_host_key IS NULL in DB): any key is accepted
// and stored. Subsequent connections: key must match the stored value.
//
// serverID == 0 falls back to InsecureIgnoreHostKey (used when server is not yet
// in the DB, e.g. during initial add).
func toFUHostKeyCallback(serverID int) ssh.HostKeyCallback {
	if serverID == 0 {
		return ssh.InsecureIgnoreHostKey() //nolint:gosec -- server not yet in DB
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		var stored *string
		row := db.DB.QueryRow(`SELECT ssh_host_key FROM servers WHERE id = $1`, serverID)
		var s string
		err := row.Scan(&s)
		if err != nil {
			// Row not found or scan error — accept and trust first key.
			_ = saveHostKey(serverID, key)
			return nil
		}
		stored = &s

		if *stored == "" {
			// First connection: trust and persist.
			return saveHostKey(serverID, key)
		}

		// Verify against stored key.
		want, err := base64.StdEncoding.DecodeString(*stored)
		if err != nil {
			return fmt.Errorf("ssh tofu: stored host key is corrupt: %w", err)
		}
		got := key.Marshal()
		if len(want) != len(got) {
			return fmt.Errorf("ssh tofu: host key mismatch for server %d (length %d != %d)", serverID, len(got), len(want))
		}
		for i := range want {
			if want[i] != got[i] {
				return fmt.Errorf("ssh tofu: host key mismatch for server %d — possible MITM attack", serverID)
			}
		}
		return nil
	}
}

func saveHostKey(serverID int, key ssh.PublicKey) error {
	encoded := base64.StdEncoding.EncodeToString(key.Marshal())
	_, err := db.DB.Exec(`UPDATE servers SET ssh_host_key = $1 WHERE id = $2`, encoded, serverID)
	return err
}
