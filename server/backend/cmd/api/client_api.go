package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─────────────────────── Auth RPCs ───────────────────────

func (s *server) Register(ctx context.Context, req *heyearth_v1.RegisterRequest) (*heyearth_v1.AuthResponse, error) {
	device := deviceFromProto(req.GetDevice())

	user, err := biz.RegisterAnonymousUser(device)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "register user: %v", err)
	}

	token, err := issueUserJWT(user.UserID, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue token: %v", err)
	}

	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}

	return &heyearth_v1.AuthResponse{
		UserId:  user.UserID,
		Token:   token,
		Profile: profileToProto(profile),
	}, nil
}

func (s *server) Login(ctx context.Context, req *heyearth_v1.LoginRequest) (*heyearth_v1.AuthResponse, error) {
	idOrUsername := strings.TrimSpace(req.GetIdOrUsername())
	if idOrUsername == "" {
		return nil, status.Error(codes.InvalidArgument, "id_or_username is required")
	}

	user, err := biz.LoginByIDOrUsername(idOrUsername, req.GetPassword())
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}

	token, err := issueUserJWT(user.UserID, user.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue token: %v", err)
	}

	profile, err := biz.GetUserProfile(user.UserID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}

	return &heyearth_v1.AuthResponse{
		UserId:  user.UserID,
		Token:   token,
		Profile: profileToProto(profile),
	}, nil
}

func (s *server) UpdateProfile(ctx context.Context, req *heyearth_v1.UpdateProfileRequest) (*heyearth_v1.UserProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	// Set username if provided
	if username := strings.TrimSpace(req.GetUsername()); username != "" {
		if err := biz.SetUsername(userID, username); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	// Set password if provided
	if newPassword := req.GetNewPassword(); newPassword != "" {
		if err := biz.SetCloudPassword(userID, newPassword, req.GetCurrentPassword()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
	}

	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get profile: %v", err)
	}
	return profileToProto(profile), nil
}

// ─────────────────────── User Info RPCs ───────────────────────

func (s *server) GetMe(ctx context.Context, req *heyearth_v1.GetMeRequest) (*heyearth_v1.UserProfileResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	profile, err := biz.GetUserProfile(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get user profile: %v", err)
	}
	return profileToProto(profile), nil
}

func (s *server) GetAccessState(ctx context.Context, req *heyearth_v1.GetAccessStateRequest) (*heyearth_v1.AccessStateResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	access, err := biz.GetUserAccessState(userID, req.GetPlatform())
	if err != nil {
		// A failed read is temporary (GetUserAccessState never turns it into
		// "no access"): Unavailable like GetConnectionBundle, SQL details stay
		// server-side.
		log.Printf("[access-state] user %s: access state lookup failed: %v", userID, err)
		return nil, status.Error(codes.Unavailable, "failed to load access state")
	}
	return accessToProto(access), nil
}

// ─────────────────────── Connection Bundle ───────────────────────

func (s *server) GetConnectionBundle(ctx context.Context, req *heyearth_v1.GetConnectionBundleRequest) (*heyearth_v1.ConnectionBundleResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	access, err := biz.GetUserAccessState(userID, req.GetPlatform())
	if err != nil {
		// Keep SQL details server-side; clients get a fixed message. A failed
		// read is temporary: Unavailable, and the app keeps its cached options.
		log.Printf("[connection-bundle] user %s: access state lookup failed: %v", userID, err)
		return nil, status.Error(codes.Unavailable, "failed to load access state")
	}
	if access.IsBlocked {
		return nil, status.Error(codes.PermissionDenied, "user is blocked")
	}
	if !access.CanConnect {
		return nil, status.Errorf(codes.PermissionDenied, "cannot connect: %s", access.Reason)
	}

	// Hub-and-spoke: return main server connection info (per-user UUID)
	options, err := buildHubConnectionOptions(userID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "connection options temporarily unavailable")
	}

	return &heyearth_v1.ConnectionBundleResponse{
		UserId:                userID,
		ConnectionOptions:     options,
		StatusMessage:         "Connection bundle is ready.",
		TrafficMaskingEnabled: access.TrafficMaskingAvailable,
	}, nil
}

// ─────────────── Inputs of the subscription profiles: strict reads ───────────────
// Every client profile (gRPC GetConnectionBundle, REST /api/v1/user/connections,
// the Happ / V2Ray / Clash subscription link) is built from one
// hubConnectionInputs. Clients REPLACE their cached profile with whatever a
// successful answer contains, so a read that fails must fail the request (503 /
// Unavailable, the client keeps its cache) — never fall back to a default and
// serve a profile without VLESS or Bamboo entries, with a default SNI or host.
// An absent or blank setting still yields its documented default.

// hubConnectionInputs is everything the connection options and the Clash
// profile of one user are built from.
type hubConnectionInputs struct {
	mainServerHost string // main_server_host ("" = no Hub profiles)
	vlessUUID      string // per-user VLESS UUID, never the server master UUID
	plan           *biz.Plan
	canVLESS       bool // plan allows vless (no active plan = allowed)

	realityPubKey  string // reality_public_key ("" = no VLESS profiles)
	realityShortID string // reality_short_id
	realitySNI     string // reality_sni_domain (biz.DefaultRealitySNI)
	realityFlow    string // reality_flow (xtls-rprx-vision)
	xhttpPort      string // xhttp_port (2443)

	canHysteria2 bool   // the Express plan gate (biz.CanUseHysteria2)
	hy2Password  string // per-user Hysteria2 password (only with canHysteria2)
	obfsPassword string // shared Salamander password (only with canHysteria2)
	hysteria2SNI string // hub_main_server_host (hiearth.ru): the cert domain

	entries entryPlan // where the lines point (phase 3): reserves, failover, update interval
}

// subscriptionSetting reads an app_settings value for a client profile:
// default only for an absent/blank key, an error for a failed read.
func subscriptionSetting(key, def string) (string, error) {
	return withSyncTimeout(func(ctx context.Context) (string, error) {
		return settingForSync(ctx, biz.GetSettingContext, key, def)
	})
}

// loadHubSettingInputs reads the user-independent settings of the profiles
// (also used by the admin preview of the entries). Defaults only for
// absent/blank keys, an error for a failed read.
func loadHubSettingInputs(in *hubConnectionInputs) error {
	var err error
	if in.mainServerHost, err = subscriptionSetting("main_server_host", ""); err != nil {
		return err
	}
	for _, r := range []struct {
		dst      *string
		key, def string
	}{
		{&in.realityPubKey, "reality_public_key", ""},
		{&in.realityShortID, "reality_short_id", ""},
		{&in.realitySNI, "reality_sni_domain", biz.DefaultRealitySNI},
		{&in.realityFlow, "reality_flow", "xtls-rprx-vision"},
		{&in.xhttpPort, "xhttp_port", "2443"},
	} {
		if *r.dst, err = subscriptionSetting(r.key, r.def); err != nil {
			return err
		}
	}
	return nil
}

// loadHubConnectionInputs reads the inputs of userID's profiles. The first
// failed read is returned; nothing is defaulted on an error.
func loadHubConnectionInputs(userID string) (hubConnectionInputs, error) {
	var in hubConnectionInputs
	var err error
	if err = loadHubSettingInputs(&in); err != nil {
		return in, err
	}

	// Per-user UUID. NEVER fall back to the server master UUID
	// (app_settings.vless_uuid): it bypasses per-user accounting, quota and
	// revocation (H7). A lookup failure is an error for the caller.
	if in.vlessUUID, err = biz.GetUserVlessUUID(userID); err != nil {
		log.Printf("[connection-options] user %s: vless uuid lookup failed: %v", userID, err)
		return in, fmt.Errorf("vless uuid: %w", err)
	}

	// The user's plan: protocol restrictions and the Express speeds (Clash).
	if in.plan, err = biz.ActiveUserPlan(userID); err != nil {
		log.Printf("[connection-options] user %s: plan lookup failed: %v", userID, err)
		return in, err
	}
	in.canVLESS = biz.PlanAllowsProtocol(in.plan, "vless")

	// Same plan gate as the auth backend. A DB failure is an error, not "no
	// Bamboo": the caller answers 503 so clients keep their cached profiles.
	if in.canHysteria2, err = biz.CanUseHysteria2(userID); err != nil {
		log.Printf("[connection-options] user %s: hysteria2 gate failed: %v", userID, err)
		return in, err
	}
	if in.canHysteria2 {
		if in.hy2Password, err = biz.GetUserHysteria2Password(userID); err != nil {
			log.Printf("[connection-options] user %s: hysteria2 password failed: %v", userID, err)
			return in, err
		}
		if in.obfsPassword, err = biz.Hysteria2ObfsPassword(); err != nil {
			return in, err
		}
		// SNI MUST match the Hysteria2 server's TLS cert (see hubConnectionOptions).
		if in.hysteria2SNI, err = subscriptionSetting("hub_main_server_host", "hiearth.ru"); err != nil {
			return in, err
		}
	}

	// Reserve entries and the failover: a failed read is a 503 too — a profile
	// without the reserve lines (or pointing at a blocked Hub during a failover)
	// would replace the cached one.
	if in.entries, err = loadEntryPlan(userID, in.mainServerHost); err != nil {
		log.Printf("[connection-options] user %s: entries: %v", userID, err)
		return in, err
	}
	return in, nil
}

// buildHubConnectionOptions returns connection options pointing to the main server.
// In hub-and-spoke model, all users connect to the main server only.
// Каждый пользователь получает уникальные подключения (per-user VLESS UUID).
// Only VLESS is exposed to users now. SOCKS/HTTP user-facing inbounds removed (see spec 2026-05-22-socks5-removal-mtproto-perk-design.md).
// Any failed read is an error (see loadHubConnectionInputs): callers answer 503
// / Unavailable.
func buildHubConnectionOptions(userID string) ([]*heyearth_v1.ConnectionOption, error) {
	in, err := loadHubConnectionInputs(userID)
	if err != nil {
		return nil, err
	}
	return hubConnectionOptions(in), nil
}

// hubConnectionOptions builds the connection options from in. Pure.
func hubConnectionOptions(in hubConnectionInputs) []*heyearth_v1.ConnectionOption {
	mainServerHost, userVlessUUID, canVLESS := in.mainServerHost, in.vlessUUID, in.canVLESS
	// Where the main Берёза/Дуб/Бамбук lines connect: the failover target, or
	// the Hub. The Дуб Host header and the Бамбук SNI never follow it.
	connect := in.entries.main
	if connect == "" {
		connect = mainServerHost
	}
	// The main Бамбук follows the target only when Бамбук answers through it.
	bambooHost := connect
	if in.entries.failover && !in.entries.mainUDP {
		bambooHost = mainServerHost
	}
	rp := realityParams{uuid: userVlessUUID, sni: in.realitySNI, pbk: in.realityPubKey, sid: in.realityShortID}
	if in.realityFlow != "" {
		rp.flowParam = "&flow=" + in.realityFlow
	}

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" && userVlessUUID != "" {
		// 1. VLESS Reality (максимальная защита от DPI) — only if plan allows vless
		if in.realityPubKey != "" && canVLESS {
			// 🌴 Пальма (AmneziaWG) — server-side infrastructure ready (biz/awg.go,
			// awg_sync.go, awg.service on :51820, per-plan amnezia_wg_enabled flag,
			// per-user keys + CPS via biz.GetUserAWGConfig) but NOT emitted in happ
			// subscription because happ uses xray-core + sing-box internally — neither
			// supports AmneziaWG. happ silently drops amneziawg:// URIs.
			//
			// Future integration via dedicated AmneziaVPN client: separate endpoint
			// GET /api/v1/user/awg-config returns WG-INI as text/plain, site shows
			// QR + .conf download for users on amnezia_wg_enabled plans. See beads
			// heyearthproject-v7lb for the planned UI work.
			//
			// DO NOT delete the biz.GetUserAWGConfig path here — left as inline
			// comment for grep continuity. To re-enable Palma in a happ-compatible
			// way (if/when happ adds amneziawg:// support OR via wireguard:// stub):
			// 1. Uncomment the emit block (see git history at commit 42a51d6)
			// 2. Restore Priority arithmetic: realityFPs +1, xhttpFPs +3, bambooVariants 5/6

			// FP matrix — happ-desktop sees different uTLS ClientHello JA3s for
			// each entry. Reality TCP shares one inbound — all FPs fail together
			// when DPI catches by IP/port. 4-FP defense-in-depth was overkill;
			// chrome+safari covers the JA3-fingerprinting case where one specific
			// FP happens to be whitelisted by an operator's DPI (rare, but cheap
			// to keep). uTLS FP is purely client-side — server (Xray) doesn't
			// validate it, so both entries share the same vless-reality inbound
			// on :443.
			//
			// PathEscape (not QueryEscape): RFC 3986 fragment encoding emits %20
			// for spaces; QueryEscape's `+` is rendered literally by happ-desktop.
			//
			// 🌳 Берёза 1..2 — TCP Reality :443, 2 uTLS variants.
			// URI params restored to pre-Hysteria2 working state — NO spx, NO fragment,
			// NO alt-port/alt-IP variants. User-reported these additions broke
			// the previously-working Reality path. Keep tcpKeepAlive* and flow at end
			// (load-bearing for happ-desktop URI parser, verified working pre-Hysteria2).
			//
			// uTLS fingerprints: chrome + firefox.
			// Rationale (research: docs/research/2026-05-27-happ-tele2-tls-handshake.md):
			// fp=ios and fp=safari have documented Xray-core regressions — XTLS #5918
			// ("firstLen=0, EOF" on iOS+XHTTP: TLS handshake completes but HTTP data
			// never flows) and XTLS #4852 (uTLS ClientHello extension order). These
			// regressions manifest on Tele2 mobile DPI as «плавающая ошибка TLS
			// рукопожатия» in Happ ping check. Chrome is the most-tested uTLS profile
			// (used by 80% of Reality clients) and has no open regressions. Firefox
			// is the second-most-tested, kept as JA3 diversification within one
			// subscription.
			realityFPs := []string{"chrome", "firefox"}
			for i, fp := range realityFPs {
				label := fmt.Sprintf("🌳 Берёза %d", i+1)
				opts = append(opts, vlessOption(rp.tcpURI(connect, fp, label), label, int32(i))) // 0..1
			}

			// 🌲 Дуб 1..2 — XHTTP Reality :2443, 2 uTLS variants.
			// Pre-Hysteria2 working URI shape — no spx, no fragment.
			// Same rationale as realityFPs above (research 2026-05-27) — chrome + firefox
			// to avoid ios/safari XTLS regressions, especially XHTTP-specific #5918.
			xhttpFPs := []string{"chrome", "firefox"}
			for i, fp := range xhttpFPs {
				label := fmt.Sprintf("🌲 Дуб %d", i+1)
				opts = append(opts, vlessOption(rp.xhttpURI(connect, in.xhttpPort, fp, mainServerHost, label), label, int32(i+2))) // 2..3
			}
		}

	}

	// 2. Hysteria2 «Бамбук» — QUIC/UDP, Wi-Fi only (mobile carriers QoS UDP).
	// Two entries differ only in congestion control:
	//   Бамбук 1 — no bandwidth in URI → Hysteria2 client picks BBR (adaptive)
	//   Бамбук 2 — explicit upmbps/downmbps → Hysteria2 client picks Brutal CC
	//              (fixed bandwidth, ignores loss-induced backoff — beats operator
	//              QoS that throttles UDP via packet loss injection)
	// Both labels contain "only WiFi" — happ natively hides them on cellular.
	if mainServerHost != "" && in.canHysteria2 {
		hy2Password, obfsPassword := in.hy2Password, in.obfsPassword
		// SNI MUST match the Hysteria2 server's TLS cert. The server uses a
		// Let's Encrypt cert for hiearth.ru (SAN: hiearth.ru, www, admin — NO IP).
		// main_server_host became the bare IP 45.150.39.236 on 2026-05-26, so using
		// it as the URI sni with insecure=0 made every QUIC TLS handshake fail
		// ("Бамбук не отвечает"). Connect host stays the IP (no DNS dependency),
		// but sni uses the cert domain from hub_main_server_host.
		hysteria2SNI := in.hysteria2SNI
		if hy2Password != "" && obfsPassword != "" {
			// Two Бамбук variants (bambooVariants) — identical except for the
			// bandwidth params that flip Hysteria2's congestion control.
			for _, v := range bambooVariants {
				label := fmt.Sprintf("🎋 %s only WiFi", v.name)
				opts = append(opts, bambooOption(bambooURI(hy2Password, bambooHost, obfsPassword, hysteria2SNI, v.bwParams, label), label, v.priority))
			}
		}
	}

	opts = append(opts, directOptions(in, rp)...)
	opts = append(opts, reserveOptions(in, rp)...)
	return opts
}

// directOptions: while everyone is pinned to a relay, one Берёза and one Дуб
// (fp chrome) straight to the Hub — the way out on Wi-Fi if the relay dies.
// Priorities 10..11: after the main lines, before the reserves.
func directOptions(in hubConnectionInputs, rp realityParams) []*heyearth_v1.ConnectionOption {
	if !in.entries.pinned || in.mainServerHost == "" || rp.uuid == "" || rp.pbk == "" || !in.canVLESS {
		return nil
	}
	label := "🌳 Берёза" + directLabel
	opts := []*heyearth_v1.ConnectionOption{vlessOption(rp.tcpURI(in.mainServerHost, "chrome", label), label, 10)}
	label = "🌲 Дуб" + directLabel
	return append(opts, vlessOption(rp.xhttpURI(in.mainServerHost, in.xhttpPort, "chrome", in.mainServerHost, label), label, 11))
}

// ── Entries (phase 3): reserve lines and the failover of the main address ──
//
// A reserve entry is a RU relay (servers.role 'relay') that DNATs the Hub's
// user ports to the Hub (internal/config/relay_nft.go): TCP 443/2443 to the
// same ports, the whole UDP hop range 30000-65000 to Hub:50443. A line through
// it is the Hub's line with the relay's address in front of the same ports —
// the Reality SNI/keys, the Дуб Host header (main_server_host) and the Бамбук
// SNI (the cert domain) stay the Hub's.

// defaultUpdateIntervalHours is Profile-Update-Interval without the setting
// (matches the auto-tuner); 1 h during a failover.
const defaultUpdateIntervalHours = 4

type reserveEntry struct {
	id      int
	address string
	udp     bool // Бамбук answers through this entry (its UDP check is healthy)
}

// entryPlan says where the lines point. main "" means mainServerHost.
type entryPlan struct {
	main     string
	failover bool // main is the failover target
	mainUDP  bool // Бамбук answers through the target: the main Бамбук lines follow it
	pinned   bool // the owner sent everyone through the target: «· напрямую» lines, normal interval
	reserves []reserveEntry
	interval int // Profile-Update-Interval, hours
}

// planEntries: reserves = online relays with a healthy external check and an
// address, by id. During a failover (global, or forced for the test user) the
// main lines go to the target — only if it is a healthy reserve right now (a
// stale flag never points users at a dead relay), the main Бамбук only when
// its UDP check is healthy too — and the interval drops to 1 h either way, so
// clients pick up the return quickly. A pinned failover on a healthy target is
// the owner's standing choice: the normal interval stays. Pure.
func planEntries(mainHost string, relays []biz.Entry, fo biz.EntryFailover, forced bool, intervalRaw string) entryPlan {
	p := entryPlan{main: mainHost, interval: defaultUpdateIntervalHours}
	if h, err := strconv.Atoi(strings.TrimSpace(intervalRaw)); err == nil && h >= 1 && h <= 24 {
		p.interval = h
	}
	normal := p.interval
	for _, r := range relays {
		if r.Kind == biz.EntryKindRelay && r.Lifecycle == "online" && r.Health.External == biz.EntryHealthy && r.Address != "" {
			p.reserves = append(p.reserves, reserveEntry{id: r.ID, address: r.Address, udp: r.Health.UDP == biz.EntryHealthy})
		}
	}
	sort.Slice(p.reserves, func(i, j int) bool { return p.reserves[i].id < p.reserves[j].id })
	if !fo.Active && !forced {
		return p
	}
	p.interval = 1
	target := fo.Target
	if !fo.Active && len(p.reserves) > 0 {
		target = p.reserves[0].id
	}
	for _, r := range p.reserves {
		if r.id == target {
			p.main, p.failover, p.mainUDP = r.address, true, r.udp
			if fo.Active && fo.Pinned && r.id == fo.Target {
				p.pinned, p.interval = true, normal
			}
			break
		}
	}
	return p
}

// shownReserves: the reserves that get lines of their own — all of them, except
// the pinned target (the main lines already point at it).
func shownReserves(p entryPlan) []reserveEntry {
	if !p.pinned {
		return p.reserves
	}
	var out []reserveEntry
	for _, r := range p.reserves {
		if r.address != p.main {
			out = append(out, r)
		}
	}
	return out
}

// directLabel marks a line that goes straight to the Hub while everyone is pinned to a relay.
const directLabel = " · напрямую"

// loadEntryPlan reads the inputs of planEntries; every read is strict (an
// error, never "no reserves" / "no failover").
func loadEntryPlan(userID, mainHost string) (entryPlan, error) {
	relays, err := withSyncTimeout(biz.ListRelayEntries)
	if err != nil {
		return entryPlan{}, err
	}
	fo, err := withSyncTimeout(biz.GetEntryFailover)
	if err != nil {
		return entryPlan{}, err
	}
	testUser, err := subscriptionSetting("entry_failover_test_user", "")
	if err != nil {
		return entryPlan{}, err
	}
	interval, err := subscriptionSetting("subscription_update_interval_hours", strconv.Itoa(defaultUpdateIntervalHours))
	if err != nil {
		return entryPlan{}, err
	}
	return planEntries(mainHost, relays, fo, userID != "" && testUser == userID, interval), nil
}

// reserveSuffix labels the k-th reserve (0-based): " · резерв", " · резерв 2", …
func reserveSuffix(k int) string {
	if k == 0 {
		return " · резерв"
	}
	return fmt.Sprintf(" · резерв %d", k+1)
}

// realityParams are the Reality values shared by every VLESS line of a user.
type realityParams struct{ uuid, sni, pbk, sid, flowParam string }

func (p realityParams) tcpURI(host, fp, label string) string {
	return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
		p.uuid, host, p.sni, fp, p.pbk, p.sid, p.flowParam, url.PathEscape(label))
}

// xhttpURI: hostHeader is ALWAYS the Hub's main_server_host — the XHTTP inbound checks it.
func (p realityParams) xhttpURI(host, port, fp, hostHeader, label string) string {
	return fmt.Sprintf("vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=xhttp&path=%%2F&host=%s&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
		p.uuid, host, port, p.sni, fp, p.pbk, p.sid, url.QueryEscape(hostHeader), url.PathEscape(label))
}

// bambooURI: sni is the cert domain (hub_main_server_host), never the connect
// host. hop_interval=15s — Hysteria2 client rotates source port every 15
// seconds. Mobile carrier (Tele2) UDP-QoS sliding-window is typically 20-60s;
// 15s rotation beats heuristic before pattern stabilizes. Default would be
// happ-client's internal value (often 30s+). Verified safe per
// docs/research/2026-05-26-mobile-dpi-tele2.md.
func bambooURI(password, host, obfs, sni, bwParams, label string) string {
	return fmt.Sprintf("hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=30000-65000&hop_interval=15%s#%s",
		url.QueryEscape(password), host, url.QueryEscape(obfs), sni, bwParams, url.PathEscape(label))
}

// bambooVariants — identical except for the bandwidth params that flip
// Hysteria2's congestion control:
//
//	BBR (no bandwidth) — adaptive, learns network
//	Brutal (with upmbps/downmbps) — fixed rate, beats operator UDP QoS
//
// Note: "only WiFi" in the label is the happ-native filter token — exact
// spelling matters (verified per happ docs 2026-05-26). Do not change.
var bambooVariants = []struct {
	name     string // "Бамбук 1" / "Бамбук 2"
	bwParams string // "" → BBR; "&upmbps=N&downmbps=N" → Brutal CC
	priority int32
}{
	{"Бамбук 1", "", 4},
	{"Бамбук 2", "&upmbps=20&downmbps=80", 5},
}

func vlessOption(uri, label string, prio int32) *heyearth_v1.ConnectionOption {
	return &heyearth_v1.ConnectionOption{Uri: uri, Protocol: "vless", Label: label,
		RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"}, Priority: prio}
}

func bambooOption(uri, label string, prio int32) *heyearth_v1.ConnectionOption {
	return &heyearth_v1.ConnectionOption{Uri: uri, Protocol: "hysteria2", Label: label,
		RecommendedNetworks: []string{"wifi", "ethernet"}, Priority: prio}
}

// reserveOptions: per healthy reserve one Берёза and one Дуб (fp chrome) and,
// when Бамбук answers through it, both Бамбук variants; after every main line
// (priority 20 + 10·k). Same plan gates as the main lines.
func reserveOptions(in hubConnectionInputs, rp realityParams) []*heyearth_v1.ConnectionOption {
	if in.mainServerHost == "" {
		return nil
	}
	var opts []*heyearth_v1.ConnectionOption
	for k, r := range shownReserves(in.entries) {
		sfx, base := reserveSuffix(k), int32(20+10*k)
		if rp.uuid != "" && rp.pbk != "" && in.canVLESS {
			label := "🌳 Берёза" + sfx
			opts = append(opts, vlessOption(rp.tcpURI(r.address, "chrome", label), label, base))
			label = "🌲 Дуб" + sfx
			opts = append(opts, vlessOption(rp.xhttpURI(r.address, in.xhttpPort, "chrome", in.mainServerHost, label), label, base+1))
		}
		if in.canHysteria2 && in.hy2Password != "" && in.obfsPassword != "" && r.udp {
			for i, v := range bambooVariants {
				label := fmt.Sprintf("🎋 %s only WiFi%s", v.name, sfx)
				opts = append(opts, bambooOption(bambooURI(in.hy2Password, r.address, in.obfsPassword, in.hysteria2SNI, v.bwParams, label), label, base+2+int32(i)))
			}
		}
	}
	return opts
}

// ─────────────────────── Subscriptions & Payments ───────────────────────

func (s *server) ListSubscriptions(ctx context.Context, req *heyearth_v1.ListSubscriptionsRequest) (*heyearth_v1.ListSubscriptionsResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	items, err := biz.ListUserSubscriptions(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list subscriptions: %v", err)
	}
	response := &heyearth_v1.ListSubscriptionsResponse{Items: make([]*heyearth_v1.SubscriptionItem, 0, len(items))}
	for _, item := range items {
		// GroupId/GroupName were replaced by plan_id/plan_name in the data model,
		// but the proto field tags are still named group_* for wire compatibility.
		response.Items = append(response.Items, &heyearth_v1.SubscriptionItem{
			GroupId:    int32(item.PlanID),
			GroupName:  item.PlanName,
			ValidUntil: item.ValidUntil,
			AutoRenew:  item.AutoRenew,
			Active:     item.Active,
			Plan:       item.Plan,
			PriceRub:   int32(item.PriceRub),
		})
	}
	return response, nil
}

func (s *server) ListPayments(ctx context.Context, req *heyearth_v1.ListPaymentsRequest) (*heyearth_v1.ListPaymentsResponse, error) {
	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	items, err := biz.ListUserPayments(userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list payments: %v", err)
	}
	response := &heyearth_v1.ListPaymentsResponse{Items: make([]*heyearth_v1.PaymentItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, &heyearth_v1.PaymentItem{
			Id:          int32(item.ID),
			Amount:      item.Amount,
			Currency:    item.Currency,
			Status:      item.Status,
			Provider:    item.Provider,
			ExternalId:  item.ExternalID,
			Notes:       item.Notes,
			CreatedAt:   item.CreatedAt,
			ConfirmedAt: item.ConfirmedAt,
		})
	}
	return response, nil
}

// ─────────────────────── Converters ───────────────────────

func profileToProto(profile *biz.UserProfile) *heyearth_v1.UserProfileResponse {
	if profile == nil {
		return &heyearth_v1.UserProfileResponse{}
	}
	return &heyearth_v1.UserProfileResponse{
		UserId:            profile.UserID,
		Username:          profile.Username,
		HasPassword:       profile.HasPassword,
		PreferredPlatform: profile.PreferredPlatform,
		Locale:            profile.Locale,
		IsBlocked:         profile.IsBlocked,
		CurrentPlan:       profile.CurrentPlan,
		ValidUntil:        profile.ValidUntil,
		AccessStatus:      profile.AccessStatus,
		StatusReason:      profile.StatusReason,
	}
}

func accessToProto(access *biz.UserAccessState) *heyearth_v1.AccessStateResponse {
	if access == nil {
		return &heyearth_v1.AccessStateResponse{}
	}
	return &heyearth_v1.AccessStateResponse{
		UserId:                  access.UserID,
		CurrentPlan:             access.CurrentPlan,
		ValidUntil:              access.ValidUntil,
		PreferredPlatform:       access.PreferredPlatform,
		IsBlocked:               access.IsBlocked,
		CanConnect:              access.CanConnect,
		Reason:                  access.Reason,
		IsPremium:               access.IsPremium,
		TrafficMaskingAvailable: access.TrafficMaskingAvailable,
	}
}

// deviceFromProto: what a new account keeps of the device — its platform and locale.
func deviceFromProto(device *heyearth_v1.DeviceContext) biz.DeviceRegistration {
	return biz.DeviceRegistration{Platform: device.GetPlatform(), Locale: device.GetLocale()}
}

// ─────────────────────── JWT ───────────────────────

func issueUserJWT(userID string, userDBID int) (string, error) {
	token, err := biz.GenerateUserSessionToken(userID, userDBID)
	if err != nil {
		return "", err
	}
	return token, nil
}
