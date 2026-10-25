package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/heyearth/backend/internal/db"
)

// Entries: the addresses users connect to (spec 2026-10-12 «Пул нод», section 4). Entry 0 is the
// Hub (its address is main_server_host); every other entry is a servers row with role 'relay' —
// a RU VPS that DNATs the Hub's user ports to the Hub (internal/config/relay_nft.go). Health is
// written only by the entries monitor (cmd/api/entry_monitor.go).

const (
	EntryKindHub       = "hub"
	EntryKindRelay     = "relay"
	EntryUnknown       = "unknown"
	EntryHealthy       = "healthy"
	EntryUnhealthy     = "unhealthy"
	EntryAddressIP     = "ip"
	EntryAddressDomain = "domain"
)

// EntryHealth: External = TLS to <entry>:443 with the Reality SNI through a foreign exit; UDP =
// the Bamboo VN probe through the entry's hop ports; RU = check-host RU nodes, TCP :443
// (reachable|unreachable|unknown). *Error is the last error, RULink the check-host report.
type EntryHealth struct {
	External      string     `json:"external"`
	ExternalAt    *time.Time `json:"external_at"`
	ExternalError string     `json:"external_error"`
	UDP           string     `json:"udp"`
	UDPAt         *time.Time `json:"udp_at"`
	UDPError      string     `json:"udp_error"`
	RU            string     `json:"ru"`
	RUAt          *time.Time `json:"ru_at"`
	RULink        string     `json:"ru_link"`
}

// Entry is one address users connect to: the Hub or an online relay.
type Entry struct {
	ID          int         `json:"id"` // 0 = Hub, otherwise servers.id of a relay
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`    // hub | relay
	Address     string      `json:"address"` // what the subscription puts in front of the ports
	IP          string      `json:"ip"`      // relay: servers.ip_address; Hub: main_server_host
	AddressMode string      `json:"address_mode"`
	Domain      string      `json:"domain"`
	Country     string      `json:"country"`
	Lifecycle   string      `json:"lifecycle"`
	Health      EntryHealth `json:"health"`
}

// EntryFailover is the auto-swap state (app_settings entry_failover_*): while Active the main
// subscription lines point at relay Target.
type EntryFailover struct {
	Active bool      `json:"active"`
	Target int       `json:"target"`
	Since  time.Time `json:"since"`
}

// EntryCheck is one persisted check of an entry. Status: ext/udp unknown|healthy|unhealthy,
// ru unknown|reachable|unreachable. Detail: ext/udp the last error ("" = OK), ru the report link.
type EntryCheck struct {
	Status string
	At     time.Time
	Detail string
}

// ErrEntryDomainUnavailable: the domain mode of an entry is refused (no domain registry yet).
var ErrEntryDomainUnavailable = errors.New("режим «домен» станет доступен после настройки раздела «Домены»")

// EntryDomainModeAvailable and EntryDomainCheck are replaced by phase 4 (domain registry: DNS of
// the domain must point at the entry). Until then only the IP mode can be selected.
var (
	EntryDomainModeAvailable = func() bool { return false }
	EntryDomainCheck         = func(ctx context.Context, entryID int, domain string) error { return ErrEntryDomainUnavailable }
)

const entryStateColumns = `COALESCE(e.address_mode, 'ip'), COALESCE(e.domain, ''),
	COALESCE(e.ext_status, 'unknown'), e.ext_checked_at, COALESCE(e.ext_error, ''),
	COALESCE(e.udp_status, 'unknown'), e.udp_checked_at, COALESCE(e.udp_error, ''),
	COALESCE(e.ru_status, 'unknown'), e.ru_checked_at, COALESCE(e.ru_link, '')`

const relayEntriesQuery = `
	SELECT s.id, COALESCE(s.name, ''), s.ip_address, COALESCE(s.country_code, ''), COALESCE(s.lifecycle_state, ''),
	       ` + entryStateColumns + `
	  FROM servers s LEFT JOIN entry_state e ON e.entry_id = s.id
	 WHERE s.role = 'relay' AND s.lifecycle_state = 'online'
	 ORDER BY s.id`

const hubEntryStateQuery = `SELECT ` + entryStateColumns + ` FROM entry_state e WHERE e.entry_id = 0`

func scanEntry(scan func(dest ...any) error, head []any, e *Entry) error {
	var extAt, udpAt, ruAt sql.NullTime
	dest := append(head, &e.AddressMode, &e.Domain,
		&e.Health.External, &extAt, &e.Health.ExternalError,
		&e.Health.UDP, &udpAt, &e.Health.UDPError,
		&e.Health.RU, &ruAt, &e.Health.RULink)
	if err := scan(dest...); err != nil {
		return err
	}
	ptr := func(t sql.NullTime) *time.Time {
		if !t.Valid {
			return nil
		}
		return &t.Time
	}
	e.Health.ExternalAt, e.Health.UDPAt, e.Health.RUAt = ptr(extAt), ptr(udpAt), ptr(ruAt)
	e.Address = EntryAddress(e.IP, e.AddressMode, e.Domain)
	return nil
}

// ListRelayEntries returns the online relays with their state, by id. A failed read is an error
// (the subscription answers 503 on it, never a profile without the reserve lines).
func ListRelayEntries(ctx context.Context) ([]Entry, error) {
	rows, err := db.DB.QueryContext(ctx, relayEntriesQuery)
	if err != nil {
		return nil, fmt.Errorf("list relay entries: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e := Entry{Kind: EntryKindRelay}
		if err := scanEntry(rows.Scan, []any{&e.ID, &e.Name, &e.IP, &e.Country, &e.Lifecycle}, &e); err != nil {
			return nil, fmt.Errorf("list relay entries: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list relay entries: %w", err)
	}
	return out, nil
}

// ListEntries returns the Hub (ID 0) followed by the online relays.
func ListEntries(ctx context.Context) ([]Entry, error) {
	host, err := entrySetting(ctx, "main_server_host", "")
	if err != nil {
		return nil, err
	}
	hub := Entry{ID: 0, Name: "Основной вход (Hub)", Kind: EntryKindHub, IP: host, Country: "RU", Lifecycle: "online"}
	err = scanEntry(db.DB.QueryRowContext(ctx, hubEntryStateQuery).Scan, nil, &hub)
	if errors.Is(err, sql.ErrNoRows) {
		hub.AddressMode, hub.Address = EntryAddressIP, host
		hub.Health = EntryHealth{External: EntryUnknown, UDP: EntryUnknown, RU: EntryUnknown}
	} else if err != nil {
		return nil, fmt.Errorf("hub entry state: %w", err)
	}
	relays, err := ListRelayEntries(ctx)
	if err != nil {
		return nil, err
	}
	return append([]Entry{hub}, relays...), nil
}

// EntryAddress: the domain in domain mode (when set), the IP otherwise.
func EntryAddress(ip, mode, domain string) string {
	if mode == EntryAddressDomain && domain != "" {
		return domain
	}
	return ip
}

// entrySetting: def for an absent or blank key, an error for a failed read.
func entrySetting(ctx context.Context, key, def string) (string, error) {
	v, err := GetSettingContext(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}
	if v = strings.TrimSpace(v); v == "" {
		return def, nil
	}
	return v, nil
}

const (
	settingFailoverActive = "entry_failover_active"
	settingFailoverTarget = "entry_failover_target"
	settingFailoverSince  = "entry_failover_since"
)

// GetEntryFailover reads the auto-swap state; a failed read is an error, absent keys are inactive.
func GetEntryFailover(ctx context.Context) (EntryFailover, error) {
	var raw [3]string
	for i, k := range []string{settingFailoverActive, settingFailoverTarget, settingFailoverSince} {
		v, err := entrySetting(ctx, k, "")
		if err != nil {
			return EntryFailover{}, err
		}
		raw[i] = v
	}
	return ParseEntryFailover(raw[0], raw[1], raw[2]), nil
}

// ParseEntryFailover: active only with "true" and a positive target; a broken since is zero.
func ParseEntryFailover(active, target, since string) EntryFailover {
	id, err := strconv.Atoi(strings.TrimSpace(target))
	if strings.TrimSpace(active) != "true" || err != nil || id <= 0 {
		return EntryFailover{}
	}
	at, _ := time.Parse(time.RFC3339, strings.TrimSpace(since))
	return EntryFailover{Active: true, Target: id, Since: at}
}

// SetEntryFailover writes the three keys in one statement (atomic). A zero Since is stored as "".
func SetEntryFailover(ctx context.Context, st EntryFailover) error {
	if st.Active && st.Target <= 0 {
		return errors.New("entry failover: active without a target")
	}
	active, target, since := "false", "0", ""
	if st.Active {
		active, target = "true", strconv.Itoa(st.Target)
		if !st.Since.IsZero() {
			since = st.Since.UTC().Format(time.RFC3339)
		}
	}
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, NOW()), ($3, $4, NOW()), ($5, $6, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		settingFailoverActive, active, settingFailoverTarget, target, settingFailoverSince, since)
	if err != nil {
		return fmt.Errorf("save entry failover: %w", err)
	}
	return nil
}

// EntryIsFailoverTarget: the main lines point at relay id right now (deprovision must refuse).
func EntryIsFailoverTarget(ctx context.Context, id int) (bool, error) {
	st, err := GetEntryFailover(ctx)
	return err == nil && st.Active && st.Target == id, err
}

var entryCheckQueries = map[string]string{
	"ext": `INSERT INTO entry_state (entry_id, ext_status, ext_checked_at, ext_error) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET ext_status = EXCLUDED.ext_status, ext_checked_at = EXCLUDED.ext_checked_at,
		ext_error = EXCLUDED.ext_error, updated_at = NOW()`,
	"udp": `INSERT INTO entry_state (entry_id, udp_status, udp_checked_at, udp_error) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET udp_status = EXCLUDED.udp_status, udp_checked_at = EXCLUDED.udp_checked_at,
		udp_error = EXCLUDED.udp_error, updated_at = NOW()`,
	"ru": `INSERT INTO entry_state (entry_id, ru_status, ru_checked_at, ru_link) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET ru_status = EXCLUDED.ru_status, ru_checked_at = EXCLUDED.ru_checked_at,
		ru_link = EXCLUDED.ru_link, updated_at = NOW()`,
}

// entryCheckStatuses mirrors the CHECKs of entry_state (migration 000062): a wrong status is
// refused here, before the write, instead of failing on every monitor tick.
var entryCheckStatuses = map[string][]string{
	"ext": {EntryUnknown, EntryHealthy, EntryUnhealthy},
	"udp": {EntryUnknown, EntryHealthy, EntryUnhealthy},
	"ru":  {EntryUnknown, "reachable", "unreachable"},
}

// entryDetailMax caps a persisted check detail (an error text or a report link), in bytes.
const entryDetailMax = 500

// entryDetail makes a check detail storable: valid UTF-8 without NUL (PostgreSQL rejects both
// for TEXT; probe errors may quote raw network bytes), cut to entryDetailMax bytes on a rune
// boundary (the errors are often Russian — a split rune would fail the whole write).
func entryDetail(s string) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
	if len(s) <= entryDetailMax {
		return s
	}
	cut := entryDetailMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// SaveEntryCheck persists one check of kind ext|udp|ru for an entry (0 = Hub).
func SaveEntryCheck(ctx context.Context, entryID int, kind string, c EntryCheck) error {
	q, ok := entryCheckQueries[kind]
	if !ok {
		return fmt.Errorf("entry check: unknown kind %q", kind)
	}
	if !slices.Contains(entryCheckStatuses[kind], c.Status) {
		return fmt.Errorf("entry check %s: unknown status %q", kind, c.Status)
	}
	if _, err := db.DB.ExecContext(ctx, q, entryID, c.Status, c.At, entryDetail(c.Detail)); err != nil {
		return fmt.Errorf("save entry %d %s check: %w", entryID, kind, err)
	}
	return nil
}

// SetEntryAddressMode stores the address mode of an entry (0 = Hub); the IP mode clears the domain.
// Whether the domain mode is allowed is the caller's decision (EntryDomainCheck).
func SetEntryAddressMode(ctx context.Context, entryID int, mode, domain string) error {
	switch mode {
	case EntryAddressIP:
		domain = ""
	case EntryAddressDomain:
	default:
		return fmt.Errorf("address mode %q: ip или domain", mode)
	}
	_, err := db.DB.ExecContext(ctx, `INSERT INTO entry_state (entry_id, address_mode, domain) VALUES ($1, $2, $3)
		ON CONFLICT (entry_id) DO UPDATE SET address_mode = EXCLUDED.address_mode, domain = EXCLUDED.domain, updated_at = NOW()`,
		entryID, mode, domain)
	if err != nil {
		return fmt.Errorf("save entry %d address mode: %w", entryID, err)
	}
	return nil
}

// DeleteEntryState removes a relay's row after its deprovision (the Hub row 0 stays).
func DeleteEntryState(ctx context.Context, entryID int) error {
	if _, err := db.DB.ExecContext(ctx, `DELETE FROM entry_state WHERE entry_id = $1 AND entry_id > 0`, entryID); err != nil {
		return fmt.Errorf("delete entry %d state: %w", entryID, err)
	}
	return nil
}
