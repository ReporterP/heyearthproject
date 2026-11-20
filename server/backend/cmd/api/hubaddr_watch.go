package main

import (
	"context"
	"log"
	"strings"
	"time"
)

// The watcher of a switch (every 30 s and once at start-up): in «switching» it notices a revert
// the timer ran, a route source that is not the new address (a reboot: netplan still has the old
// source, transient timers are gone) and a timer that vanished past the deadline — and finishes the
// revert on the API side; in «reverting» it retries the entries that are not back yet.

const hubWatchEvery = 30 * time.Second

func startHubAddrWatcher() {
	go func() {
		for {
			if hubAddr != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
				hubAddr.watchOnce(ctx)
				cancel()
			}
			time.Sleep(hubWatchEvery)
		}
	}()
}

func (e *hubAddrEngine) watchOnce(ctx context.Context) {
	c, ok, err := e.env.Repo.Active(ctx)
	if err != nil || !ok {
		return
	}
	e.mu.Lock()
	running := e.busy[c.ID] != "" || e.reverting[c.ID]
	e.mu.Unlock()
	if running {
		return
	}
	switch c.State {
	case hubAddrSwitching:
		reason := ""
		if e.markerExists(ctx, c.ID) {
			reason = "сработал таймер отката: подтверждения не было"
		} else if st, err := e.readHub(ctx); err == nil && st.route.Src != mustAddr(c.NewIP) {
			reason = "хаб отвечает не с нового адреса (источник " + st.route.Src.String() + ") — перезагрузка?"
		} else if c.ConfirmDeadline != nil && e.env.Now().After(c.ConfirmDeadline.Add(time.Minute)) && !e.timerActive(ctx, c.ID) {
			reason = "таймер отката пропал, а подтверждения нет"
		}
		if reason == "" {
			return
		}
		log.Printf("[hub-address] change %d: %s — откатываю", c.ID, reason)
		if err := e.apiRevert(ctx, c, reason); err != nil {
			log.Printf("[hub-address] change %d revert: %v", c.ID, err)
		}
	case hubAddrReverting:
		if e.env.Now().Sub(e.revertRecord(c).LastTry) < hubRevertRetry {
			return
		}
		if err := e.retryRevertRelays(ctx, c, nil); err != nil {
			log.Printf("[hub-address] change %d revert retry: %v", c.ID, err)
		}
	}
}

func (e *hubAddrEngine) timerActive(ctx context.Context, id int64) bool {
	out, _ := e.env.Hub.Run(ctx, "systemctl is-active "+hubRevertUnit(id)+".timer\n")
	return strings.TrimSpace(out) == "active"
}
