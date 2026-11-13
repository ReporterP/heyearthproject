package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// «Запуск» on the dashboard (cleanup plan B6): the owner's way to a service that works on mobile
// networks, each step read from the real state, with a button into the right wizard. Done
// steps fold away once all are done.

type launchRelay struct {
	ID         int
	Name       string
	IP         string
	ExtHealthy bool
	MobileOK   time.Time // the last phone that got through (zero = never)
}

type launchFacts struct {
	Now          time.Time
	HubSampleAt  time.Time // zero = no sample
	HubCPU       float64
	ExitsWorking int // online, agent alive, tunnel judged healthy
	ExitsOnline  int
	Relays       []launchRelay // online relays
	Mode         string        // entryModeHub | entryModeRelay | entryModeAuto
	PinnedTarget int
	SiteHost     string
	DNS          map[string][]string // name → IPv4 addresses (absent = did not resolve)
	DNSErr       string
}

type launchAction struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

type launchItem struct {
	Key    string        `json:"key"`
	Title  string        `json:"title"`
	Done   bool          `json:"done"`
	Detail string        `json:"detail"`
	Action *launchAction `json:"action,omitempty"`
}

type launchChecklist struct {
	Items    []launchItem `json:"items"`
	Done     int          `json:"done"`
	Total    int          `json:"total"`
	Complete bool         `json:"complete"`
}

const launchHubFresh = 5 * time.Minute

func launchSiteNames(host string) []string {
	return []string{host, "www." + host, "admin." + host}
}

// buildLaunchChecklist turns the facts into the six steps (pure; tests).
func buildLaunchChecklist(f launchFacts) launchChecklist {
	var items []launchItem

	hub := launchItem{Key: "hub", Title: "Hub работает", Action: &launchAction{"Открыть Hub", "/servers?tab=hub"}}
	if !f.HubSampleAt.IsZero() && f.Now.Sub(f.HubSampleAt) <= launchHubFresh {
		hub.Done, hub.Detail = true, fmt.Sprintf("Нагрузка CPU %.0f %%", f.HubCPU)
	} else {
		hub.Detail = "Нет свежих данных о нагрузке Hub — проверьте сервис heyearth-api (go run ./cmd/deploy --status)"
	}
	items = append(items, hub)

	exits := launchItem{Key: "exits", Title: "Не меньше двух выходов за рубежом", Done: f.ExitsWorking >= 2,
		Detail: fmt.Sprintf("Работают %d из %d. Второй выход держит интернет пользователей, пока первый недоступен.", f.ExitsWorking, f.ExitsOnline)}
	if !exits.Done {
		exits.Action = &launchAction{"Добавить выход", "/servers?tab=exit&add=1"}
	}
	items = append(items, exits)

	entry := launchItem{Key: "entry", Title: "Добавлен вход в РФ", Done: len(f.Relays) > 0}
	if entry.Done {
		names := make([]string, len(f.Relays))
		for i, r := range f.Relays {
			names[i] = r.Name
		}
		entry.Detail = strings.Join(names, ", ")
	} else {
		entry.Detail = "Мобильные операторы режут адрес Hub: нужен сервер в России у другого провайдера."
		entry.Action = &launchAction{"Добавить вход", "/servers?tab=relay&add=1"}
	}
	items = append(items, entry)

	mobile := launchItem{Key: "mobile", Title: "Вход проверен с телефона"}
	var best *launchRelay
	for i := range f.Relays {
		if r := &f.Relays[i]; !r.MobileOK.IsZero() && (best == nil || r.MobileOK.After(best.MobileOK)) {
			best = r
		}
	}
	switch {
	case best != nil:
		mobile.Done = true
		mobile.Detail = fmt.Sprintf("%s: телефон на мобильном интернете получил данные %s", best.Name, best.MobileOK.In(moscowTZ).Format("02.01 15:04"))
	case len(f.Relays) > 0:
		mobile.Detail = "Откройте проверку на телефоне без Wi-Fi: так видно, пропускает ли оператор адрес входа."
		mobile.Action = &launchAction{"Проверить с телефона", "/servers?tab=relay&phone=" + strconv.Itoa(f.Relays[0].ID)}
	default:
		mobile.Detail = "Сначала добавьте вход."
	}
	items = append(items, mobile)

	enable := launchItem{Key: "enable", Title: "Вход включён для всех", Done: f.Mode == entryModeRelay}
	if enable.Done {
		for _, r := range f.Relays {
			if r.ID == f.PinnedTarget {
				enable.Detail = "Пользователи подключаются через " + r.Name
			}
		}
		if enable.Detail == "" {
			enable.Detail = "Пользователи подключаются через вход в РФ"
		}
	} else {
		enable.Detail = "Сейчас пользователи подключаются к Hub напрямую — на мобильном интернете он не открывается."
		if len(f.Relays) > 0 {
			enable.Action = &launchAction{"Включить", "/servers?tab=relay"}
		}
	}
	items = append(items, enable)

	items = append(items, dnsItem(f))

	out := launchChecklist{Items: items, Total: len(items)}
	for _, it := range items {
		if it.Done {
			out.Done++
		}
	}
	out.Complete = out.Done == out.Total
	return out
}

// dnsItem: every address the site's names resolve to must be an entry (the old Hub record
// removed); a name that does not resolve at all is not required except the main one.
func dnsItem(f launchFacts) launchItem {
	it := launchItem{Key: "dns", Title: "Домен указывает на вход"}
	if len(f.Relays) == 0 {
		it.Detail = "Сначала добавьте вход."
		return it
	}
	entryIPs := make([]string, 0, len(f.Relays))
	for _, r := range f.Relays {
		entryIPs = append(entryIPs, r.IP)
	}
	it.Action = &launchAction{"Как настроить", "/servers?tab=relay#dns"}
	if f.DNSErr != "" {
		it.Detail = "Не удалось проверить DNS: " + f.DNSErr
		return it
	}
	var wrong []string
	for _, name := range launchSiteNames(f.SiteHost) {
		ips, ok := f.DNS[name]
		if !ok || len(ips) == 0 {
			if name == f.SiteHost {
				wrong = append(wrong, name+" не находится в DNS")
			}
			continue
		}
		var bad []string
		for _, ip := range ips {
			if !slices.Contains(entryIPs, ip) {
				bad = append(bad, ip)
			}
		}
		if len(bad) > 0 {
			wrong = append(wrong, name+" → "+strings.Join(bad, ", "))
		}
	}
	if len(wrong) == 0 {
		it.Done, it.Action = true, nil
		it.Detail = f.SiteHost + " указывает на вход: сайт и подписка открываются на мобильном интернете"
		return it
	}
	it.Detail = "Не адрес входа: " + strings.Join(wrong, "; ") + ". Замените A-записи у регистратора домена."
	return it
}

// Seams (tests).
var (
	launchLookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return net.DefaultResolver.LookupIPAddr(ctx, host)
	}
	launchNow = time.Now
)

func resolveSiteNames(ctx context.Context, host string) (map[string][]string, string) {
	names := launchSiteNames(host)
	out := map[string][]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var hardErr string
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			addrs, err := launchLookup(c, n)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				var dnsErr *net.DNSError
				if n == host && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
					hardErr = err.Error()
				}
				return
			}
			for _, a := range addrs {
				if v4 := a.IP.To4(); v4 != nil {
					out[n] = append(out[n], v4.String())
				}
			}
		}(n)
	}
	wg.Wait()
	return out, hardErr
}

// gatherLaunchFacts reads the state; a failed read is an error (the widget says so instead of
// showing a wrong checklist).
func gatherLaunchFacts(ctx context.Context) (launchFacts, error) {
	f := launchFacts{Now: launchNow(), SiteHost: monitorSiteHost()}
	err := db.DB.QueryRowContext(ctx, `SELECT at, cpu_pct FROM server_metric_samples WHERE server_id IS NULL ORDER BY at DESC LIMIT 1`).
		Scan(&f.HubSampleAt, &f.HubCPU)
	if err != nil && err != sql.ErrNoRows {
		return f, fmt.Errorf("hub sample: %w", err)
	}
	if err := db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE COALESCE(status, '') = 'online' AND COALESCE(tunnel_status, '') = 'healthy'), COUNT(*)
		FROM servers WHERE COALESCE(role, 'exit') = 'exit' AND lifecycle_state = 'online'`).Scan(&f.ExitsWorking, &f.ExitsOnline); err != nil {
		return f, fmt.Errorf("exits: %w", err)
	}
	relays, err := biz.ListRelayEntries(ctx)
	if err != nil {
		return f, err
	}
	for _, e := range relays {
		r := launchRelay{ID: e.ID, Name: e.Name, IP: e.IP, ExtHealthy: e.Health.External == biz.EntryHealthy}
		var v string
		if err := db.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, admin.MobileOKKey(e.ID)).Scan(&v); err == nil {
			r.MobileOK, _ = time.Parse(time.RFC3339, strings.TrimSpace(v))
		}
		if r.Name == "" {
			r.Name = "Вход " + strconv.Itoa(e.ID)
		}
		f.Relays = append(f.Relays, r)
	}
	fo, err := entriesFailover(ctx)
	if err != nil {
		return f, err
	}
	enabled, err := entriesSetting("entry_failover_enabled")
	if err != nil {
		return f, err
	}
	f.Mode, f.PinnedTarget = entryModeOf(enabled != "false", fo), fo.Target
	if len(f.Relays) > 0 {
		f.DNS, f.DNSErr = resolveSiteNames(ctx, f.SiteHost)
	}
	return f, nil
}

// handleLaunchChecklist — GET /api/admin/launch-checklist.
func handleLaunchChecklist(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	f, err := gatherLaunchFacts(ctx)
	if err != nil {
		log.Printf("[launch-checklist] %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Не удалось прочитать состояние серверов"})
		return
	}
	writeJSON(w, http.StatusOK, buildLaunchChecklist(f))
}
