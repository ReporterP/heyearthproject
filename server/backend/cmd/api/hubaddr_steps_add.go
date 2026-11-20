package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/heyearth/backend/internal/hubnet"
)

// Step 1 «Добавить адрес»: the new address on the default-route interface (runtime) and in the
// netplan file that lists its addresses (backup once, `netplan generate`, never apply).

type hubAddrAddChecks struct {
	AddedRuntime   bool   `json:"added_runtime"` // the wizard put it on the interface (cancel removes it)
	NetplanPath    string `json:"netplan_path"`
	NetplanChanged bool   `json:"netplan_changed"`
	Backup         string `json:"backup"`
	Subnet         string `json:"subnet"` // /24 of the new address, shown next to the current one's
	CurSubnet      string `json:"cur_subnet"`
}

func hubNetplanBackup(path string, id int64) string {
	return fmt.Sprintf("%s.bak-hubaddr-%d", path, id)
}

func (e *hubAddrEngine) readNetplan(ctx context.Context, iface string) (string, []byte, error) {
	out, err := e.env.Hub.Run(ctx, hubnet.NetplanFilesScript())
	if err != nil {
		return "", nil, fmt.Errorf("netplan не прочитан: %w", err)
	}
	files, err := hubnet.ParseNetplanFiles(out)
	if err != nil {
		return "", nil, err
	}
	path, err := hubnet.PickNetplanFile(files, iface)
	if err != nil {
		return "", nil, err
	}
	return path, files[path], nil
}

func subnet24(a netip.Addr) string {
	if p, err := a.Prefix(24); err == nil {
		return p.String()
	}
	return ""
}

func (e *hubAddrEngine) stepAdd(ctx context.Context, c hubAddrChange, prev hubAddrStep, lg *stepLog) (any, error) {
	newA := mustAddr(c.NewIP)
	var ch hubAddrAddChecks
	if len(prev.Checks) > 0 {
		_ = json.Unmarshal(prev.Checks, &ch)
	}
	ch.Subnet, ch.CurSubnet = subnet24(newA), subnet24(mustAddr(c.OldIP))
	st, err := e.readHub(ctx)
	if err != nil {
		return ch, err
	}
	if st.route.Dev != c.Iface {
		return ch, fmt.Errorf("интерфейс маршрута по умолчанию сменился (%s → %s) — отмените смену и начните заново", c.Iface, st.route.Dev)
	}
	path, doc, err := e.readNetplan(ctx, c.Iface)
	if err != nil {
		return ch, err
	}
	ch.NetplanPath, ch.Backup = path, hubNetplanBackup(path, c.ID)

	addedNow := false
	if !hubnet.HasAddr(st.addrs, newA) {
		lg.printf("добавляю %s/32 на %s", newA, c.Iface)
		if out, err := e.env.Hub.Run(ctx, hubnet.AddrAddScript(c.Iface, newA)); err != nil {
			return ch, fmt.Errorf("ip addr add: %v: %s", err, out)
		}
		addedNow, ch.AddedRuntime = true, true
	} else {
		lg.printf("%s уже на %s", newA, c.Iface)
	}
	edited, changed, err := hubnet.NetplanAddAddress(doc, c.Iface, newA.String()+"/32")
	if err != nil {
		e.rollbackAdd(ctx, c, addedNow, false, ch, lg)
		return ch, err
	}
	if changed {
		lg.printf("netplan %s: добавляю адрес (копия %s)", path, ch.Backup)
		if out, err := e.env.Hub.Run(ctx, hubnet.NetplanWriteScript(path, edited, ch.Backup)); err != nil {
			e.rollbackAdd(ctx, c, addedNow, false, ch, lg)
			return ch, fmt.Errorf("netplan: %v: %s", err, out)
		}
		ch.NetplanChanged = true
	}

	// Verify: on the interface and in the file.
	st2, err := e.readHub(ctx)
	if err == nil && !hubnet.HasAddr(st2.addrs, newA) {
		err = fmt.Errorf("после добавления %s нет на %s", newA, c.Iface)
	}
	if err == nil {
		var doc2 []byte
		if _, doc2, err = e.readNetplan(ctx, c.Iface); err == nil {
			if _, again, aErr := hubnet.NetplanAddAddress(doc2, c.Iface, newA.String()+"/32"); aErr != nil || again {
				err = fmt.Errorf("адреса %s нет в netplan после записи", newA)
			}
		}
	}
	if err != nil {
		e.rollbackAdd(ctx, c, addedNow, changed, ch, lg)
		return ch, err
	}
	e.env.ResetHubIPs()
	lg.printf("адрес %s поднят и записан в netplan", newA)
	return ch, e.advance(ctx, c, hubAddrAdded, nil)
}

// rollbackAdd undoes what this run of step 1 changed.
func (e *hubAddrEngine) rollbackAdd(ctx context.Context, c hubAddrChange, addedNow, netplanChanged bool, ch hubAddrAddChecks, lg *stepLog) {
	if netplanChanged {
		if out, err := e.env.Hub.Run(ctx, hubnet.NetplanRestoreScript(ch.NetplanPath, ch.Backup)); err != nil {
			lg.printf("откат netplan не удался: %v: %s", err, out)
		} else {
			lg.printf("netplan возвращён из %s", ch.Backup)
		}
	}
	if addedNow {
		if out, err := e.env.Hub.Run(ctx, hubnet.AddrDelScript(c.Iface, mustAddr(c.NewIP))); err != nil {
			lg.printf("откат адреса не удался: %v: %s", err, out)
		} else {
			lg.printf("адрес %s снят с %s", c.NewIP, c.Iface)
		}
	}
}
