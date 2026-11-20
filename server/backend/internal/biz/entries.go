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
	// Rotation: who gets the entry in their subscription (EntryRotation*; the Hub is always 'all').
	Rotation       string     `json:"rotation"`
	RotationReason string     `json:"rotation_reason"`
	RotationAt     *time.Time `json:"rotation_at"`
	RotationBy     string     `json:"rotation_by"`
	Tunnel         bool       `json:"tunnel"` // servers.relay_tunnel: the relay forwards through its GRE tunnel
}

// Rotation of a reserve entry (entry_state.rotation, migration 000097).
const (
	EntryRotationAll  = "all"  // every user
	EntryRotationTest = "test" // only the «Тестовый пользователь» of «Входы» (entry_failover_test_user)
	EntryRotationOff  = "off"  // nobody; the monitor still checks it
)

// ErrRotationChanged: the entry's rotation is not the one the caller expected (a concurrent change).
var ErrRotationChanged = errors.New("состояние входа уже изменилось — обновите страницу")

// EntryFailover is the auto-swap state (app_settings entry_failover_*): while Active the main
// subscription lines point at relay Target. Pinned: the owner sent everyone through Target on
// purpose («Все через резервный вход», e.g. the Hub is cut on mobile networks while it answers
// from outside) — the entry monitor never switches it back by itself.
type EntryFailover struct {
	Active bool      `json:"active"`
	Target int       `json:"target"`
	Since  time.Time `json:"since"`
	Pinned bool      `json:"pinned"`
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
	COALESCE(e.ru_status, 'unknown'), e.ru_checked_at, COALESCE(e.ru_link, ''),
	COALESCE(e.rotation, 'all'), COALESCE(e.rotation_reason, ''), e.rotation_at, COALESCE(e.rotation_by, '')`

// relayEntriesQuery lists relays matching where (relaysPublished etc.), by id.
const relayEntriesQuery = `
	SELECT s.id, COALESCE(s.name, ''), s.ip_address, COALESCE(s.country_code, ''), COALESCE(s.lifecycle_state, ''),
	       COALESCE(s.relay_tunnel, FALSE), ` + entryStateColumns + `
	  FROM servers s LEFT JOIN entry_state e ON e.entry_id = s.id
	 WHERE s.role = 'relay' %s
	 ORDER BY s.id`

// The relay sets: what the subscription and every user-facing consumer publish (online, in
// rotation), the same plus the canary entries for the test user, what the monitor checks (every
// online relay) and what «Входы» shows (every relay).
const (
	relaysPublished    = `AND s.lifecycle_state = 'online' AND COALESCE(e.rotation, 'all') = 'all'`
	relaysWithTest     = `AND s.lifecycle_state = 'online' AND COALESCE(e.rotation, 'all') IN ('all', 'test')`
	relaysOnline       = `AND s.lifecycle_state = 'online'`
	relaysEveryState   = ``
	hubEntryStateQuery = `SELECT ` + entryStateColumns + ` FROM entry_state e WHERE e.entry_id = 0`
)

func scanEntry(scan func(dest ...any) error, head []any, e *Entry) error {
	var extAt, udpAt, ruAt, rotAt sql.NullTime
	dest := append(head, &e.AddressMode, &e.Domain,
		&e.Health.External, &extAt, &e.Health.ExternalError,
		&e.Health.UDP, &udpAt, &e.Health.UDPError,
		&e.Health.RU, &ruAt, &e.Health.RULink,
		&e.Rotation, &e.RotationReason, &rotAt, &e.RotationBy)
	if err := scan(dest...); err != nil {
		return err
	}
	ptr := func(t sql.NullTime) *time.Time {
		if !t.Valid {
			return nil
		}
		return &t.Time
	}
	e.Health.ExternalAt, e.Health.UDPAt, e.Health.RUAt, e.RotationAt = ptr(extAt), ptr(udpAt), ptr(ruAt), ptr(rotAt)
	e.Address = EntryAddress(e.IP, e.AddressMode, e.Domain)
	return nil
}

func listRelays(ctx context.Context, where string) ([]Entry, error) {
	rows, err := db.DB.QueryContext(ctx, fmt.Sprintf(relayEntriesQuery, where))
	if err != nil {
		return nil, fmt.Errorf("list relay entries: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e := Entry{Kind: EntryKindRelay}
		if err := scanEntry(rows.Scan, []any{&e.ID, &e.Name, &e.IP, &e.Country, &e.Lifecycle, &e.Tunnel}, &e); err != nil {
			return nil, fmt.Errorf("list relay entries: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list relay entries: %w", err)
	}
	return out, nil
}

// ListRelayEntries returns the published relays — online and in rotation ('all') — with their
// state, by id: every user-facing consumer (subscription, tg proxy, check-host, checklist). A
// failed read is an error (the subscription answers 503 on it, never a profile without the
// reserve lines).
func ListRelayEntries(ctx context.Context) ([]Entry, error) { return listRelays(ctx, relaysPublished) }

// ListSubscriptionRelays: the relays of one user's subscription — the published ones, plus the
// canary entries ('test') when withTest (the user is entry_failover_test_user).
func ListSubscriptionRelays(ctx context.Context, withTest bool) ([]Entry, error) {
	if withTest {
		return listRelays(ctx, relaysWithTest)
	}
	return listRelays(ctx, relaysPublished)
}

// ListEntries returns the Hub (ID 0) followed by the published relays.
func ListEntries(ctx context.Context) ([]Entry, error) { return listWithHub(ctx, relaysPublished) }

// ListMonitoredEntries: the Hub and every online relay whatever its rotation (the entries
// monitor checks an entry out of rotation too — the owner sees whether it recovered).
func ListMonitoredEntries(ctx context.Context) ([]Entry, error) {
	return listWithHub(ctx, relaysOnline)
}

// ListAdminEntries: the Hub and every relay in any lifecycle state («Входы»).
func ListAdminEntries(ctx context.Context) ([]Entry, error) {
	return listWithHub(ctx, relaysEveryState)
}

func listWithHub(ctx context.Context, where string) ([]Entry, error) {
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
	hub.Rotation = EntryRotationAll
	relays, err := listRelays(ctx, where)
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
	settingFailoverPinned = "entry_failover_pinned"
)

// entryFailoverQuery reads the four failover keys in one statement: one snapshot, so a target,
// a since and the pin written together by SetEntryFailover are never mixed with older values.
const entryFailoverQuery = `SELECT key, value FROM app_settings WHERE key IN ($1, $2, $3, $4)`

// GetEntryFailover reads the auto-swap state; a failed read is an error, absent keys are inactive.
func GetEntryFailover(ctx context.Context) (EntryFailover, error) {
	rows, err := db.DB.QueryContext(ctx, entryFailoverQuery, settingFailoverActive, settingFailoverTarget, settingFailoverSince, settingFailoverPinned)
	if err != nil {
		return EntryFailover{}, fmt.Errorf("read entry failover: %w", err)
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return EntryFailover{}, fmt.Errorf("read entry failover: %w", err)
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return EntryFailover{}, fmt.Errorf("read entry failover: %w", err)
	}
	return ParseEntryFailover(raw[settingFailoverActive], raw[settingFailoverTarget], raw[settingFailoverSince], raw[settingFailoverPinned]), nil
}

// ParseEntryFailover: active only with "true" and a positive target; a broken since is zero;
// pinned only with "true" on an active failover.
func ParseEntryFailover(active, target, since, pinned string) EntryFailover {
	id, err := strconv.Atoi(strings.TrimSpace(target))
	if strings.TrimSpace(active) != "true" || err != nil || id <= 0 {
		return EntryFailover{}
	}
	at, _ := time.Parse(time.RFC3339, strings.TrimSpace(since))
	return EntryFailover{Active: true, Target: id, Since: at, Pinned: strings.TrimSpace(pinned) == "true"}
}

// SetEntryFailover writes the four keys in one statement (atomic). A zero Since is stored as "";
// an inactive failover is never pinned.
func SetEntryFailover(ctx context.Context, st EntryFailover) error {
	if st.Active && st.Target <= 0 {
		return errors.New("entry failover: active without a target")
	}
	active, target, since, pinned := "false", "0", "", "false"
	if st.Active {
		active, target = "true", strconv.Itoa(st.Target)
		if !st.Since.IsZero() {
			since = st.Since.UTC().Format(time.RFC3339)
		}
		if st.Pinned {
			pinned = "true"
		}
	}
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, NOW()), ($3, $4, NOW()), ($5, $6, NOW()), ($7, $8, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		settingFailoverActive, active, settingFailoverTarget, target, settingFailoverSince, since, settingFailoverPinned, pinned)
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

// entryCheckQueries: one upsert per check kind. The WHERE keeps the newest check: a slow probe
// that finishes after a newer one (or a write retried late) never overwrites it.
var entryCheckQueries = map[string]string{
	"ext": `INSERT INTO entry_state (entry_id, ext_status, ext_checked_at, ext_error) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET ext_status = EXCLUDED.ext_status, ext_checked_at = EXCLUDED.ext_checked_at,
		ext_error = EXCLUDED.ext_error, updated_at = NOW()
		WHERE entry_state.ext_checked_at IS NULL OR entry_state.ext_checked_at <= EXCLUDED.ext_checked_at`,
	"udp": `INSERT INTO entry_state (entry_id, udp_status, udp_checked_at, udp_error) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET udp_status = EXCLUDED.udp_status, udp_checked_at = EXCLUDED.udp_checked_at,
		udp_error = EXCLUDED.udp_error, updated_at = NOW()
		WHERE entry_state.udp_checked_at IS NULL OR entry_state.udp_checked_at <= EXCLUDED.udp_checked_at`,
	"ru": `INSERT INTO entry_state (entry_id, ru_status, ru_checked_at, ru_link) VALUES ($1, $2, $3, $4)
		ON CONFLICT (entry_id) DO UPDATE SET ru_status = EXCLUDED.ru_status, ru_checked_at = EXCLUDED.ru_checked_at,
		ru_link = EXCLUDED.ru_link, updated_at = NOW()
		WHERE entry_state.ru_checked_at IS NULL OR entry_state.ru_checked_at <= EXCLUDED.ru_checked_at`,
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

// SaveEntryCheck persists one check of kind ext|udp|ru for an entry (0 = Hub). At is when the
// check ran (required); a check older than the stored one is silently dropped.
func SaveEntryCheck(ctx context.Context, entryID int, kind string, c EntryCheck) error {
	q, ok := entryCheckQueries[kind]
	if !ok {
		return fmt.Errorf("entry check: unknown kind %q", kind)
	}
	if !slices.Contains(entryCheckStatuses[kind], c.Status) {
		return fmt.Errorf("entry check %s: unknown status %q", kind, c.Status)
	}
	if c.At.IsZero() {
		return fmt.Errorf("entry check %s: no check time", kind)
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
