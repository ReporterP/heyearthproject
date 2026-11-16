package tgweb

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// fakeHub is the Hub's files for WriteBackends / exitsCheck.
type fakeHub struct{ files map[string][]byte }

func (h *fakeHub) Run(_ context.Context, s string) (string, error) {
	if strings.HasPrefix(s, "cat ") {
		return string(h.files[strings.Fields(s)[1]]), nil
	}
	return "", nil
}

func (h *fakeHub) PutFile(_ context.Context, p string, d []byte, _ uint32) (bool, error) {
	changed := string(h.files[p]) != string(d)
	h.files[p] = d
	return changed, nil
}

func fakeExits(t *testing.T, m map[string]string, down map[string]bool) {
	t.Helper()
	fakeSettings(t, m)
	oe, od := onlineExits, socksDial
	t.Cleanup(func() { onlineExits, socksDial = oe, od })
	onlineExits = func(context.Context) ([]biz.ExitNodeTunnel, error) {
		return []biz.ExitNodeTunnel{{ID: 3, IPAddress: "198.51.100.3"}, {ID: 2, IPAddress: "198.51.100.2"}}, nil
	}
	socksDial = func(_ context.Context, socks, addr string) (net.Conn, error) {
		if down[socks] || down[addr] {
			return nil, errors.New("down")
		}
		a, _ := net.Pipe()
		return a, nil
	}
}

func TestExitsAndForwarderList(t *testing.T) {
	m := map[string]string{ExitKey(2): SecretTag(sec), ExitKey(3): SecretTag("old-secret")}
	down := map[string]bool{"127.0.0.1:1090": true} // exit 2: UDP choked, ShadowTLS answers
	fakeExits(t, m, down)
	ex, err := Exits(context.Background(), sec)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex) != 2 || ex[0].ID != 2 || !ex[0].Installed || !ex[0].Reachable || ex[0].Path != "stls" || ex[1].Installed || ex[1].Reachable {
		t.Fatalf("%+v", ex)
	}
	hub := &fakeHub{files: map[string][]byte{}}
	check := exitsCheck(sec)
	if miss, _ := check(context.Background(), hub); !strings.Contains(miss, "#3") {
		t.Fatalf("exit 3 runs an old secret: %q", miss)
	}
	m[ExitKey(3)] = SecretTag(sec)
	if miss, _ := check(context.Background(), hub); !strings.Contains(miss, "устарел") {
		t.Fatalf("list not written yet: %q", miss)
	}
	ex, _ = Exits(context.Background(), sec)
	if n, err := WriteBackends(context.Background(), hub, ex); err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if miss, _ := check(context.Background(), hub); miss != "" {
		t.Fatalf("converged, yet %q", miss)
	}
	list := string(hub.files[config.TGFwdBackendsPath])
	if !strings.Contains(list, "198.51.100.2:3398") || !strings.Contains(list, "127.0.0.1:2091") {
		t.Fatalf("list:\n%s", list)
	}
	// Both tunnels of every exit down: nothing serves.
	down["127.0.0.1:2090"], down["127.0.0.1:1091"], down["127.0.0.1:2091"] = true, true, true
	if miss, _ := check(context.Background(), hub); !strings.Contains(miss, "через туннель") {
		t.Fatalf("no reachable exit: %q", miss)
	}
}

func TestExitOptionsFollowSettings(t *testing.T) {
	m := map[string]string{KeyEnabled: "false", KeySecret: sec}
	fakeSettings(t, m)
	if o, err := ExitOptions(context.Background(), 2); err != nil || o != nil {
		t.Fatal("switched off: no exit step")
	}
	m[KeyEnabled] = "true"
	o, err := ExitOptions(context.Background(), 2)
	if err != nil || o == nil || o.Secret != sec {
		t.Fatalf("%+v %v", o, err)
	}
	if err := o.Record(context.Background()); err != nil || m[ExitKey(2)] != SecretTag(sec) {
		t.Fatal("record")
	}
	if strings.Contains(SecretTag(sec), sec[:8]) {
		t.Fatal("the tag must not reveal the secret")
	}
}

var _ provision.Remote = (*fakeHub)(nil)
