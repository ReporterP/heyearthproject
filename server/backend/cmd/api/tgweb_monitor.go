package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/tgweb"
)

// The Telegram WEB proxy's readiness (phase T): while it is switched on and installed for its host,
// the relay's /readyz is asked once a minute; three answers in a row other than 200 raise
// tgWebAlertKey, the next 200 resolves it.
const (
	tgWebAlertKey   = "tg-webproxy-readyz"
	tgWebProbeEvery = time.Minute
	tgWebFailAfter  = 3
)

// tgWebProbe keeps the failure streak (one goroutine).
type tgWebProbe struct {
	fails int
	// seams
	load  func(ctx context.Context) (tgweb.Settings, error)
	ready func(ctx context.Context) (bool, string)
	raise func(title, details string)
	clear func(msg string)
}

func (p *tgWebProbe) tick(ctx context.Context) {
	s, err := p.load(ctx)
	if err != nil {
		log.Printf("[monitor] WEB-прокси: настройки не прочитаны: %v", err)
		return
	}
	if !s.LinkReady() {
		if p.fails > 0 {
			p.clear("WEB-прокси выключен или переустанавливается — проверка готовности остановлена.")
		}
		p.fails = 0
		return
	}
	ok, why := p.ready(ctx)
	if ok {
		if p.fails >= tgWebFailAfter {
			p.clear(fmt.Sprintf("WEB-прокси %s снова готов (readyz 200).", s.Host))
		}
		p.fails = 0
		return
	}
	p.fails++
	log.Printf("[monitor] WEB-прокси %s: readyz не 200 (%d подряд): %s", s.Host, p.fails, why)
	if p.fails == tgWebFailAfter {
		p.raise("WEB-прокси Telegram не готов",
			fmt.Sprintf("%d проверки подряд релей WEB-прокси %s не ответил 200 на readyz: %s. Подписчики с WEB-прокси без Telegram. "+
				"Проверьте на Hub: systemctl status tproxy-server mtproxy; journalctl -u mtproxy --since -10min. Админка → Telegram → «Установить / обновить».",
				p.fails, s.Host, why))
	}
}

func startTGWebMonitor() {
	p := &tgWebProbe{
		load:  tgweb.Load,
		ready: tgweb.Ready,
		raise: func(title, details string) { RaiseAlert(tgWebAlertKey, AlertWarn, title, details) },
		clear: func(msg string) { ResolveAlert(tgWebAlertKey, msg) },
	}
	go runEvery(2*time.Minute, tgWebProbeEvery, func() {
		safely("WEB-прокси", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			p.tick(ctx)
		})
	})
}
