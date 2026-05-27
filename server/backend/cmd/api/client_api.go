package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─────────────────────── Auth RPCs ───────────────────────

func (s *server) Register(ctx context.Context, req *heyearth_v1.RegisterRequest) (*heyearth_v1.AuthResponse, error) {
	device := deviceFromProto(req.GetDevice(), "register")

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

	if req.GetDevice() != nil {
		_ = biz.RegisterUserDevice(user.UserID, deviceFromProto(req.GetDevice(), "login"))
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
	if req.GetDevice() != nil {
		_ = biz.RegisterUserDevice(userID, deviceFromProto(req.GetDevice(), "app_open"))
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
		return nil, status.Errorf(codes.Internal, "get access state: %v", err)
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
		return nil, status.Errorf(codes.Internal, "get access state: %v", err)
	}
	if access.IsBlocked {
		return nil, status.Error(codes.PermissionDenied, "user is blocked")
	}
	if !access.CanConnect {
		return nil, status.Errorf(codes.PermissionDenied, "cannot connect: %s", access.Reason)
	}

	// Hub-and-spoke: return main server connection info (per-user UUID)
	options := buildHubConnectionOptions(userID)

	return &heyearth_v1.ConnectionBundleResponse{
		UserId:                userID,
		ConnectionOptions:     options,
		StatusMessage:         "Connection bundle is ready.",
		TrafficMaskingEnabled: access.TrafficMaskingAvailable,
	}, nil
}

// buildHubConnectionOptions returns connection options pointing to the main server.
// In hub-and-spoke model, all users connect to the main server only.
// Каждый пользователь получает уникальные подключения (per-user VLESS UUID).
// Only VLESS is exposed to users now. SOCKS/HTTP user-facing inbounds removed (see spec 2026-05-22-socks5-removal-mtproto-perk-design.md).
func buildHubConnectionOptions(userID string) []*heyearth_v1.ConnectionOption {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")

	// Per-user UUID используется как пароль для прокси-авторизации
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")
	}

	// Check user's plan for protocol restrictions
	canVLESS := biz.IsProtocolAllowed(userID, "vless")

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" && userVlessUUID != "" {
		// 1. VLESS Reality (максимальная защита от DPI) — only if plan allows vless
		realityPubKey := biz.GetSettingOrDefault("reality_public_key", "")
		realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
		realitySNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
		if realityPubKey != "" && canVLESS {
			flow := biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision")
			flowParam := ""
			if flow != "" {
				flowParam = "&flow=" + flow
			}

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
			// uTLS fingerprints: ios + safari (both Apple-style, less commonly fingerprinted
// by RU TSPU). chrome was previously the first fingerprint but user reports
// Берёза 1 (chrome) and Дуб 1 (chrome) both fail to connect ("n/a"), while
// Берёза 2 and Дуб 2 (safari) work — TSPU has started targeting Chrome JA3
// signatures specifically because Chrome is the most popular uTLS profile
// used by VPN clients. Switching the first profile to ios (Apple mobile)
// keeps two distinct fingerprints for diversity and avoids the blocklist.
realityFPs := []string{"ios", "safari"}
			for i, fp := range realityFPs {
				label := fmt.Sprintf("🌳 Берёза %d", i+1)
				vlessURI := fmt.Sprintf(
					"vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
					userVlessUUID, mainServerHost, realitySNI, fp, realityPubKey, realityShortID, flowParam,
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 vlessURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(i), // 0..1
				})
			}

			// 🌲 Дуб 1..2 — XHTTP Reality :2443, 2 uTLS variants.
			// Pre-Hysteria2 working URI shape — no spx, no fragment.
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			// Same rationale as realityFPs above — chrome → ios для Дуб 1.
xhttpFPs := []string{"ios", "safari"}
			for i, fp := range xhttpFPs {
				label := fmt.Sprintf("🌲 Дуб %d", i+1)
				xhttpURI := fmt.Sprintf(
					"vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=xhttp&path=%%2F&host=%s&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
					userVlessUUID, mainServerHost, xhttpPort, realitySNI, fp, realityPubKey, realityShortID,
					url.QueryEscape(mainServerHost),
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 xhttpURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(i + 2), // 2..3
				})
			}
		}

		// ☁️ Облако (CDN) — VLESS over WebSocket through Cloudflare. Bypasses
		// per-IP operator blocks (Tele2 flapping, MTS throttling) by routing
		// user traffic through CF edge IP pool (millions of IPs). Trades Reality
		// stealth for CF's blanket-blocking immunity. Use when Berëza/Дуб are
		// blocked at the operator level. Toggle-gated via app_settings.cdn_enabled
		// for zero-deploy rollback. Priority 10 — displayed last; happ users try
		// faster Reality profiles first, fall back to Облако if blocked.
		if canVLESS && biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {
			cdnHost := biz.GetSettingOrDefault("cdn_host", "cdn.hiearth.ru")
			cdnPath := biz.GetSettingOrDefault("cdn_ws_path", "/cdn-ws")
			if cdnHost != "" {
				cdnLabel := "☁️ Облако (CDN)"
				cdnURI := fmt.Sprintf(
					"vless://%s@%s:443?encryption=none&security=tls&sni=%s&type=ws&host=%s&path=%s&fp=chrome&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
					userVlessUUID, cdnHost, cdnHost, cdnHost,
					url.QueryEscape(cdnPath),
					url.PathEscape(cdnLabel))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 cdnURI,
					Protocol:            "vless",
					Label:               cdnLabel,
					RecommendedNetworks: []string{"cellular", "wifi", "ethernet", "any"},
					Priority:            10, // displayed last — fallback when Reality direct fails
				})
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
	if mainServerHost != "" {
		hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)
		obfsPassword := biz.GetHysteria2ObfsPassword()
		if hy2Err == nil && hy2Password != "" && obfsPassword != "" {
			// Two Бамбук variants — identical except for the bandwidth params that
			// flip Hysteria2's congestion control:
			//   BBR (no bandwidth) — adaptive, learns network
			//   Brutal (with upmbps/downmbps) — fixed rate, beats operator UDP QoS
			// Note: "only WiFi" in label is the happ-native filter token — exact
			// spelling matters (verified per happ docs 2026-05-26). Do not change.
			bambooVariants := []struct {
				name     string // "Бамбук 1" / "Бамбук 2"
				bwParams string // "" → BBR; "&upmbps=N&downmbps=N" → Brutal CC
				priority int32
			}{
				{"Бамбук 1", "", 4},
				{"Бамбук 2", "&upmbps=20&downmbps=80", 5},
			}
			for _, v := range bambooVariants {
				label := fmt.Sprintf("🎋 %s only WiFi", v.name)
				// hop_interval=15s — Hysteria2 client rotates source port every 15 seconds.
				// Mobile carrier (Tele2) UDP-QoS sliding-window is typically 20-60s; 15s
				// rotation beats heuristic before pattern stabilizes. Default would be
				// happ-client's internal value (often 30s+). Verified safe per
				// docs/research/2026-05-26-mobile-dpi-tele2.md.
				uri := fmt.Sprintf(
					"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=30000-65000&hop_interval=15%s#%s",
					url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
					v.bwParams, url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 uri,
					Protocol:            "hysteria2",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "ethernet"},
					Priority:            v.priority,
				})
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

func deviceFromProto(device *heyearth_v1.DeviceContext, linkedVia string) biz.DeviceRegistration {
	if device == nil {
		return biz.DeviceRegistration{LinkedVia: linkedVia}
	}
	return biz.DeviceRegistration{
		DeviceID:    device.GetDeviceId(),
		Platform:    device.GetPlatform(),
		DeviceLabel: device.GetDeviceLabel(),
		Locale:      device.GetLocale(),
		LinkedVia:   firstNonEmpty(device.GetLinkedVia(), linkedVia),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ─────────────────────── JWT ───────────────────────

func issueUserJWT(userID string, userDBID int) (string, error) {
	token, err := biz.GenerateUserSessionToken(userID, userDBID)
	if err != nil {
		return "", err
	}
	return token, nil
}
